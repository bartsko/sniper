package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"math"
	"net/http"
	"sort"
	"strconv"
	"time"
)

const REST_URL = "https://api.mexc.com"

type Listing struct {
	APIKey      string  `json:"api_key"`
	APISecret   string  `json:"api_secret"`
	Symbol      string  `json:"symbol"`
	QuoteAmount float64 `json:"quote_amount"`
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

func httpPost(client *http.Client, url string, headers, qs map[string]string) {
	req, _ := http.NewRequest("POST", url, nil)
	q := req.URL.Query()
	for k, v := range qs {
		q.Set(k, v)
	}
	req.URL.RawQuery = q.Encode()
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	go client.Do(req) // FIRE & FORGET – NIE CZEKAJ NA ODPOWIEDŹ
}

func main() {
	data, err := ioutil.ReadFile("current_listing.json")
	if err != nil {
		panic(err)
	}
	var l Listing
	if err := json.Unmarshal(data, &l); err != nil {
		panic(err)
	}

	client := &http.Client{}

	for i := 0; i < 3; i++ {
		params := map[string]string{
			"symbol":        l.Symbol,
			"side":          "BUY",
			"type":          "MARKET",
			"quoteOrderQty": fmt.Sprintf("%.6f", math.Round(l.QuoteAmount*1e6)/1e6),
			"recvWindow":    "2000",
			"timestamp":     strconv.FormatInt(time.Now().UnixNano()/1e6, 10),
		}
		params["signature"] = sign(params, l.APISecret)

		headers := map[string]string{"X-MEXC-APIKEY": l.APIKey}
		fmt.Printf("[%d] WYSYŁAM ORDER @ %s\n", i+1, time.Now().Format("15:04:05.000"))
		httpPost(client, REST_URL+"/api/v3/order", headers, params)

		if i < 2 {
			time.Sleep(5 * time.Millisecond)
		}
	}
}
