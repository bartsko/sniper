#!/usr/bin/env python3
import asyncio, time, hmac, hashlib, json
from datetime import datetime, timezone
from pathlib import Path

import httpx

# ──────────────────────────────────────────────────────────────────────────────
#  Load the scheduled listing from JSON
# ──────────────────────────────────────────────────────────────────────────────
CURRENT_FILE = Path("current_listing.json")
if not CURRENT_FILE.exists():
    raise FileNotFoundError(f"{CURRENT_FILE} not found")

with open(CURRENT_FILE, "r", encoding="utf-8") as f:
    listing = json.load(f)

API_KEY          = listing["api_key"]
API_SECRET       = listing["api_secret"]
SYMBOL           = listing["symbol"].upper()
QUOTE_AMOUNT     = float(listing["quote_amount"])
LISTING_TIME     = listing["listing_time"]
PRICE_MARKUP_PCT = float(listing.get("price_markup_pct", 20))
PROFIT_PCT       = float(listing.get("profit_pct", 200))

REST_URL = "https://api.mexc.com"

# ──────────────────────────────────────────────────────────────────────────────
#  Helpers
# ──────────────────────────────────────────────────────────────────────────────
def sign(params: dict, secret: str) -> str:
    qs = "&".join(f"{k}={params[k]}" for k in sorted(params))
    return hmac.new(secret.encode(), qs.encode(), hashlib.sha256).hexdigest()

def log_attempts(attempts):
    print("\n📊 Tabela prób:\n")
    hdr = f"{'Nr':<3} | {'Wysłano':<23} | {'Odebrano':<23} | {'Lat(ms)':<8} | {'Status':<7} | {'Qty':<8} | {'Cena':<10} | Msg"
    print(hdr)
    print("-" * len(hdr))
    for i, a in enumerate(attempts, 1):
        print(f"{i:<3} | {a['sent']:<23} | {a['recv']:<23} | {a['lat']:>7.2f} | "
              f"{a['status']:<7} | {a['exec_qty']:<8.6f} | {a['price']:<10} | {a['msg']}")

async def get_server_offset(client):
    r = await client.get(f"{REST_URL}/api/v3/time")
    return r.json()["serverTime"] - int(time.time() * 1000)

async def warmup(client):
    ts = int(time.time() * 1000) - 100_000
    dummy = {
        "symbol": SYMBOL, "side": "BUY", "type": "MARKET",
        "quoteOrderQty": "0.000001", "recvWindow": "2000",
        "timestamp": str(ts)
    }
    dummy["signature"] = sign(dummy, API_SECRET)
    await client.post(f"{REST_URL}/api/v3/order", params=dummy,
                      headers={"X-MEXC-APIKEY": API_KEY})

async def prepare_limit_price(client):
    r = await client.get(f"{REST_URL}/api/v3/depth",
                         params={"symbol": SYMBOL, "limit": 5})
    asks = r.json().get("asks", [])
    if not asks:
        return None
    market_price = float(asks[0][0])
    limit_price = round(market_price * (1 + PRICE_MARKUP_PCT/100), 8)
    print(f"[PREP] market={market_price:.8f} → limit={limit_price:.8f}")
    return limit_price

# ──────────────────────────────────────────────────────────────────────────────
#  Place BUY via HTTP REST (LIMIT or MARKET)
# ──────────────────────────────────────────────────────────────────────────────
async def place_buy(client, offset, delay_s, qty, limit_price, success_evt):
    # poczekaj asynchronicznie na swoją kolej...
    await asyncio.sleep(delay_s)
    if success_evt.is_set():
        return None

    # zbuduj parametry
    ts = int(time.time()*1000) + offset
    params = {
        "symbol": SYMBOL,
        "side":   "BUY",
        "type":   "LIMIT" if limit_price else "MARKET",
        "recvWindow": "5000",
        "timestamp":   str(ts)
    }
    if limit_price:
        params.update({
            "price":       str(limit_price),
            "quantity":    str(round(qty, 6)),
            "timeInForce": "IOC"
        })
    else:
        params["quoteOrderQty"] = str(QUOTE_AMOUNT)

    params["signature"] = sign(params, API_SECRET)

    sent = datetime.now().strftime("%H:%M:%S.%f")[:-3]
    start = time.perf_counter()
    resp = await client.post(f"{REST_URL}/api/v3/order",
                             params=params,
                             headers={"X-MEXC-APIKEY": API_KEY})
    lat = (time.perf_counter() - start) * 1000
    data = resp.json()
    exec_qty = float(data.get("executedQty", 0))
    status   = "OK" if exec_qty>0 else "NOFILL"
    price    = data.get("price", params.get("price", ""))
    msg      = data.get("msg", "")

    if status=="OK":
        success_evt.set()

    return {
        "sent":     sent,
        "recv":     datetime.now().strftime("%H:%M:%S.%f")[:-3],
        "lat":      lat,
        "status":   status,
        "exec_qty": exec_qty,
        "price":    price,
        "msg":      msg
    }

# ──────────────────────────────────────────────────────────────────────────────
#  Main flow
# ──────────────────────────────────────────────────────────────────────────────
async def main():
    print(f"[INFO] {SYMBOL} @ {LISTING_TIME}, amount={QUOTE_AMOUNT}, markup={PRICE_MARKUP_PCT}%, profit={PROFIT_PCT}%")

    # oblicz T0 w ms
    t0_ms = int(datetime.fromisoformat(LISTING_TIME)
                .astimezone(timezone.utc).timestamp() * 1000)

    async with httpx.AsyncClient(http2=True) as client:
        # 1) do ~5s przed T0
        await asyncio.sleep(max((t0_ms - int(time.time()*1000) - 5000)/1000, 0))

        offset = await get_server_offset(client)
        print(f"[SYNC] offset: {offset} ms → warmup")
        await warmup(client)

        # 2) przygotuj limit_price ~4s przed T0
        await asyncio.sleep(max((t0_ms - int(time.time()*1000) - 4000)/1000, 0))
        limit_price = await prepare_limit_price(client)
        qty = round(QUOTE_AMOUNT/limit_price,6) if limit_price else None

        # 3) schedule 3 asynchroniczne próby
        now_ms = int(time.time()*1000)
        delays = [(t0_ms - delta - now_ms)/1000 for delta in (10,5,0)]
        success = asyncio.Event()
        tasks = [
            asyncio.create_task(place_buy(client, offset, delays[i], qty, limit_price, success))
            for i in range(3)
        ]

        # 4) await i loguj
        results = [r for r in await asyncio.gather(*tasks) if r]
        log_attempts(results)

        # 5) sprawdź czy coś kupiono
        filled = next((r["exec_qty"]*(limit_price or 1) for r in results if r["status"]=="OK"), 0)
        if filled<=0:
            print("[BOT] no buy")
            return

        # 6) SELL LIMIT @TP
        sell_price = round((limit_price or 0)*(1+PROFIT_PCT/100),8)
        sell_qty   = round(filled/(limit_price or 1),6)
        print(f"[BOT] SELL {sell_qty}@{sell_price}")

        ts = str(int(time.time()*1000) + offset)
        sp = {
            "symbol": SYMBOL, "side": "SELL", "type": "LIMIT",
            "price": str(sell_price), "quantity": str(sell_qty),
            "timeInForce": "GTC", "recvWindow": "5000",
            "timestamp": ts
        }
        sp["signature"] = sign(sp, API_SECRET)
        sent = datetime.now().strftime("%H:%M:%S.%f")[:-3]
        start = time.perf_counter()
        await client.post(f"{REST_URL}/api/v3/order", params=sp,
                          headers={"X-MEXC-APIKEY": API_KEY})
        lat = (time.perf_counter()-start)*1000
        print(f"[SELL] qty={sell_qty} price={sell_price} lat={lat:.2f}ms")

if __name__=="__main__":
    asyncio.run(main())
