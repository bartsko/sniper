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

type attemptResult struct {
  Sent      string
  Recv      string
  Latency   float64
  Status    string
  Qty       float64
  Price     float64
  Msg       string
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

  // 2) Oblicz T0 w ms
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
  marketPrice, _ := strconv.ParseFloat(depth.Asks[0][0], 64)
  limitPrice := math.Round(marketPrice*(1+l.PriceMarkupPct/100)*1e8) / 1e8
  log.Printf("[PREP] market=%.8f → limit=%.8f", marketPrice, limitPrice)

  qty := math.Round(l.QuoteAmount/limitPrice*1e6) / 1e6

  // 6) Wyślij 3 próby i zbierz wyniki
  buyOffsets := []int64{-10, -5, 0}
  var success atomic.Bool
  var results []attemptResult

  for i, off := range buyOffsets {
    target := t0ms + off
    // busy-wait
    for time.Now().UnixNano()/1e6 < target-offset {
    }
    if success.Load() {
      break
    }

    // zbuduj params
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

    sentTime := time.Now()
    body, _ := httpPost(client, REST_URL+"/api/v3/order",
      map[string]string{"X-MEXC-APIKEY": l.APIKey}, params)
    recvTime := time.Now()
    latency := float64(recvTime.Sub(sentTime).Microseconds()) / 1000.0

    var resp map[string]interface{}
    json.Unmarshal(body, &resp)

    status := "ERR"
    execQty := 0.0
    if v, ok := resp["executedQty"].(float64); ok {
      execQty = v
      if execQty > 0 {
        status = "OK"
        success.Store(true)
      } else {
        status = "NOFILL"
      }
    }
    msg := ""
    if m, ok := resp["msg"].(string); ok {
      msg = m
    }

    results = append(results, attemptResult{
      Sent:    sentTime.Format("15:04:05.000"),
      Recv:    recvTime.Format("15:04:05.000"),
      Latency: latency,
      Status:  status,
      Qty:     execQty,
      Price:   limitPrice,
      Msg:     msg,
    })

    log.Printf("[TRY %d] sent=%s recv=%s lat=%.2fms status=%s qty=%.6f price=%.8f msg=%s",
      i+1, results[i].Sent, results[i].Recv, results[i].Latency,
      results[i].Status, results[i].Qty, results[i].Price, results[i].Msg)
  }

  // 7) Wyświetl podsumowanie tabelą
  fmt.Println("\n📊 Tabela prób:")
  fmt.Printf("%-3s | %-12s | %-12s | %-8s | %-6s | %-9s | %-11s | %s\n",
    "Nr", "Wysłano", "Odebrano", "Lat(ms)", "Status", "Qty", "Price", "Msg")
  fmt.Println(strings.Repeat("-", 80))
  for i, a := range results {
    fmt.Printf("%-3d | %-12s | %-12s | %8.2f | %-6s | %9.6f | %11.8f | %s\n",
      i+1, a.Sent, a.Recv, a.Latency, a.Status, a.Qty, a.Price, a.Msg)
  }

  if !success.Load() {
    log.Println("[BOT] no buy")
    return
  }

  // 8) SELL
  sellPrice := math.Round(limitPrice*(1+l.ProfitPct/100)*1e8) / 1e8
  sellQty := math.Round(qty*1e6) / 1e6

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
  startSell := time.Now()
  httpPost(client, REST_URL+"/api/v3/order",
    map[string]string{"X-MEXC-APIKEY": l.APIKey}, sellParams)
  sellLat := float64(time.Since(startSell).Microseconds()) / 1000.0

  log.Printf("[SELL] qty=%.6f price=%.8f lat=%.2fms",
    sellQty, sellPrice, sellLat)
}
