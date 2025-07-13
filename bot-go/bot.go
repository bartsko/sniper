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
	"strings"
	"sync/atomic"
	"time"
)

const REST_URL = "https://api.mexc.com"

// Listing opisany w current_listing.json
type Listing struct {
	APIKey         string  `json:"api_key"`
	APISecret      string  `json:"api_secret"`
	Symbol         string  `json:"symbol"`
	QuoteAmount    float64 `json:"quote_amount"`
	ListingTime    string  `json:"listing_time"`
	PriceMarkupPct float64 `json:"price_markup_pct"`
	ProfitPct      float64 `json:"profit_pct"`
}

// wynik pojedynczej próby
type attemptResult struct {
	Sent    string
	Recv    string
	Latency float64
	Status  string
	Qty     float64
	Price   float64
	Msg     string
}

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}

// podpis HMAC SHA256 alfabetycznie posortowanych parametrów
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

// busy-wait until epoch-ms reaches target
func busyWait(targetMs int64) {
	for time.Now().UnixNano()/1e6 < targetMs {
	}
}

func main() {
	// 0) Wczytaj listing
	data, err := ioutil.ReadFile("current_listing.json")
	must(err)
	var l Listing
	must(json.Unmarshal(data, &l))
	log.Printf("[INFO] %s @ %s, amount=%.4f, markup=%.2f%%, profit=%.2f%%",
		l.Symbol, l.ListingTime, l.QuoteAmount, l.PriceMarkupPct, l.ProfitPct)

	client := &http.Client{}

	// 1) Oblicz T0 w ms UTC
	t0, err := time.Parse(time.RFC3339, l.ListingTime)
	must(err)
	t0ms := t0.UTC().UnixNano() / 1e6

	// 2) Na ~5s przed T0: synchronizacja czasu + warmup
	offsetData := httpGet(client, REST_URL+"/api/v3/time", nil, nil)
	var srv struct{ ServerTime int64 `json:"serverTime"` }
	must(json.Unmarshal(offsetData, &srv))
	offset := srv.ServerTime - time.Now().UnixNano()/1e6
	log.Printf("[SYNC] offset=%dms", offset)

	warmupTs := strconv.FormatInt(time.Now().UnixNano()/1e6+offset-100000, 10)
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
	log.Println("[WARMUP] done")

	// czekaj aż do ~4s przed
	busyWait(t0ms - 4000 - offset)

	// 3) Pobierz ASK z orderbook, oblicz limit-price lub tryb MARKET
	depthData := httpGet(client, REST_URL+"/api/v3/depth", nil,
		map[string]string{"symbol": l.Symbol, "limit": "5"})
	var depth struct{ Asks [][]string `json:"asks"` }
	must(json.Unmarshal(depthData, &depth))

	mode := "LIMIT"
	var limitPrice float64
	if len(depth.Asks) == 0 {
		mode = "MARKET"
		log.Println("[PREP] orderbook empty → MARKET mode")
	} else {
		price, _ := strconv.ParseFloat(depth.Asks[0][0], 64)
		limitPrice = math.Round(price*(1+l.PriceMarkupPct/100)*1e8) / 1e8
		log.Printf("[PREP] market=%.8f → limit=%.8f", price, limitPrice)
	}

	qty := 0.0
	if mode == "LIMIT" {
		qty = math.Round(l.QuoteAmount/limitPrice*1e6) / 1e6
	}

	// 4) Przygotuj próby: T0-10ms, -5ms, 0ms
	buyOffsets := []int64{-10, -5, 0}
	var success atomic.Bool
	var results []attemptResult

	// 5) Wyślij trzy próby równolegle, ale bez blokowania kolejki
	type job struct{ off int64; idx int }
	jobs := make(chan job, len(buyOffsets))
	for i, off := range buyOffsets {
		jobs <- job{off, i}
	}
	close(jobs)

	const preciseDelayMs = 0 // ustaw np. -1 lub -2 jeśli trafiasz za późno

	// start workerów
	done := make(chan struct{})
	for w := 0; w < len(buyOffsets); w++ {
		go func() {
			for jb := range jobs {
				if success.Load() {
					break
				}
				target := t0ms + jb.off + preciseDelayMs

				// Przygotuj parametry poza busy-wait
				params := map[string]string{
					"symbol":     l.Symbol,
					"side":       "BUY",
					"recvWindow": "5000",
				}
				if mode == "MARKET" {
					params["type"] = "MARKET"
					params["quoteOrderQty"] = fmt.Sprintf("%.6f", l.QuoteAmount)
				} else {
					params["type"] = "LIMIT"
					params["price"] = fmt.Sprintf("%.8f", limitPrice)
					params["quantity"] = fmt.Sprintf("%.6f", qty)
					params["timeInForce"] = "IOC"
				}

				// Busy-wait do momentu target
				busyWait(target - offset)

				// Timestamp i signature generujesz dokładnie w tej ms
				params["timestamp"] = strconv.FormatInt(time.Now().UnixNano()/1e6+offset, 10)
				params["signature"] = sign(params, l.APISecret)

				sent := time.Now()
				body := httpPost(client, REST_URL+"/api/v3/order",
					map[string]string{"X-MEXC-APIKEY": l.APIKey}, params)
				recv := time.Now()
				lat := float64(recv.Sub(sent).Microseconds()) / 1000.0

				var resp map[string]interface{}
				json.Unmarshal(body, &resp)

				stat := "NOFILL"
				execQty := 0.0
				if v, ok := resp["executedQty"].(float64); ok && v > 0 {
					execQty = v
					stat = "OK"
					success.Store(true)
				}
				msg := fmt.Sprint(resp["msg"])

				results = append(results, attemptResult{
					Sent:    sent.Format("15:04:05.000"),
					Recv:    recv.Format("15:04:05.000"),
					Latency: lat,
					Status:  stat,
					Qty:     execQty,
					Price:   limitPrice,
					Msg:     msg,
				})

				log.Printf("[TRY] sent=%s recv=%s lat=%.2fms stat=%s qty=%.6f msg=%s",
					sent.Format("15:04:05.000"), recv.Format("15:04:05.000"),
					lat, stat, execQty, msg)
			}
			done <- struct{}{}
		}()
	}

	// czekaj aż wszyscy pracownicy skończą
	for i := 0; i < len(buyOffsets); i++ {
		<-done
	}

	// 6) Logowanie tabelą
	fmt.Println("\n📊 Tabela prób:")
	fmt.Printf("%-3s | %-12s | %-12s | %-8s | %-6s | %-9s | %-11s | %s\n",
		"Nr", "Wysłano", "Odebrano", "Lat(ms)", "Status", "Qty", "Price", "Msg")
	fmt.Println(strings.Repeat("-", 90))
	for i, a := range results {
		fmt.Printf("%-3d | %-12s | %-12s | %8.2f | %-6s | %9.6f | %11.8f | %s\n",
			i+1, a.Sent, a.Recv, a.Latency, a.Status, a.Qty, a.Price, a.Msg)
	}

	// 7) Jeżeli żaden nie był OK → stagnacja?
	if !success.Load() && mode == "LIMIT" {
		log.Println("[BOT] wszystkie LIMIT próby NOFILL → sprawdzam stagnację 3s")
		first := ""
		stag := true
		for i := 0; i < 6; i++ {
			time.Sleep(500 * time.Millisecond)
			d := httpGet(client, REST_URL+"/api/v3/depth", nil,
				map[string]string{"symbol": l.Symbol, "limit": "5"})
			var dep struct{ Asks [][]string `json:"asks"` }
			json.Unmarshal(d, &dep)
			if len(dep.Asks) > 0 {
				if first == "" {
					first = dep.Asks[0][0]
				} else if dep.Asks[0][0] != first {
					stag = false
					break
				}
			}
		}
		if stag {
			log.Println("[BOT] STAGNACJA → zaplanuj ponowny BUY za 10min")
		} else {
			log.Println("[BOT] cena ruszyła → kończę no buy")
		}
		if stag {
			// tutaj możesz rzucić schedulerem na t0+10m-5s
		}
		return
	}

	if !success.Load() {
		log.Println("[BOT] no buy (MARKET fallback też się nie udał)")
		return
	}

	// 8) SELL TP natychmiast po pierwszym OK
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
	startSell := time.Now()
	httpPost(client, REST_URL+"/api/v3/order",
		map[string]string{"X-MEXC-APIKEY": l.APIKey}, params)
	latSell := float64(time.Since(startSell).Microseconds()) / 1000.0
	log.Printf("[SELL] qty=%.6f price=%.8f lat=%.2fms",
		sellQty, sellPrice, latSell)
}
