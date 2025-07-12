#!/usr/bin/env python3
# bot.py

import asyncio
import time
import hmac
import hashlib
import json
from datetime import datetime, timezone
import httpx
import websockets
from pathlib import Path

# —————————————————————————————————————————————
# 1) Wczytanie danych listingu z JSON
CURRENT_FILE = Path("current_listing.json")
if not CURRENT_FILE.exists():
    raise FileNotFoundError(f"{CURRENT_FILE} not found")

with open(CURRENT_FILE, "r", encoding="utf-8") as f:
    listing = json.load(f)

print(f"[BOT] Loaded listing: {listing}")

API_KEY          = listing["api_key"]
API_SECRET       = listing["api_secret"]
SYMBOL           = listing["symbol"].upper()
QUOTE_AMOUNT     = float(listing["quote_amount"])
LISTING_TIME     = listing["listing_time"]         # ISO8601 z offsetem
PRICE_MARKUP_PCT = float(listing.get("price_markup_pct", 20))
PROFIT_PCT       = float(listing.get("profit_pct", 200))

REST_URL = "https://api.mexc.com"
WS_URL   = "wss://wbs.mexc.com/ws"

# —————————————————————————————————————————————
def sign(params: dict, secret: str) -> str:
    qs = "&".join(f"{k}={params[k]}" for k in sorted(params))
    return hmac.new(secret.encode(), qs.encode(), hashlib.sha256).hexdigest()

# —————————————————————————————————————————————
def log_attempts(attempts):
    print("\n📊 Tabela prób:\n")
    header = f"{'Nr':<3} | {'Wysłano':<23} | {'Odpowiedź':<23} | {'Lat(ms)':<8} | {'Status':<5} | {'Qty':<8} | {'Msg'}"
    print(header)
    print("-" * len(header))
    for i, a in enumerate(attempts, 1):
        print(f"{i:<3} | {a['sent']:<23} | {a['recv']:<23} | {a['lat']:>7.2f} | {a['status']:<5} | {a['exec_qty']:<8.6f} | {a['msg']}")

# —————————————————————————————————————————————
async def get_server_offset(client):
    r = await client.get(f"{REST_URL}/api/v3/time")
    server_ms = r.json()["serverTime"]
    return server_ms - int(time.time() * 1000)

# —————————————————————————————————————————————
async def warmup(client):
    await client.get(f"{REST_URL}/api/v3/time")
    ts = int(time.time() * 1000) - 100_000
    dummy = {
        "symbol": SYMBOL,
        "side": "BUY",
        "type": "MARKET",
        "quoteOrderQty": 0.000001,
        "recvWindow": 2000,
        "timestamp": ts
    }
    dummy["signature"] = sign(dummy, API_SECRET)
    await client.post(f"{REST_URL}/api/v3/order", params=dummy,
                      headers={"X-MEXC-APIKEY": API_KEY})

# —————————————————————————————————————————————
async def prepare_buy(client):
    r = await client.get(f"{REST_URL}/api/v3/depth", params={"symbol": SYMBOL, "limit": 5})
    asks = r.json().get("asks", [])
    if not asks:
        print("[PREP] Brak asks → tryb MARKET")
        return {"mode": "market"}

    market_price = float(asks[0][0])
    limit_price  = round(market_price * (1 + PRICE_MARKUP_PCT / 100), 8)
    print(f"[PREP] market={market_price} → limit={limit_price}")
    return {
        "mode": "limit",
        "limit_price": limit_price,
        "template_base": {
            "symbol": SYMBOL,
            "side": "BUY",
            "type": "LIMIT",
            "price": str(limit_price),
            "timeInForce": "IOC",
            "recvWindow": 5000
        },
        "url": f"{REST_URL}/api/v3/order",
        "headers": {"X-MEXC-APIKEY": API_KEY}
    }

# —————————————————————————————————————————————
def busy_wait(target_ms: int):
    target_ns = target_ms * 1_000_000
    while time.time_ns() < target_ns:
        pass

# —————————————————————————————————————————————
async def place_buy(client, build, offset, send_at, quantity):
    busy_wait(send_at)
    params = build["template_base"].copy()
    params["quantity"] = str(quantity)
    params["timestamp"] = int(time.time() * 1000) + offset
    params["signature"] = sign(params, API_SECRET)

    sent = datetime.now().strftime("%H:%M:%S.%f")[:-3]
    start = time.perf_counter()
    resp = await client.post(build["url"], params=params, headers=build["headers"])
    lat = (time.perf_counter() - start) * 1000
    data = resp.json()
    status = "OK" if "orderId" in data else "ERR"
    msg = data.get("msg", "")
    exec_qty = float(data.get("executedQty", "0"))
    return {"sent": sent, "recv": datetime.now().strftime("%H:%M:%S.%f")[:-3],
            "lat": lat, "status": status, "msg": msg, "exec_qty": exec_qty}

# —————————————————————————————————————————————
async def place_market(client, offset, send_at, quote_amount):
    busy_wait(send_at)
    ts = int(time.time() * 1000) + offset
    qs = (f"symbol={SYMBOL}&side=BUY&type=MARKET"
          f"&quoteOrderQty={quote_amount}&recvWindow=5000"
          f"&timestamp={ts}")
    sig = hashlib.sha256(f"{qs}".encode()).hexdigest()
    url = f"{REST_URL}/api/v3/order?{qs}&signature={sig}"

    sent = datetime.now().strftime("%H:%M:%S.%f")[:-3]
    start = time.perf_counter()
    resp = await client.post(url, headers={"X-MEXC-APIKEY": API_KEY})
    lat = (time.perf_counter() - start) * 1000
    data = resp.json()
    status = "OK" if "orderId" in data else "ERR"
    msg = data.get("msg", "")
    exec_qty = float(data.get("executedQty", "0"))
    return {"sent": sent, "recv": datetime.now().strftime("%H:%M:%S.%f")[:-3],
            "lat": lat, "status": status, "msg": msg, "exec_qty": exec_qty}

# —————————————————————————————————————————————
async def main():
    print(f"[INFO] {SYMBOL} @ {LISTING_TIME}, amount={QUOTE_AMOUNT}, markup={PRICE_MARKUP_PCT}%, profit={PROFIT_PCT}%")
    async with httpx.AsyncClient(http2=True) as client:
        offset = await get_server_offset(client)
        print(f"[SYNC] offset: {offset} ms")

        dt = datetime.fromisoformat(LISTING_TIME)
        t0_utc = int(dt.astimezone(timezone.utc).timestamp() * 1000)

        now_ms = lambda: int(time.time() * 1000)
        while now_ms() < t0_utc - 10000:
            await asyncio.sleep(0.001)

        build = await prepare_buy(client)
        await warmup(client)

        if build.get("mode") == "limit":
            print("[WS] subskrybuję trade…")
            async with websockets.connect(WS_URL) as ws:
                await ws.send(json.dumps({"method": "SUBSCRIBE",
                                           "params": [f"{SYMBOL.lower()}@trade"], "id": 1}))
                while True:
                    msg = await ws.recv()
                    data = json.loads(msg)
                    if data.get("e") == "trade":
                        t0_local = now_ms()
                        print(f"[WS] T0 @ {t0_local}")
                        break
        else:
            t0_local = t0_utc

        attempts = []
        remaining = QUOTE_AMOUNT
        for send_at in (t0_local - 10, t0_local - 5, t0_local):
            if remaining <= 0:
                break
            if build.get("mode") == "limit":
                qty = round(remaining / build["limit_price"], 6)
                att = await place_buy(client, build, offset, send_at, qty)
            else:
                att = await place_market(client, offset, send_at, remaining)
            attempts.append(att)
            if att["status"] == "OK":
                used = att["exec_qty"] * (build.get("limit_price") or 1)
                remaining -= used
        log_attempts(attempts)

        bought = QUOTE_AMOUNT - remaining
        if bought <= 0:
            print("[BOT] Nie kupiono nic, kończę")
            return

        sell_price = round((build.get("limit_price") or 0) * (1 + PROFIT_PCT / 100), 8)
        sell_qty = bought / (build.get("limit_price") or 1)
        print(f"[BOT] Sprzedaję {sell_qty:.6f}@{sell_price}")

        sell_params = {
            "symbol": SYMBOL,
            "side": "SELL",
            "type": "LIMIT",
            "price": str(sell_price),
            "quantity": str(round(sell_qty, 6)),
            "timeInForce": "GTC",
            "recvWindow": 5000,
            "timestamp": int(time.time() * 1000) + offset
        }
        sell_params["signature"] = sign(sell_params, API_SECRET)

        sent = datetime.now().strftime("%H:%M:%S.%f")[:-3]
        start = time.perf_counter()
        resp = await client.post(f"{REST_URL}/api/v3/order", params=sell_params,
                                 headers={"X-MEXC-APIKEY": API_KEY})
        lat = (time.perf_counter() - start) * 1000
        data = resp.json()
        status = "OK" if "orderId" in data else "ERR"
        msg = data.get("msg", "")
        log_attempts([{"sent": sent, "recv": datetime.now().strftime("%H:%M:%S.%f")[:-3],
                       "lat": lat, "status": status, "exec_qty": sell_qty, "msg": msg}])

if __name__ == "__main__":
    asyncio.run(main())
