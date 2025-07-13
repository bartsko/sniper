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
	"time"
	"strings"
)

const (
	API_KEY    = "TWOJ_API_KEY"
	API_SECRET = "TWOJ_API_SECRET"
	SYMBOL     = "SRXUSDT"
	QUOTE_AMT  = 2.0
	LIST_T0    = "2025-07-13T15:30:00Z" // <-- ustaw swój czas w formacie RFC3339
	REST_URL   = "https://api.mexc.com"
)

// Tabela wyników
type Result struct {
	Sent     string
	Received string
	Latency  float64
	Status   string
	Qty      string
	Msg      string
}

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

func getServerTime(client *http.Client) int64 {
	req, _ := http.NewRequest("GET", REST_URL+"/api/v3/time", nil)
	resp, err := client.Do(req)
	if err != nil {
		log.Fatalf("Nie mogę pobrać czasu z MEXC: %v", err)
	}
	defer resp.Body.Close()
	body, _ := ioutil.ReadAll(resp.Body)
	var res struct{ ServerTime int64 `json:"serverTime"` }
	json.Unmarshal(body, &res)
	return res.ServerTime
}

func main() {
	t0, err := time.Parse(time.RFC3339, LIST_T0)
	if err != nil {
		log.Fatal(err)
	}
	t0ms := t0.UTC().UnixNano() / 1e6

	tr := &http.Transport{
		MaxIdleConns:        2,
		MaxIdleConnsPerHost: 2,
		IdleConnTimeout:     90 * time.Second,
		DisableKeepAlives:   false,
	}
	client := &http.Client{Transport: tr}

	// 1. Synchronizacja czasu
	localNow := time.Now().UnixNano() / 1e6
	mexcNow := getServerTime(client)
	offset := mexcNow - localNow
	log.Printf("[SYNC] Offset czasu VPS vs MEXC: %d ms", offset)

	// 2. Rozgrzewka połączenia (GET + micro order)
	req, _ := http.NewRequest("GET", REST_URL+"/api/v3/time", nil)
	_, _ = client.Do(req)

	paramsWarm := map[string]string{
		"symbol":        SYMBOL,
		"side":          "BUY",
		"type":          "MARKET",
		"quoteOrderQty": "0.000001",
		"recvWindow":    "2000",
		"timestamp":     strconv.FormatInt(time.Now().UnixNano()/1e6+offset, 10),
	}
	paramsWarm["signature"] = sign(paramsWarm, API_SECRET)
	reqWarm, _ := http.NewRequest("POST", REST_URL+"/api/v3/order", nil)
	q := reqWarm.URL.Query()
	for k, v := range paramsWarm {
		q.Set(k, v)
	}
	reqWarm.URL.RawQuery = q.Encode()
	reqWarm.Header.Set("X-MEXC-APIKEY", API_KEY)
	_, _ = client.Do(reqWarm)

	log.Println("Połączenie rozgrzane – czekam na T0 (zsynchronizowane z serwerem giełdy)...")

	// 3. Busy wait do T0, skorygowane o offset
	for {
		now := time.Now().UnixNano()/1e6 + offset
		if now >= t0ms {
			break
		}
		time.Sleep(1 * time.Millisecond)
	}

	// 4. Zlecenie MARKET (synchronizowany timestamp!)
	params := map[string]string{
		"symbol":        SYMBOL,
		"side":          "BUY",
		"type":          "MARKET",
		"quoteOrderQty": fmt.Sprintf("%.6f", QUOTE_AMT),
		"recvWindow":    "5000",
		"timestamp":     strconv.FormatInt(time.Now().UnixNano()/1e6+offset, 10),
	}
	params["signature"] = sign(params, API_SECRET)

	reqMain, _ := http.NewRequest("POST", REST_URL+"/api/v3/order", nil)
	qMain := reqMain.URL.Query()
	for k, v := range params {
		qMain.Set(k, v)
	}
	reqMain.URL.RawQuery = qMain.Encode()
	reqMain.Header.Set("X-MEXC-APIKEY", API_KEY)

	sent := time.Now()
	resp, err := client.Do(reqMain)
	recv := time.Now()
	if err != nil {
		log.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := ioutil.ReadAll(resp.Body)

	lat := float64(recv.Sub(sent).Microseconds()) / 1000.0

	// Parsowanie odpowiedzi
	status := "ERROR"
	qty := ""
	msg := ""
	var out map[string]interface{}
	if json.Unmarshal(body, &out) == nil {
		if out["executedQty"] != nil {
			qty = fmt.Sprintf("%v", out["executedQty"])
			if qty != "0" && qty != "" {
				status = "OK"
			} else {
				status = "NOFILL"
			}
		}
		if out["msg"] != nil {
			msg = fmt.Sprintf("%v", out["msg"])
		}
		if out["code"] != nil && msg == "" {
			msg = fmt.Sprintf("%v", out["code"])
		}
	}

	// --- TABELA ---
	fmt.Println("\nTabela prób:")
	fmt.Printf("%-3s | %-12s | %-12s | %-8s | %-8s | %-8s | %s\n", "Nr", "Wysłano", "Odebrano", "Lat(ms)", "Status", "Qty", "Msg")
	fmt.Println(strings.Repeat("-", 80))
	fmt.Printf("%-3d | %-12s | %-12s | %8.2f | %-8s | %-8s | %s\n",
		1, sent.Format("15:04:05.000"), recv.Format("15:04:05.000"), lat, status, qty, msg)
}
