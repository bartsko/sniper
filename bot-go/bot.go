#!/usr/bin/env python3
import asyncio
import time
import hmac
import hashlib
import json
from datetime import datetime, timezone, timedelta
from pathlib import Path

import httpx

# —————————————————————————————————————————————————————————————————————
#  0) Wczytanie bieżącego listing
# —————————————————————————————————————————————————————————————————————
CURRENT_FILE = Path("current_listing.json")
if not CURRENT_FILE.exists():
    raise FileNotFoundError("current_listing.json not found")
listing = json.loads(CURRENT_FILE.read_text())
API_KEY       = listing["api_key"]
API_SECRET    = listing["api_secret"]
SYMBOL        = listing["symbol"].upper()
QUOTE_AMOUNT  = float(listing["quote_amount"])
LISTING_TIME  = listing["listing_time"]
PRICE_MARKUP  = float(listing.get("price_markup_pct", 20))
PROFIT_PCT    = float(listing.get("profit_pct", 200))
REST_URL      = "https://api.mexc.com"

# —————————————————————————————————————————————————————————————————————
#  Pomocnicze
# —————————————————————————————————————————————————————————————————————
def sign(params: dict) -> str:
    """HMAC SHA256 z posortowanym querystringiem."""
    qs = "&".join(f"{k}={params[k]}" for k in sorted(params))
    return hmac.new(API_SECRET.encode(), qs.encode(), hashlib.sha256).hexdigest()

def log_attempts(atts):
    print("\n📊 Tabela prób:")
    hdr = f"{'Nr':<3} | {'Sent':<12} | {'Recv':<12} | {'Lat(ms)':>8} | {'Stat':<6} | {'Qty':>8} | {'Price':>10} | Msg"
    print(hdr)
    print("-" * len(hdr))
    for i,a in enumerate(atts,1):
        print(
            f"{i:<3} | {a['sent']:<12} | {a['recv']:<12} | {a['lat']:>8.2f} | "
            f"{a['status']:<6} | {a['exec_qty']:>8.6f} | {a['price']:>10} | {a['msg']}"
        )

async def place_buy(client, build, offset, send_at, qty, evt):
    # busy-wait
    while int(time.time()*1000) < send_at:
        await asyncio.sleep(0)
    if evt.is_set():
        return None

    params = build["template"].copy()
    params["quantity"]  = f"{qty:.6f}"
    params["timestamp"] = str(int(time.time()*1000) + offset)
    # zachowaj kolejność
    ordered = {k: params[k] for k in ["symbol","side","type","price","quantity","timeInForce","recvWindow","timestamp"]}
    ordered["signature"] = sign(ordered)

    sent  = datetime.now().strftime("%H:%M:%S.%f")[:-3]
    start = time.perf_counter()
    resp  = await client.post(build["url"], params=ordered, headers=build["headers"])
    lat   = (time.perf_counter()-start)*1000
    data  = resp.json()

    status   = "ERR"
    exec_qty = float(data.get("executedQty", 0))
    msg      = data.get("msg","")
    price    = ordered["price"]

    if "orderId" in data:
        status = "OK" if exec_qty>0 else "NOFILL"
    if status=="OK":
        evt.set()

    return {
        "sent":     sent,
        "recv":     datetime.now().strftime("%H:%M:%S.%f")[:-3],
        "lat":      lat,
        "status":   status,
        "exec_qty": exec_qty,
        "price":    price,
        "msg":      msg
    }

async def place_market(client, offset, send_at, amount, evt):
    while int(time.time()*1000) < send_at:
        await asyncio.sleep(0)
    if evt.is_set():
        return None

    ts = str(int(time.time()*1000) + offset)
    params = {
        "symbol":        SYMBOL,
        "side":          "BUY",
        "type":          "MARKET",
        "quoteOrderQty": f"{amount}",
        "recvWindow":    "5000",
        "timestamp":     ts
    }
    params["signature"] = sign(params)

    sent  = datetime.now().strftime("%H:%M:%S.%f")[:-3]
    start = time.perf_counter()
    resp  = await client.post(f"{REST_URL}/api/v3/order", params=params, headers={"X-MEXC-APIKEY":API_KEY})
    lat   = (time.perf_counter()-start)*1000
    data  = resp.json()

    status   = "ERR"
    exec_qty = float(data.get("executedQty", 0))
    msg      = data.get("msg","")
    price    = ""

    if "orderId" in data:
        status = "OK" if exec_qty>0 else "NOFILL"
    if status=="OK":
        evt.set()

    return {
        "sent":     sent,
        "recv":     datetime.now().strftime("%H:%M:%S.%f")[:-3],
        "lat":      lat,
        "status":   status,
        "exec_qty": exec_qty,
        "price":    price,
        "msg":      msg
    }

# —————————————————————————————————————————————————————————————————————
#  Główna logika
# —————————————————————————————————————————————————————————————————————
async def main():
    print(f"[INFO] {SYMBOL} @ {LISTING_TIME}, amount={QUOTE_AMOUNT}, markup={PRICE_MARKUP}%, profit={PROFIT_PCT}%")

    # 1) oblicz T0 w ms UTC
    t0ms = int(datetime.fromisoformat(LISTING_TIME).astimezone(timezone.utc).timestamp()*1000)

    # 2) przygotuj klienta i rozgrzewka TCP+krypto
    client = httpx.AsyncClient(http2=True)
    await client.get(f"{REST_URL}/api/v3/time")
    await client.get(f"{REST_URL}/api/v3/depth", params={"symbol":SYMBOL,"limit":1})
    print("[WARMUP] connection ready")

    # 3) uśpij aż do ~5 s przed T0
    now = lambda: int(time.time()*1000)
    wait = (t0ms - 5000 - now())/1000
    if wait>0: await asyncio.sleep(wait)

    # 4) synchronizuj offset
    server = (await client.get(f"{REST_URL}/api/v3/time")).json()["serverTime"]
    offset = server - now()
    print(f"[SYNC] offset={offset}ms")

    # 5) ~4 s przed T0 pobierz orderbook i oblicz LIMIT
    await asyncio.sleep(max((t0ms - now() - 4000)/1000, 0))
    asks = (await client.get(f"{REST_URL}/api/v3/depth",
                params={"symbol":SYMBOL,"limit":5})).json().get("asks",[])
    if asks:
        market_price = float(asks[0][0])
        limit_price  = round(market_price*(1+PRICE_MARKUP/100),8)
        mode = "LIMIT"
        qty  = round(QUOTE_AMOUNT/limit_price,6)
        print(f"[PREP] LIMIT → {limit_price}")
    else:
        mode = "MARKET"
        limit_price = None
        qty = None
        print("[PREP] MARKET fallback (orderbook empty)")

    # 6) trzy próby: T0–10ms, –5ms, 0ms
    buy_times = [t0ms-10, t0ms-5, t0ms]
    evt = asyncio.Event()
    tasks = []
    if mode=="LIMIT":
        build = {
            "template": {
                "symbol": SYMBOL,
                "side": "BUY",
                "type": "LIMIT",
                "price": f"{limit_price}",
                "timeInForce": "IOC",
                "recvWindow": "5000"
            },
            "url": f"{REST_URL}/api/v3/order",
            "headers": {"X-MEXC-APIKEY":API_KEY}
        }
        for t in buy_times:
            tasks.append(place_buy(client, build, offset, t, qty, evt))
    else:
        for t in buy_times:
            tasks.append(place_market(client, offset, t, QUOTE_AMOUNT, evt))

    # 7) wyślij równolegle
    results = [r for r in await asyncio.gather(*tasks) if r]
    log_attempts(results)

    # 8) jeśli cokolwiek poszło OK → SELL TP
    ok = next((r for r in results if r["status"]=="OK" and r["exec_qty"]>0), None)
    if ok:
        bought = ok["exec_qty"] * (limit_price if mode=="LIMIT" else 1)
        sell_price = round((limit_price if mode=="LIMIT" else bought)*(1+PROFIT_PCT/100),8)
        sell_qty   = ok["exec_qty"] if mode=="MARKET" else round(bought/limit_price,6)
        print(f"[BOT] SELL {sell_qty}@{sell_price}")
        # TP
        sp = {
            "symbol": SYMBOL, "side":"SELL","type":"LIMIT",
            "price":f"{sell_price}","quantity":f"{sell_qty}",
            "timeInForce":"GTC","recvWindow":"5000",
            "timestamp":str(int(time.time()*1000)+offset)
        }
        sp["signature"] = sign(sp)
        start = time.perf_counter()
        await client.post(f"{REST_URL}/api/v3/order", params=sp, headers={"X-MEXC-APIKEY":API_KEY})
        lat = (time.perf_counter()-start)*1000
        print(f"[SELL] qty={sell_qty} price={sell_price} lat={lat:.2f}ms")
        await client.aclose()
        return

    # 9) brak zakupu LIMIT → detekcja stagnacji
    if mode=="LIMIT":
        print("[BOT] wszystkie LIMIT próby NOFILL → sprawdzam stagnację przez 3s")
        dep0 = (await client.get(f"{REST_URL}/api/v3/depth",
                    params={"symbol":SYMBOL,"limit":1})).json().get("asks",[])
        prev = dep0[0][0] if dep0 else None
        stagnant = True
        for _ in range(6):
            await asyncio.sleep(0.5)
            dep = (await client.get(f"{REST_URL}/api/v3/depth",
                        params={"symbol":SYMBOL,"limit":1})).json().get("asks",[])
            cur = dep[0][0] if dep else None
            if cur != prev:
                stagnant = False
                print(f"[BOT] cena zmienia się {prev}→{cur}, kończę z no buy")
                break
        if stagnant:
            retry_time = datetime.fromtimestamp(t0ms/1000, tz=timezone.utc)+timedelta(minutes=10) - timedelta(seconds=5)
            print(f"[BOT] STAGNACJA! zaplanuj ponownie BUY na {retry_time.isoformat()}")
        else:
            print("[BOT] koniec z no buy")
    else:
        print("[BOT] no buy (MARKET fallback też nieudany)")

    await client.aclose()

if __name__=="__main__":
    asyncio.run(main())
