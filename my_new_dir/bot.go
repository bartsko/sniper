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
    "os"
    "sort"
    "strconv"
    "time"
)

const REST_URL = "https://api.mexc.com"

type Listing struct {
    ID             string  `json:"id"`
    APIKey         string  `json:"api_key"`
    APISecret      string  `json:"api_secret"`
    Symbol         string  `json:"symbol"`
    QuoteAmount    float64 `json:"quote_amount"`
    ListingTime    string  `json:"listing_time"`
    PriceMarkupPct float64 `json:"price_markup_pct"`
    ProfitPct      float64 `json:"profit_pct"`
}

func must(err error) {
    if err != nil { log.Fatal(err) }
}

func sign(params map[string]string, secret string) string {
    keys := make([]string,0,len(params))
    for k:=range params { keys = append(keys,k) }
    sort.Strings(keys)
    var buf bytes.Buffer
    for i,k := range keys {
        if i>0 { buf.WriteString("&") }
        buf.WriteString(fmt.Sprintf("%s=%s", k, params[k]))
    }
    mac := hmac.New(sha256.New, []byte(secret))
    mac.Write(buf.Bytes())
    return fmt.Sprintf("%x", mac.Sum(nil))
}

func httpReq(method, url string, headers, qs map[string]string) []byte {
    req, _ := http.NewRequest(method, url, nil)
    q := req.URL.Query()
    for k,v := range qs { q.Set(k,v) }
    req.URL.RawQuery = q.Encode()
    for k,v := range headers { req.Header.Set(k,v) }
    resp, err := http.DefaultClient.Do(req)
    must(err)
    defer resp.Body.Close()
    b, _ := ioutil.ReadAll(resp.Body)
    return b
}

func main() {
    if len(os.Args)<2 {
        log.Fatal("brak listing_id")
    }
    lid := os.Args[1]
    // 1) załaduj current_listing.json
    b,_ := ioutil.ReadFile("current_listing.json")
    var l Listing
    must(json.Unmarshal(b,&l))
    log.Printf("[INFO] %s @ %s, amount=%.4f, markup=%.2f%%, profit=%.2f%%",
        l.Symbol, l.ListingTime, l.QuoteAmount, l.PriceMarkupPct, l.ProfitPct)

    // 2) oblicz T0
    t0, _ := time.Parse(time.RFC3339, l.ListingTime)
    t0ms := t0.UTC().UnixNano()/1e6

    // 3) sync offset
    resp := httpReq("GET", REST_URL+"/api/v3/time", nil, nil)
    var tm struct{ ServerTime int64 `json:"serverTime"` }
    must(json.Unmarshal(resp,&tm))
    offset := tm.ServerTime - time.Now().UnixNano()/1e6
    log.Printf("[SYNC] offset=%dms", offset)

    // 4) dummy
    warmTs := strconv.FormatInt(time.Now().UnixNano()/1e6-offset-100000,10)
    dummy := map[string]string{
        "symbol":l.Symbol,"side":"BUY","type":"MARKET",
        "quoteOrderQty":"0.000001","recvWindow":"2000","timestamp":warmTs,
    }
    dummy["signature"]=sign(dummy,l.APISecret)
    httpReq("POST", REST_URL+"/api/v3/order", map[string]string{"X-MEXC-APIKEY":l.APIKey}, dummy)

    // 5) pobierz book
    dep := httpReq("GET", REST_URL+"/api/v3/depth", nil, map[string]string{"symbol":l.Symbol,"limit":"5"})
    var D struct{ Asks [][]string `json:"asks"` }
    must(json.Unmarshal(dep,&D))
    price,_ := strconv.ParseFloat(D.Asks[0][0],64)
    limitPrice := math.Round(price*(1+l.PriceMarkupPct/100)*1e8)/1e8
    log.Printf("[PREP] market=%.8f → limit=%.8f", price, limitPrice)

    // 6) qty
    qty := math.Round(l.QuoteAmount/limitPrice*1e6)/1e6

    // 7) próby
    offsets := []int64{-10,-5,0}
    var done bool
    type R struct{ Sent string; Lat float64; Status string; Qty float64 }
    var results []R

    for _,off := range offsets {
        target := t0ms + off
        for time.Now().UnixNano()/1e6 < target-offset { /*busy*/ }
        if done { break }
        params := map[string]string{
            "symbol":l.Symbol,"side":"BUY","type":"LIMIT",
            "price":fmt.Sprintf("%.8f",limitPrice),
            "quantity":fmt.Sprintf("%.6f",qty),
            "timeInForce":"IOC","recvWindow":"5000",
            "timestamp":strconv.FormatInt(time.Now().UnixNano()/1e6+offset,10),
        }
        params["signature"]=sign(params,l.APISecret)
        start:=time.Now()
        body:=httpReq("POST",REST_URL+"/api/v3/order",
            map[string]string{"X-MEXC-APIKEY":l.APIKey}, params)
        lat := float64(time.Since(start).Microseconds())/1000
        var d map[string]interface{}
        json.Unmarshal(body,&d)
        status,filled := "ERR",0.0
        if _, ok := d["orderId"]; ok {
            filled = d["executedQty"].(float64)
            if filled>0{ status="OK"; done=true }
        }
        results = append(results,R{Sent:start.Format("15:04:05.000"), Lat:lat, Status:status, Qty:filled})
    }

    // 8) log
    for i,r := range results {
        log.Printf("%d: %s | %.2fms | %s | qty=%.6f",i+1,r.Sent,r.Lat,r.Status,r.Qty)
    }
    if !done { log.Println("[BOT] no buy"); return }

    // 9) SELL
    sellPrice:=math.Round(limitPrice*(1+l.ProfitPct/100)*1e8)/1e8
    sellQty:=qty
    log.Printf("[BOT] SELL %f @ %.8f",sellQty,sellPrice)
    sp:=map[string]string{
        "symbol":l.Symbol,"side":"SELL","type":"LIMIT",
        "price":fmt.Sprintf("%.8f",sellPrice),
        "quantity":fmt.Sprintf("%.6f",sellQty),
        "timeInForce":"GTC","recvWindow":"5000",
        "timestamp":strconv.FormatInt(time.Now().UnixNano()/1e6+offset,10),
    }
    sp["signature"]=sign(sp,l.APISecret)
    httpReq("POST",REST_URL+"/api/v3/order",map[string]string{"X-MEXC-APIKEY":l.APIKey},sp)
    log.Println("[SELL] done")
}
