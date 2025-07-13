#!/usr/bin/env python3
import asyncio, time, hmac, hashlib, json, os
from datetime import datetime, timezone
import httpx, websockets
from pathlib import Path

# —————————————————————————————————————————————
# Ładowanie listing z dysku
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

REST_URL    = "https://api.mexc.com"
WS_URL      = "wss://wbs.mexc.com/ws"  # MEXC spot WS
# —————————————————————————————————————————————

def sign(params: dict, secret: str) -> str:
    qs = "&".join(f"{k}={params[k]}" for k in params)
    return hmac.new(secret.encode(), qs.encode(), hashlib.sha256).hexdigest()

def log_attempts(attempts):
    print("\n📊 Tabela prób:\n")
    hdr = f"{'Nr':<3} | {'Wysłano':<23} | {'Odpowiedź':<23} | {'Lat(ms)':<8} | {'Status':<5} | {'Qty':<8} | {'Msg'}"
    print(hdr)
    print("-" * len(hdr))
    for i,a in enumerate(attempts,1):
        print(f"{i:<3} | {a['sent']:<23} | {a['recv']:<23} | {a['lat']:>7.2f} | {a['status']:<5} | {a['exec_qty']:<8.6f} | {a['msg']}")

async def get_server_offset(client):
    r = await client.get(f"{REST_URL}/api/v3/time")
    return r.json()["serverTime"] - int(time.time()*1000)

async def warmup(client):
    await client.get(f"{REST_URL}/api/v3/time")
    ts = int(time.time()*1000)-100_000
    dummy = {
        "symbol": SYMBOL,
        "side": "BUY",
        "type": "MARKET",
        "quoteOrderQty": "0.000001",
        "recvWindow": "2000",
        "timestamp": str(ts)
    }
    dummy["signature"] = sign(dummy, API_SECRET)
    await client.post(f"{REST_URL}/api/v3/order", params=dummy, headers={"X-MEXC-APIKEY":API_KEY})

async def prepare_buy(client):
    r = await client.get(f"{REST_URL}/api/v3/depth", params={"symbol":SYMBOL,"limit":5})
    asks = r.json().get("asks",[])
    if not asks:
        return None
    price = float(asks[0][0])
    limit = round(price*(1+PRICE_MARKUP_PCT/100),8)
    print(f"[PREP] market={price} → limit={limit}")
    return {
        "limit_price":limit,
        "template_base": {
            "symbol": SYMBOL,
            "side": "BUY",
            "type": "LIMIT",
            "price": str(limit),
            "quantity": None,
            "timeInForce": "IOC",
            "recvWindow": "5000"
        },
        "url": f"{REST_URL}/api/v3/order",
        "headers": {"X-MEXC-APIKEY":API_KEY}
    }

def busy_wait_until(target_ms:int):
    while int(time.time()*1000) < target_ms:
        time.sleep(0.0005)

async def fetch_executed_qty(client, order_id):
    params = {
        "symbol": SYMBOL,
        "orderId": order_id,
        "timestamp": str(int(time.time()*1000))
    }
    params["signature"] = sign(params, API_SECRET)
    resp = await client.get(f"{REST_URL}/api/v3/order", params=params, headers={"X-MEXC-APIKEY":API_KEY})
    d = resp.json()
    return float(d.get("executedQty","0")), d

async def place_buy(client, build, offset, send_at, qty, success_event):
    busy_wait_until(send_at)
    if success_event.is_set(): return None
    p = build["template_base"].copy()
    p["quantity"] = str(qty)
    p["timestamp"] = str(int(time.time()*1000)+offset)
    p = {k:p[k] for k in ["symbol","side","type","price","quantity","timeInForce","recvWindow","timestamp"]}
    p["signature"] = sign(p, API_SECRET)
    sent = datetime.now().strftime("%H:%M:%S.%f")[:-3]
    start = time.perf_counter()
    resp = await client.post(build["url"], params=p, headers=build["headers"])
    lat = (time.perf_counter()-start)*1000
    d = resp.json()
    oid = d.get("orderId"); qty_exec = 0.0; msg = d.get("msg",""); status = "ERR"
    if oid:
        await asyncio.sleep(0.3)
        qty_exec, details = await fetch_executed_qty(client, oid)
        status = "OK" if qty_exec>0 else "NOFILL"
        msg += f" | {details.get('status','')}"
    result = {"sent":sent, "recv":datetime.now().strftime("%H:%M:%S.%f")[:-3],
              "lat":lat, "status":status, "exec_qty":qty_exec, "msg":msg}
    if status=="OK": success_event.set()
    return result

async def place_market(client, offset, send_at, amount, success_event):
    busy_wait_until(send_at)
    if success_event.is_set(): return None
    ts = str(int(time.time()*1000)+offset)
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
    resp = await client.post(f"{REST_URL}/api/v3/order", params=params, headers={"X-MEXC-APIKEY":API_KEY})
    lat = (time.perf_counter()-start)*1000
    d = resp.json()
    oid = d.get("orderId"); qty_exec = 0.0; msg = d.get("msg",""); status="ERR"
    if oid:
        await asyncio.sleep(0.3)
        qty_exec, details = await fetch_executed_qty(client, oid)
        status = "OK" if qty_exec>0 else "NOFILL"
        msg += f" | {details.get('status','')}"
    result = {"sent":sent,"recv":datetime.now().strftime("%H:%M:%S.%f")[:-3],
              "lat":lat,"status":status,"exec_qty":qty_exec,"msg":msg}
    if status=="OK": success_event.set()
    return result

# —————————————————————————————————————————————
# WebSocket listener dla ORDER_TRADE_UPDATE
async def user_data_listener():
    async with httpx.AsyncClient() as rest:
        # 1) otwórz listenKey
        r = await rest.post(f"{REST_URL}/api/v3/userDataStream", headers={"X-MEXC-APIKEY":API_KEY})
        listen_key = r.json().get("listenKey")
    uri = f"{WS_URL}/stream?streams={listen_key}"
    async with websockets.connect(uri) as ws:
        print("[WS ] Połączono do userDataStream")
        async for msg in ws:
            data = json.loads(msg)
            ev   = data.get("data",{})
            if ev.get("e")=="ORDER_TRADE_UPDATE":
                # tutaj możesz filtrować tylko swoje SYMBOL i przypisywać wyniki
                print("[WS ] ORDER_TRADE_UPDATE:", ev)

# —————————————————————————————————————————————
async def main():
    print(f"[INFO] {SYMBOL} @ {LISTING_TIME}, amount={QUOTE_AMOUNT}, markup={PRICE_MARKUP_PCT}%, profit={PROFIT_PCT}%")
    async with httpx.AsyncClient(http2=True) as client:
        # start WS listener równolegle
        asyncio.create_task(user_data_listener())

        offset = await get_server_offset(client)
        print(f"[SYNC] offset: {offset}ms")
        # czekaj do momentu snajpu
        t0      = int(datetime.fromisoformat(LISTING_TIME).astimezone(timezone.utc).timestamp()*1000)
        now_ms  = lambda:int(time.time()*1000)
        while now_ms()<t0-4000: await asyncio.sleep(0.01)
        offset = await get_server_offset(client)
        print(f"[SYNC] offset (przed zakupem): {offset}ms")
        while now_ms()<t0-5000: await asyncio.sleep(0.01)

        build  = await prepare_buy(client)
        await warmup(client)
        buy_ts = [t0-10, t0-5, t0]
        evt    = asyncio.Event()
        qty    = round(QUOTE_AMOUNT/build["limit_price"],6) if build else None

        # 3 próby
        tasks  = [ place_buy(client, build, offset, buy_ts[i], qty, evt) for i in range(3) ] \
              if build else \
              [ place_market(client, offset, buy_ts[i], QUOTE_AMOUNT, evt) for i in range(3) ]
        results = [r for r in await asyncio.gather(*tasks) if r]
        log_attempts(results)

        # sprzedaż follow-up
        bought = next((r["exec_qty"]*build["limit_price"] for r in results if r["status"]=="OK"), 0)
        if bought<=0:
            print("[BOT] no buy"); return

        sell_price = round(build["limit_price"]*(1+PROFIT_PCT/100),8)
        sell_qty   = round(bought/build["limit_price"],6)
        print(f"[BOT] SELL {sell_qty}@{sell_price}")
        # REST SELL, analogicznie do BUY…

if __name__=="__main__":
    asyncio.run(main())
