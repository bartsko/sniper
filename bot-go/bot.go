package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mexcdevelop/mexc-api-sdk-go/spot"
)

const (
	REST_URL = "https://api.mexc.com"
)

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

// Busy-wait until epoch-ms reaches target
func busyWait(targetMs int64) {
	sleepMs := targetMs - time.Now().UnixNano()/1e6 - 2
	if sleepMs > 0 {
		time.Sleep(time.Duration(sleepMs) * time.Millisecond)
	}
	for time.Now().UnixNano()/1e6 < targetMs {
	}
}

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}

func main() {
	// 0) Wczytaj listing
	data, err := os.ReadFile("current_listing.json")
	must(err)
	var l Listing
	must(json.Unmarshal(data, &l))
	log.Printf("[INFO] %s @ %s, amount=%.4f, markup=%.2f%%, profit=%.2f%%",
		l.Symbol, l.ListingTime, l.QuoteAmount, l.PriceMarkupPct, l.ProfitPct)

	// 1) Oblicz T0 w ms UTC
	t0, err := time.Parse(time.RFC3339, l.ListingTime)
	must(err)
	t0ms := t0.UTC().UnixNano() / 1e6

	// 2) Utwórz klienta SDK
	client := spot.NewClient(l.APIKey, l.APISecret)

	// 3) Synchronizacja czasu
	serverTime, err := client.Common.ServerTime(context.Background())
	must(err)
	offset := serverTime - time.Now().UTC().UnixMilli()
	log.Printf("[SYNC] offset=%dms", offset)

	// 4) Rozgrzewka połączenia (pobierz czas, micro order)
	_, _ = client.Common.ServerTime(context.Background())
	warmupParams := spot.PlaceOrderParams{
		Symbol:        l.Symbol,
		Side:          "BUY",
		Type:          "MARKET",
		QuoteOrderQty: "0.000001",
		RecvWindow:    2000,
	}
	_, _ = client.Order.PlaceOrder(context.Background(), warmupParams)
	log.Println("[WARMUP] done (SDK keep-alive)")

	// czekaj aż do ~4s przed
	busyWait(t0ms - 4000 - offset)

	// 5) Pobierz ASK z orderbook przez SDK, oblicz limit-price lub tryb MARKET
	depth, err := client.Market.Depth(context.Background(), l.Symbol, 5)
	must(err)
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

	// 6) Przygotuj próby: T0-10ms, -5ms, 0ms (SDK PlaceOrder)
	buyOffsets := []int64{-10, -5, 0}
	var success atomic.Bool
	var results []attemptResult
	var resultsMu sync.Mutex

	const preciseDelayMs = 0 // ustaw np. -2 jeśli trafiasz za późno

	done := make(chan struct{}, len(buyOffsets))
	for i, off := range buyOffsets {
		go func(idx int, offsetMs int64) {
			var params spot.PlaceOrderParams
			if mode == "MARKET" {
				params = spot.PlaceOrderParams{
					Symbol:        l.Symbol,
					Side:          "BUY",
					Type:          "MARKET",
					QuoteOrderQty: fmt.Sprintf("%.6f", l.QuoteAmount),
					RecvWindow:    5000,
				}
			} else {
				params = spot.PlaceOrderParams{
					Symbol:      l.Symbol,
					Side:        "BUY",
					Type:        "LIMIT",
					Price:       fmt.Sprintf("%.8f", limitPrice),
					Quantity:    fmt.Sprintf("%.6f", qty),
					TimeInForce: "IOC",
					RecvWindow:  5000,
				}
			}
			target := t0ms + offsetMs + preciseDelayMs
			busyWait(target - offset)
			sent := time.Now()
			orderResp, err := client.Order.PlaceOrder(context.Background(), params)
			recv := time.Now()
			lat := float64(recv.Sub(sent).Microseconds()) / 1000.0

			stat := "NOFILL"
			execQty := 0.0
			if err == nil && orderResp.ExecutedQty != "" {
				v, _ := strconv.ParseFloat(orderResp.ExecutedQty, 64)
				if v > 0 {
					execQty = v
					stat = "OK"
					success.Store(true)
				}
			}
			msg := ""
			if err != nil {
				msg = err.Error()
			} else if orderResp.Msg != "" {
				msg = orderResp.Msg
			}
			resultsMu.Lock()
			results = append(results, attemptResult{
				Sent:    sent.Format("15:04:05.000"),
				Recv:    recv.Format("15:04:05.000"),
				Latency: lat,
				Status:  stat,
				Qty:     execQty,
				Price:   limitPrice,
				Msg:     msg,
			})
			resultsMu.Unlock()
			log.Printf("[TRY] sent=%s recv=%s lat=%.2fms stat=%s qty=%.6f msg=%s",
				sent.Format("15:04:05.000"), recv.Format("15:04:05.000"),
				lat, stat, execQty, msg)
			done <- struct{}{}
		}(i, off)
	}

	for i := 0; i < len(buyOffsets); i++ {
		<-done
	}

	// 7) Logowanie tabelą
	fmt.Println("\n📊 Tabela prób:")
	fmt.Printf("%-3s | %-12s | %-12s | %-8s | %-6s | %-9s | %-11s | %s\n",
		"Nr", "Wysłano", "Odebrano", "Lat(ms)", "Status", "Qty", "Price", "Msg")
	fmt.Println(strings.Repeat("-", 90))
	for i, a := range results {
		fmt.Printf("%-3d | %-12s | %-12s | %8.2f | %-6s | %9.6f | %11.8f | %s\n",
			i+1, a.Sent, a.Recv, a.Latency, a.Status, a.Qty, a.Price, a.Msg)
	}

	// 8) Jeżeli żaden nie był OK → stagnacja?
	if !success.Load() && mode == "LIMIT" {
		log.Println("[BOT] wszystkie LIMIT próby NOFILL → sprawdzam stagnację 3s")
		first := ""
		stag := true
		for i := 0; i < 6; i++ {
			time.Sleep(500 * time.Millisecond)
			depth, _ := client.Market.Depth(context.Background(), l.Symbol, 5)
			if len(depth.Asks) > 0 {
				if first == "" {
					first = depth.Asks[0][0]
				} else if depth.Asks[0][0] != first {
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

	// 9) SELL TP natychmiast po pierwszym OK
	sellPrice := math.Round(limitPrice*(1+l.ProfitPct/100)*1e8) / 1e8
	sellQty := math.Round(qty*1e6) / 1e6
	params := spot.PlaceOrderParams{
		Symbol:      l.Symbol,
		Side:        "SELL",
		Type:        "LIMIT",
		Price:       fmt.Sprintf("%.8f", sellPrice),
		Quantity:    fmt.Sprintf("%.6f", sellQty),
		TimeInForce: "GTC",
		RecvWindow:  5000,
	}
	startSell := time.Now()
	_, _ = client.Order.PlaceOrder(context.Background(), params)
	latSell := float64(time.Since(startSell).Microseconds()) / 1000.0
	log.Printf("[SELL] qty=%.6f price=%.8f lat=%.2fms",
		sellQty, sellPrice, latSell)
}
