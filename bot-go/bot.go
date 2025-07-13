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

// sign tworzy HMAC SHA256 nad querystringiem posortowanych parametrów
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

func httpGet(client *http.Client, url string, headers, qs map[string]string) []byte {
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
    must(err)
    defer resp.Body.Close()
    data, err := ioutil.ReadAll(resp.Body)
    must(err)
    return data
}

func httpPost(client *http.Client, url string, headers, qs map[string]string) []byte {
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
    must(err)
    defer resp.Body.Close()
    body, err := ioutil.ReadAll(resp.Body)
    must(err)
    return body
}

func busyWait(targetMs int64) {
    for time.Now().UnixNano()/1e6 < targetMs {
    }
}

func logTable(attempts []map[string]interface{}) {
    fmt.Println("\n📊 Wyniki prób:")
    fmt.Printf("%-3s | %-12s | %-12s | %-7s | %-5s | %-8s | %s\n",
        "Nr", "Sent", "Recv", "Lat(ms)", "Stat", "Qty", "Price/msg")
    fmt.Println(strings.Repeat("-", 80))
    for i, a := range attempts {
        fmt.Printf("%-3d | %-12s | %-12s | %7.2f | %-5s | %8.6f | %v\n",
            i+1,
            a["sent"], a["recv"], a["lat"].(float64),
            a["status"], a["exec_qty"].(float64), a["info"],
        )
    }
}

func main() {
    // 1) Load listing
    data, err := ioutil.ReadFile("current_listing.json")
    must(err)
    var l Listing
    must(json.Unmarshal(data, &l))
    log.Printf("[INFO] %s @ %s, amount=%.4f, markup=%.2f%%, profit=%.2f%%",
        l.Symbol, l.ListingTime, l.QuoteAmount, l.PriceMarkupPct, l.ProfitPct)

    client := &http.Client{}

    // 2) Compute T0 (epoch-ms)
    t0, err := time.Parse(time.RFC3339, l.ListingTime)
    must(err)
    t0ms := t0.UTC().UnixNano() / 1e6

    // 3) –5s: sync + warmup
    warmupTime := time.Now().UnixNano()/1e6 - 100_000
    serverTimeData := httpGet(client, REST_URL+"/api/v3/time", nil, nil)
    var srv struct{ ServerTime int64 `json:"serverTime"` }
    must(json.Unmarshal(serverTimeData, &srv))
    offset := srv.ServerTime - time.Now().UnixNano()/1e6
    log.Printf("[SYNC] offset=%dms  → warmup", offset)
    warmupParams := map[string]string{
        "symbol":        l.Symbol,
        "side":          "BUY",
        "type":          "MARKET",
        "quoteOrderQty": "0.000001",
        "recvWindow":    "2000",
        "timestamp":     strconv.FormatInt(warmupTime+offset, 10),
    }
    warmupParams["signature"] = sign(warmupParams, l.APISecret)
    httpPost(client, REST_URL+"/api/v3/order", map[string]string{"X-MEXC-APIKEY": l.APIKey}, warmupParams)

    // 4) –4s: pobierz orderbook
    busyWait(t0ms - 4000 - offset)
    depthData := httpGet(client, REST_URL+"/api/v3/depth", nil, map[string]string{"symbol": l.Symbol, "limit": "5"})
    var depth struct{ Asks [][]string `json:"asks"` }
    must(json.Unmarshal(depthData, &depth))

    // oblicz ceny
    var mode string
    var limitPrice float64
    if len(depth.Asks) == 0 {
        log.Println("[BOT] orderbook pusty → MARKET mode")
        mode = "MARKET"
    } else {
        price, _ := strconv.ParseFloat(depth.Asks[0][0], 64)
        limitPrice = math.Round(price*(1+l.PriceMarkupPct/100)*1e8) / 1e8
        log.Printf("[PREP] limitPrice=%.8f", limitPrice)
        mode = "LIMIT"
    }

    qty := 0.0
    if mode == "LIMIT" {
        qty = math.Round(l.QuoteAmount/limitPrice*1e6) / 1e6
    }

    // 5) Trzy próby: –10ms, –5ms, 0ms
    offsets := []int64{-10, -5, 0}
    var success atomic.Bool
    attempts := make([]map[string]interface{}, 0, 3)

    for _, off := range offsets {
        if success.Load() {
            break
        }
        target := t0ms + off
        busyWait(target - offset)

        // zbuduj params
        params := map[string]string{"symbol": l.Symbol, "side": "BUY", "recvWindow": "5000", "timestamp": strconv.FormatInt(time.Now().UnixNano()/1e6+offset, 10)}
        if mode == "MARKET" {
            params["type"] = "MARKET"
            params["quoteOrderQty"] = fmt.Sprintf("%.6f", l.QuoteAmount)
        } else {
            params["type"] = "LIMIT"
            params["price"] = fmt.Sprintf("%.8f", limitPrice)
            params["quantity"] = fmt.Sprintf("%.6f", qty)
            params["timeInForce"] = "IOC"
        }
        params["signature"] = sign(params, l.APISecret)

        sent := time.Now().Format("15:04:05.000")
        start := time.Now()
        body := httpPost(client, REST_URL+"/api/v3/order", map[string]string{"X-MEXC-APIKEY": l.APIKey}, params)
        lat := float64(time.Since(start).Microseconds()) / 1000.0
        nowTs := time.Now().Format("15:04:05.000")

        // analiza odpowiedzi
        var resp map[string]interface{}
        _ = json.Unmarshal(body, &resp)
        execQty := 0.0
        if v, ok := resp["executedQty"].(float64); ok {
            execQty = v
        }
        status := "NOFILL"
        if resp["orderId"] != nil && execQty > 0 {
            status = "OK"
            success.Store(true)
        }
        info := resp["price"]
        if info == nil {
            info = resp["msg"]
        }

        attempts = append(attempts, map[string]interface{}{
            "sent":     sent,
            "recv":     nowTs,
            "lat":      lat,
            "status":   status,
            "exec_qty": execQty,
            "info":     info,
        })
    }

    // 6) Logowanie prób
    logTable(attempts)

    if success.Load() {
        // 7) SELL TP
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
        log.Printf("[SELL] qty=%.6f price=%.8f lat=%.2fms",
            sellQty, sellPrice, float64(time.Since(start).Microseconds())/1000.0)
        return
    }

    // 8) Jeśli LIMIT i NOFILL w 3 próbach → stagnacja?
    if mode == "LIMIT" {
        log.Println("[BOT] wszystkie LIMIT próby NOFILL → sprawdzam stagnację przez 3s")
        ask0 := ""
        stagnant := true
        for i := 0; i < 6; i++ { // co 500ms przez 3s
            time.Sleep(500 * time.Millisecond)
            d := httpGet(client, REST_URL+"/api/v3/depth", nil, map[string]string{"symbol": l.Symbol, "limit": "5"})
            var dep struct{ Asks [][]string `json:"asks"` }
            must(json.Unmarshal(d, &dep))
            if len(dep.Asks) > 0 {
                if ask0 == "" {
                    ask0 = dep.Asks[0][0]
                } else if dep.Asks[0][0] != ask0 {
                    stagnant = false
                    break
                }
            }
        }
        if stagnant {
            log.Println("[BOT] STAGNACJA wykryta → zaplanuj ponowny BUY za 10min od T0")
        } else {
            log.Println("[BOT] cena ruszyła → kończę z no buy")
        }
    } else {
        log.Println("[BOT] no buy (MARKET fallback też się nie udał)")
    }
}
