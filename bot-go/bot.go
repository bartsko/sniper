#!/usr/bin/env python3
import asyncio
import time
import hmac
import hashlib
import json
from datetime import datetime, timezone
from pathlib import Path

import httpx

# ──────────────────────────────────────────────────────────────────────────────
#  Wczytaj listing
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
    qs = "&".join(f"{k}={params[k]}" for k in params)
    return hmac.new(secret.encode(), qs.encode(), hashlib.sha256).hexdigest()

def busy_wait_until(target_ms: int):
    while int(time.time() * 1000) < target_ms:
        time.sleep(0.0005)

def log_attempts(attempts):
    print("\n📊 Tabela prób:\n")
    hdr = (
        f"{'Nr':<3} | {'Wysłano':<23} | {'Odpowiedź':<23} | "
        f"{'Lat(ms)':<8} | {'Status':<6} | {'Qty':<8} | {'Cena':<10} | Msg"
    )
    print(hdr)
    print("-" * len(hdr))
    for i, a in enumerate(attempts, 1):
        print(
            f"{i:<3} | {a['sent']:<23} | {a['recv']:<23} | "
            f"{a['lat']:>7.2f} | {a['status']:<6} | "
            f"{a['exec_qty']:<8.6f} | {a['price']:<10.8f} | {a['msg']}"
        )

# ──────────────────────────────────────────────────────────────────────────────
#  Synchronizacja czasu i warmup
# ──────────────────────────────────────────────────────────────────────────────
async def get_server_offset(client):
    r = await client.get(f"{REST_URL}/api/v3/time")
    return r.json()["serverTime"] - int(time.time() * 1000)

async def warmup(client):
    ts = int(time.time() * 1000) - 100_000
    dummy = {
        "symbol": SYMBOL,
        "side": "BUY",
        "type": "MARKET",
        "quoteOrderQty": "0.000001",
        "recvWindow": "2000",
        "timestamp": str(ts)
    }
    dummy["signature"] = sign(dummy, API_SECRET)
    await client.post(f"{REST_URL}/api/v3/order", params=dummy, headers={"X-MEXC-APIKEY": API_KEY})

# ──────────────────────────────────────────────────────────────────────────────
#  Przygotowanie limit‐IOC BUY
# ──────────────────────────────────────────────────────────────────────────────
async def prepare_buy(client):
    r = await client.get(f"{REST_URL}/api/v3/depth", params={"symbol": SYMBOL, "limit": 5})
    asks = r.json().get("asks", [])
    if not asks:
        return None
    market_price = float(asks[0][0])
    limit_price = round(market_price * (1 + PRICE_MARKUP_PCT / 100), 8)
    print(f"[PREP] market={market_price} → limit={limit_price}")
    return {
        "limit_price": limit_price,
        "template": {
            "symbol": SYMBOL,
            "side": "BUY",
            "type": "LIMIT",
            "price": str(limit_price),
            "quantity": None,            # tu uzupełniamy poniżej
            "timeInForce": "IOC",
            "recvWindow": "5000"
        },
        "url": f"{REST_URL}/api/v3/order",
        "headers": {"X-MEXC-APIKEY": API_KEY}
    }

# ──────────────────────────────────────────────────────────────────────────────
#  Wysłanie BUY przez REST z zapisem ceny
# ──────────────────────────────────────────────────────────────────────────────
async def place_buy(client, build, offset, send_at, qty, success_evt):
    busy_wait_until(send_at)
    if success_evt.is_set():
        return None

    # budowa parametrów
    params = build["template"].copy()
    params["quantity"] = str(qty)
    params["timestamp"] = str(int(time.time() * 1000) + offset)
    ordered = {k: params[k] for k in ["symbol","side","type","price","quantity","timeInForce","recvWindow","timestamp"]}
    ordered["signature"] = sign(ordered, API_SECRET)

    sent = datetime.now().strftime("%H:%M:%S.%f")[:-3]
    start = time.perf_counter()
    resp = await client.post(build["url"], params=ordered, headers=build["headers"])
    lat = (time.perf_counter() - start) * 1000
    data = resp.json()

    status = "ERR"
    exec_qty = float(data.get("executedQty", 0))
    if "orderId" in data:
        status = "OK" if exec_qty > 0 else "NOFILL"
        if exec_qty > 0:
            success_evt.set()

    msg = data.get("msg", "")
    price = build["limit_price"]

    return {
        "sent":     sent,
        "recv":     datetime.now().strftime("%H:%M:%S.%f")[:-3],
        "lat":      lat,
        "status":   status,
        "exec_qty": exec_qty,
        "price":    price,
        "msg":       msg
    }

# ──────────────────────────────────────────────────────────────────────────────
#  Fallback MARKET BUY (tu cena = 0)
# ──────────────────────────────────────────────────────────────────────────────
async def place_market(client, offset, send_at, amount, success_evt):
    busy_wait_until(send_at)
    if success_evt.is_set():
        return None

    ts = str(int(time.time() * 1000) + offset)
    params = {
        "symbol": SYMBOL,
        "side": "BUY",
        "type": "MARKET",
        "quoteOrderQty": str(amount),
        "recvWindow": "5000",
        "timestamp": ts
    }
    params["signature"] = sign(params, API_SECRET)

    sent = datetime.now().strftime("%H:%M:%S.%f")[:-3]
    start = time.perf_counter()
    resp = await client.post(f"{REST_URL}/api/v3/order", params=params, headers={"X-MEXC-APIKEY": API_KEY})
    lat = (time.perf_counter() - start) * 1000
    data = resp.json()

    status = "ERR"
    exec_qty = float(data.get("executedQty", 0))
    if "orderId" in data:
        status = "OK" if exec_qty > 0 else "NOFILL"
        if exec_qty > 0:
            success_evt.set()

    msg = data.get("msg", "")
    price = 0.0

    return {
        "sent":     sent,
        "recv":     datetime.now().strftime("%H:%M:%S.%f")[:-3],
        "lat":      lat,
        "status":   status,
        "exec_qty": exec_qty,
        "price":    price,
        "msg":       msg
    }

# ──────────────────────────────────────────────────────────────────────────────
#  Główna sekwencja
# ──────────────────────────────────────────────────────────────────────────────
async def main():
    print(f"[INFO] {SYMBOL} @ {LISTING_TIME}, amount={QUOTE_AMOUNT}, markup={PRICE_MARKUP_PCT}%, profit={PROFIT_PCT}%")

    # moment T0 w ms
    t0_ms = int(datetime.fromisoformat(LISTING_TIME)
                .astimezone(timezone.utc).timestamp() * 1000)

    async with httpx.AsyncClient(http2=True) as client:
        # 1) czekaj do 200 ms przed T0
        print(f"[WAIT] czekam do {t0_ms - 200} (200 ms przed)…")
        busy_wait_until(t0_ms - 200)

        # 2) sync + warmup
        offset = await get_server_offset(client)
        print(f"[SYNC] offset: {offset} ms → warmup")
        await warmup(client)

        # 3) prepare BUY
        build = await prepare_buy(client)
        if not build:
            print("[BOT] brak orderbook → market fallback")
        qty = round(QUOTE_AMOUNT / build["limit_price"], 6) if build else None

        # 4) wyślij 3 próby: T0−10, T0−5, T0
        buy_offsets = [-10, -5, 0]
        success_evt = asyncio.Event()
        tasks = []
        for off in buy_offsets:
            send_at = t0_ms + off
            if build:
                tasks.append(place_buy(client, build, offset, send_at, qty, success_evt))
            else:
                tasks.append(place_market(client, offset, send_at, QUOTE_AMOUNT, success_evt))

        # 5) zbierz i wyświetl wyniki
        results = [r for r in await asyncio.gather(*tasks) if r]
        log_attempts(results)

        # 6) znajdź pierwszą udaną
        filled = 0.0
        for r in results:
            if r["status"] == "OK" and r["exec_qty"] > 0:
                filled = r["exec_qty"] * r["price"]
                break
        if filled <= 0:
            print("[BOT] no buy")
            return

        # 7) SELL LIMIT na PROFIT_PCT
        limit_price = results[0]["price"]
        sell_price = round(limit_price * (1 + PROFIT_PCT / 100), 8)
        sell_qty   = round(filled / limit_price, 6)
        print(f"[BOT] SELL {sell_qty}@{sell_price}")

        ts = str(int(time.time() * 1000) + offset)
        sell_params = {
            "symbol":      SYMBOL,
            "side":        "SELL",
            "type":        "LIMIT",
            "price":       str(sell_price),
            "quantity":    str(sell_qty),
            "timeInForce": "GTC",
            "recvWindow":  "5000",
            "timestamp":   ts
        }
        sell_params["signature"] = sign(sell_params, API_SECRET)
        start = time.perf_counter()
        resp = await client.post(f"{REST_URL}/api/v3/order",
                                 params=sell_params,
                                 headers={"X-MEXC-APIKEY": API_KEY})
        lat = (time.perf_counter() - start) * 1000
        d = resp.json()
        status = "OK" if "orderId" in d else "ERR"
        msg    = d.get("msg", "")
        print(f"[SELL] status={status} qty={sell_qty} price={sell_price} msg={msg} lat={lat:.2f}ms")

if __name__ == "__main__":
    asyncio.run(main())
