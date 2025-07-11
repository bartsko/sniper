import asyncio
import time
import hmac
import hashlib
import json
from datetime import datetime, timezone
import httpx
import websockets
import os

REST = "https://api.mexc.com"
WS   = "wss://wbs.mexc.com/ws"

def load_listing(path="listings.json"):
    with open(path) as f:
        listing = json.load(f)
    if isinstance(listing, list):
        return listing[0]
    return listing

def sign(secret, qs):
    return hmac.new(secret.encode(), qs.encode(), hashlib.sha256).hexdigest()

async def get_server_offset():
    async with httpx.AsyncClient(timeout=2.0) as c:
        srv = (await c.get(f"{REST}/api/v3/time")).json()["serverTime"]
    return srv - int(time.time() * 1000)

async def tcp_warmup(api_key, api_secret, symbol):
    async with httpx.AsyncClient(timeout=2.0) as c:
        await c.get(f"{REST}/api/v3/time")
        ts = int(time.time() * 1000) - 100_000
        qs = (f"symbol={symbol}&side=BUY&type=MARKET"
              f"&quoteOrderQty=0.000001&recvWindow=2000"
              f"&timestamp={ts}")
        sig = sign(api_secret, qs)
        await c.post(f"{REST}/api/v3/order?{qs}&signature={sig}",
                     headers={"X-MEXC-APIKEY": api_key})

def busy_wait(ms_ts):
    target = ms_ts * 1_000_000
    while time.time_ns() < target:
        pass

async def place_market(api_key, api_secret, symbol, quote_amount, offset, send_at):
    busy_wait(send_at)
    ts = int(time.time() * 1000) + offset
    qs = (f"symbol={symbol}&side=BUY&type=MARKET"
          f"&quoteOrderQty={quote_amount}&recvWindow=5000"
          f"&timestamp={ts}")
    sig = sign(api_secret, qs)
    url = f"{REST}/api/v3/order?{qs}&signature={sig}"

    sent = datetime.now().strftime("%H:%M:%S.%f")[:-3]
    start = time.perf_counter()
    async with httpx.AsyncClient(timeout=2.0) as c:
        r = await c.post(url, headers={"X-MEXC-APIKEY": api_key})
    dt = (time.perf_counter() - start) * 1000
    d = r.json()
    status = "OK" if "orderId" in d else "ERR"
    msg = d.get("msg", "")
    print(f"[{sent}] {status} {msg} ({dt:.2f}ms)")

async def main():
    listing = load_listing()
    api_key      = listing["api_key"]
    api_secret   = listing["api_secret"]
    symbol       = listing["symbol"].upper()
    quote_amount = float(listing["quote_amount"])
    listing_time = listing["listing_time"]
    take_profit  = listing.get("take_profit", None)  # Dodane pobieranie TP%

    print(f"\n[INFO] Symbol: {symbol}, Kwota: {quote_amount} USDT, Take Profit: {take_profit}%")

    offset = await get_server_offset()
    print(f"[SYNC] offset serwera: {offset} ms")

    listing_dt = datetime.fromisoformat(listing_time)
    listing_ts = int(listing_dt.astimezone(timezone.utc).timestamp() * 1000)
    print(f"[WAIT] Czekam do T0-3000ms: {listing_ts - 3000} ms since epoch")
    while int(time.time() * 1000) < listing_ts - 3000:
        await asyncio.sleep(0.001)

    print("[WARMUP] TCP/TLS warmup 3s przed T0…")
    await tcp_warmup(api_key, api_secret, symbol)

    print("[WS] Łączę websocket…")
    async with websockets.connect(WS) as ws:
        sub = {
            "method": "SUBSCRIBE",
            "params": [f"{symbol.lower()}@trade"],
            "id": 1
        }
        await ws.send(json.dumps(sub))
        print(f"[WS] Subscribed to {symbol.lower()}@trade")

        while True:
            msg = await ws.recv()
            data = json.loads(msg)
            if data.get("e") == "trade":
                t0_local = int(time.time() * 1000)
                print(f"[WS] Pierwszy trade @ {t0_local} ms (T0)")
                break

    await asyncio.gather(
        place_market(api_key, api_secret, symbol, quote_amount, offset, t0_local - 10),
        place_market(api_key, api_secret, symbol, quote_amount, offset, t0_local - 5),
        place_market(api_key, api_secret, symbol, quote_amount, offset, t0_local),
    )

if __name__ == "__main__":
    asyncio.run(main())
