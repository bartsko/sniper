package main

import (
    "bytes"
    "crypto/hmac"
    "crypto/sha256"
    "encoding/json"
    "fmt"
    "io/ioutil"
    "log"
    "net/http"
    "sort"
    "strconv"
    "strings"
    "sync/atomic"
    "time"
)

const REST_URL = "https://api.mexc.com"

type Listing struct {
    APIKey          string  `json:"api_key"`
    APISecret       string  `json:"api_secret"`
    Symbol          string  `json:"symbol"`
    QuoteAmount     float64 `json:"quote_amount"`
    ListingTime     string  `json:"listing_time"`
    PriceMarkupPct  float64 `json:"price_markup_pct"`
    ProfitPct       float64 `json:"profit_pct"`
}

func must(err error) {
    if err != nil {
        log.Fatal(err)
    }
}

// HMAC SHA256 w odpowiedniej kolejności kluczy
func sign(params map[string]string, secret string) string {
    // sort klucze w kolejności alfabetycznej (MEXC akceptuje)
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
    // 1) Wczytaj listing:
    b, err := ioutil.ReadFile("current_listing.json")
    must(err)
    var l Listing
    must(json.Unmarshal(b, &l))
    log.Printf("[INFO] %s @ %s, amount=%.4f, markup=%.2f%%, profit=%.2f%%",
        l.Symbol, l.ListingTime, l.QuoteAmount, l.PriceMarkupPct, l.ProfitPct)

    // 2) Przygotuj HTTP client
    client := &http.Client{}

    // 3) Oblicz moment T0 (epoch ms UTC)
    t0, err := time.Parse(time.RFC3339, l.ListingTime)
    must(err)
    t0ms := t0.UTC().UnixNano() / int64(time.Millisecond)

    // 4) Warmup & offset
    // fetch server time
    data, _ := httpGet(client, REST_URL+"/api/v3/time", nil, nil)
    var tm struct{ ServerTime int64 `json:"serverTime"` }
    must(json.Unmarshal(data, &tm))
    offset := tm.ServerTime - time.Now().UnixNano()/1e6
    log.Printf("[SYNC] offset: %d ms", offset)

    // dummy order
    warmupTs := strconv.FormatInt(time.Now().UnixNano()/1e6-offset-100000, 10)
    warmupParams := map[string]string{
        "symbol":       l.Symbol,
        "side":         "BUY",
        "type":         "MARKET",
        "quoteOrderQty": "0.000001",
        "recvWindow":   "2000",
        "timestamp":    warmupTs,
    }
    warmupParams["signature"] = sign(warmupParams, l.APISecret)
    httpPost(client, REST_URL+"/api/v3/order", map[string]string{"X-MEXC-APIKEY": l.APIKey}, warmupParams)

    // 5) Pobierz book i price
    depthData, _ := httpGet(client, REST_URL+"/api/v3/depth", nil, map[string]string{"symbol": l.Symbol, "limit": "5"})
    var depth struct{ Asks [][]string `json:"asks"` }
    must(json.Unmarshal(depthData, &depth))
    if len(depth.Asks) == 0 {
        log.Fatal("brak orderbook")
    }
    price, _ := strconv.ParseFloat(depth.Asks[0][0], 64)
    limitPrice := math.Round(price*(1+l.PriceMarkupPct/100)*1e8) / 1e8
    log.Printf("[PREP] market=%.8f → limit=%.8f", price, limitPrice)

    // 6) Przygotuj template
    template := map[string]string{
        "symbol":      l.Symbol,
        "side":        "BUY",
        "type":        "LIMIT",
        "price":       fmt.Sprintf("%.8f", limitPrice),
        "quantity":    "", // uzupełnimy poniżej
        "timeInForce": "IOC",
        "recvWindow":  "5000",
    }

    // 7) Oblicz qty
    qty := math.Round(l.QuoteAmount/limitPrice*1e6) / 1e6

    // 8) Wyślij 3 próby: T0–10ms, –5ms, 0
    offsets := []int64{-10, -5, 0}
    var success atomic.Bool
    type result struct {
        Sent    string
        Latency float64
        Status  string
        Qty     float64
        Msg     string
    }
    results := make([]result, 0, 3)

    for _, off := range offsets {
        target := t0ms + off
        for time.Now().UnixNano()/1e6 < target-offset {
            // busy-wait
        }
        if success.Load() {
            break
        }
        // build params
        params := make(map[string]string)
        for k, v := range template {
            params[k] = v
        }
        params["quantity"] = fmt.Sprintf("%.6f", qty)
        params["timestamp"] = strconv.FormatInt(time.Now().UnixNano()/1e6+offset, 10)
        params["signature"] = sign(params, l.APISecret)

        start := time.Now()
        body, _ := httpPost(client, REST_URL+"/api/v3/order", map[string]string{"X-MEXC-APIKEY": l.APIKey}, params)
        latency := float64(time.Since(start).Microseconds()) / 1000.0

        var resp map[string]interface{}
        json.Unmarshal(body, &resp)
        status := "ERR"
        executedQty := 0.0
        if id, ok := resp["orderId"]; ok {
            // jeśli mamy orderId, to patrzymy na executedQty
            executedQty = resp["executedQty"].(float64)
            if executedQty > 0 {
                status = "OK"
                success.Store(true)
            } else {
                status = "NOFILL"
            }
        }
        results = append(results, result{
            Sent:    start.Format("15:04:05.000"),
            Latency: latency,
            Status:  status,
            Qty:     executedQty,
            Msg:     fmt.Sprint(resp["msg"]),
        })
    }

    // 9) Log
    log.Println("\n📊 Tabela prób:")
    for i, r := range results {
        log.Printf("%d | sent=%s | lat=%.2fms | status=%s | qty=%.6f | msg=%s",
            i+1, r.Sent, r.Latency, r.Status, r.Qty, r.Msg)
    }

    // 10) Jeśli kupiliśmy – wystaw SELL
    if !success.Load() {
        log.Println("[BOT] no buy")
        return
    }
    sellPrice := math.Round(limitPrice*(1+l.ProfitPct/100)*1e8) / 1e8
    sellQty := math.Round(qty*1e6) / 1e6
    log.Printf("[BOT] SELL %f @ %.8f", sellQty, sellPrice)

    sellParams := map[string]string{
        "symbol":      l.Symbol,
        "side":        "SELL",
        "type":        "LIMIT",
        "price":       fmt.Sprintf("%.8f", sellPrice),
        "quantity":    fmt.Sprintf("%.6f", sellQty),
        "timeInForce": "GTC",
        "recvWindow":  "5000",
        "timestamp":   strconv.FormatInt(time.Now().UnixNano()/1e6+offset, 10),
    }
    sellParams["signature"] = sign(sellParams, l.APISecret)
    start := time.Now()
    httpPost(client, REST_URL+"/api/v3/order", map[string]string{"X-MEXC-APIKEY": l.APIKey}, sellParams)
    log.Printf("[SELL] lat=%.2fms", float64(time.Since(start).Microseconds())/1000.0)
}
