package main

import (
    "bytes"
    "crypto/hmac"
    "crypto/sha256"
    "encoding/json"
    "fmt"
    "io/ioutil"
    "log"
    "math"
    "net/http"
    "sort"
    "strconv"
    "sync/atomic"
    "time"
)

const REST_URL = "https://api.mexc.com"

type Listing struct {
    APIKey         string  `json:"api_key"`
    APISecret      string  `json:"api_secret"`
    Symbol         string  `json:"symbol"`
    QuoteAmount    float64 `json:"quote_amount"`
    ListingTime    string  `json:"listing_time"`
    PriceMarkupPct float64 `json:"price_markup_pct"`
    ProfitPct      float64 `json:"profit_pct"`
}

func must(err error) {
    if err != nil {
        log.Fatal(err)
    }
}

// HMAC SHA256 w kolejności alfabetycznej kluczy
func sign(params map[string]string, secret string) string {
    keys := make([]string, 0, len(params))
    for k := range params {
        keys = append(keys, k)
    }
    sort.Strings(keys)
    var buf bytes.Buffer
    for i, k := range keys {
        if i > 0 {
            buf.WriteString("&")
        }
        buf.WriteString(fmt.Sprintf("%s=%s", k, params[k]))
    }
    mac := hmac.New(sha256.New, []byte(secret))
    mac.Write(buf.Bytes())
    return fmt.Sprintf("%x", mac.Sum(nil))
}

func httpGet(client *http.Client, url string, headers map[string]string, qs map[string]string) ([]byte, error) {
    req, _ := http.NewRequest("GET", url, nil)
    q := req.URL.Query()
    for k, v := range qs {
        q.Set(k, v)
    }
    req.URL.RawQuery = q.Encode()
    for k, v := range headers {
        req.Header.Set(k, v)
    }
    resp, err := client.Do(req)
    if err != nil {
        return nil, err
    }
    defer resp.Body.Close()
    return ioutil.ReadAll(resp.Body)
}

func httpPost(client *http.Client, url string, headers map[string]string, qs map[string]string) ([]byte, error) {
    req, _ := http.NewRequest("POST", url, nil)
    q := req.URL.Query()
    for k, v := range qs {
        q.Set(k, v)
    }
    req.URL.RawQuery = q.Encode()
    for k, v := range headers {
        req.Header.Set(k, v)
    }
    resp, err := client.Do(req)
    if err != nil {
        return nil, err
    }
    defer resp.Body.Close()
    return ioutil.ReadAll(resp.Body)
}

func main() {
    // 1) Wczytaj listing
    data, err := ioutil.ReadFile("current_listing.json")
    must(err)
    var l Listing
    must(json.Unmarshal(data, &l))
    log.Printf("[INFO] %s @ %s, amount=%.4f, markup=%.2f%%, profit=%.2f%%",
        l.Symbol, l.ListingTime, l.QuoteAmount, l.PriceMarkupPct, l.ProfitPct)

    client := &http.Client{}

    // 2) Oblicz T0
    t0, err := time.Parse(time.RFC3339, l.ListingTime)
    must(err)
    t0ms := t0.UTC().UnixNano() / int64(time.Millisecond)

    // 3) Sync offset
    tmData, _ := httpGet(client, REST_URL+"/api/v3/time", nil, nil)
    var tm struct{ ServerTime int64 `json:"serverTime"` }
    must(json.Unmarshal(tmData, &tm))
    offset := tm.ServerTime - time.Now().UnixNano()/1e6
    log.Printf("[SYNC] offset: %d ms", offset)

    // 4) Warmup
    warmupTs := strconv.FormatInt(time.Now().UnixNano()/1e6-offset-100000, 10)
    warmupParams := map[string]string{
        "symbol":        l.Symbol,
        "side":          "BUY",
        "type":          "MARKET",
        "quoteOrderQty": "0.000001",
        "recvWindow":    "2000",
        "timestamp":     warmupTs,
    }
    warmupParams["signature"] = sign(warmupParams, l.APISecret)
    httpPost(client, REST_URL+"/api/v3/order", map[string]string{"X-MEXC-APIKEY": l.APIKey}, warmupParams)

    // 5) Pobierz depth i oblicz limit
    depthData, _ := httpGet(client, REST_URL+"/api/v3/depth", nil, map[string]string{"symbol": l.Symbol, "limit": "5"})
    var depth struct{ Asks [][]string `json:"asks"` }
    must(json.Unmarshal(depthData, &depth))
    if len(depth.Asks) == 0 {
        log.Fatal("brak orderbook")
    }
    price, _ := strconv.ParseFloat(depth.Asks[0][0], 64)
    limitPrice := math.Round(price*(1+l.PriceMarkupPct/100)*1e8) / 1e8
    log.Printf("[PREP] market=%.8f → limit=%.8f", price, limitPrice)

    qty := math.Round(l.QuoteAmount/limitPrice*1e6) / 1e6

    // 6) Wyślij 3 próby
    offsets := []int64{-10, -5, 0}
    var success atomic.Bool
    for _, off := range offsets {
        target := t0ms + off
        for time.Now().UnixNano()/1e6 < target-offset {
        }
        if success.Load() {
            break
        }
        params := map[string]string{
            "symbol":      l.Symbol,
            "side":        "BUY",
            "type":        "LIMIT",
            "price":       fmt.Sprintf("%.8f", limitPrice),
            "quantity":    fmt.Sprintf("%.6f", qty),
            "timeInForce": "IOC",
            "recvWindow":  "5000",
            "timestamp":   strconv.FormatInt(time.Now().UnixNano()/1e6+offset, 10),
        }
        params["signature"] = sign(params, l.APISecret)
        start := time.Now()
        body, _ := httpPost(client, REST_URL+"/api/v3/order", map[string]string{"X-MEXC-APIKEY": l.APIKey}, params)
        latency := float64(time.Since(start).Microseconds()) / 1000.0
        var resp map[string]interface{}
        json.Unmarshal(body, &resp)
        if eq, ok := resp["executedQty"].(float64); ok && eq > 0 {
            log.Printf("[BUY] OK qty=%.6f lat=%.2fms", eq, latency)
            success.Store(true)
            break
        }
    }

    if !success.Load() {
        log.Println("[BOT] no buy")
        return
    }

    // 7) SELL
    sellPrice := math.Round(limitPrice*(1+l.ProfitPct/100)*1e8) / 1e8
    sellQty := math.Round(qty*1e6) / 1e6
    params := map[string]string{
        "symbol":      l.Symbol,
        "side":        "SELL",
        "type":        "LIMIT",
        "price":       fmt.Sprintf("%.8f", sellPrice),
        "quantity":    fmt.Sprintf("%.6f", sellQty),
        "timeInForce": "GTC",
        "recvWindow":  "5000",
        "timestamp":   strconv.FormatInt(time.Now().UnixNano()/1e6+offset, 10),
    }
    params["signature"] = sign(params, l.APISecret)
    start := time.Now()
    httpPost(client, REST_URL+"/api/v3/order", map[string]string{"X-MEXC-APIKEY": l.APIKey}, params)
    log.Printf("[SELL] qty=%.6f price=%.8f lat=%.2fms", sellQty, sellPrice, float64(time.Since(start).Microseconds())/1000.0)
}
