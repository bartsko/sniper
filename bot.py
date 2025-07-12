#!/usr/bin/env python3
import asyncio, time, hmac, hashlib, json
from datetime import datetime, timezone
import httpx
from pathlib import Path

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
LISTING_TIME     = listing["listing_time"]
PRICE_MARKUP_PCT = float(listing.get("price_markup_pct", 20))
PROFIT_PCT       = float(listing.get("profit_pct", 200))

REST_URL = "https://api.mexc.com"

def sign(params: dict, secret: str) -> str:
    # NIE sortuj! Musi być dokładnie taka kolejność jak w params
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
            "quantity": None, # wypełnione później
            "timeInForce": "IOC",
            "recvWindow": "5000"
        },
        "url": f"{REST_URL}/api/v3/order",
        "headers": {"X-MEXC-APIKEY":API_KEY}
    }

def busy_wait(ms:int):
    target = ms*1_000_000
    while time.time_ns()<target: pass

async def place_buy(client,build,offset,send_at,qty):
    busy_wait(send_at)
    p=build["template_base"].copy()
    p["quantity"]=str(qty)
    p["timestamp"]=str(int(time.time()*1000)+offset)
    # Musi być dokładna kolejność: symbol, side, type, price, quantity, timeInForce, recvWindow, timestamp
    p = {k: p[k] for k in ["symbol","side","type","price","quantity","timeInForce","recvWindow","timestamp"]}
    p["signature"]=sign(p,API_SECRET)
    sent=datetime.now().strftime("%H:%M:%S.%f")[:-3]
    start=time.perf_counter()
    resp=await client.post(build["url"],params=p,headers=build["headers"])
    lat=(time.perf_counter()-start)*1000
    d=resp.json(); status="OK" if "orderId" in d else "ERR"
    return {"sent":sent,"recv":datetime.now().strftime("%H:%M:%S.%f")[:-3],"lat":lat,"status":status,"exec_qty":float(d.get("executedQty","0")),"msg":d.get("msg","")}

async def place_market(client,offset,send_at,amount):
    busy_wait(send_at)
    ts=str(int(time.time()*1000)+offset)
    # parametry i kolejność kluczy!
    params = {
        "symbol": SYMBOL,
        "side": "BUY",
        "type": "MARKET",
        "quoteOrderQty": str(amount),
        "recvWindow": "5000",
        "timestamp": ts
    }
    params["signature"] = sign(params, API_SECRET)
    sent=datetime.now().strftime("%H:%M:%S.%f")[:-3]
    start=time.perf_counter()
    resp=await client.post(f"{REST_URL}/api/v3/order", params=params, headers={"X-MEXC-APIKEY":API_KEY})
    lat=(time.perf_counter()-start)*1000
    d=resp.json(); status="OK" if "orderId" in d else "ERR"
    return {"sent":sent,"recv":datetime.now().strftime("%H:%M:%S.%f")[:-3],"lat":lat,"status":status,"exec_qty":float(d.get("executedQty","0")),"msg":d.get("msg","")}

async def main():
    print(f"[INFO] {SYMBOL} @ {LISTING_TIME}, amount={QUOTE_AMOUNT}, markup={PRICE_MARKUP_PCT}%, profit={PROFIT_PCT}%")
    async with httpx.AsyncClient(http2=True) as client:
        offset=await get_server_offset(client); print(f"[SYNC] offset: {offset}ms")
        t0=int(datetime.fromisoformat(LISTING_TIME).astimezone(timezone.utc).timestamp()*1000)
        now=lambda:int(time.time()*1000)
        
        # 5 sekund przed listingiem — sprawdź orderbook
        while now()<t0-5000: await asyncio.sleep(0.01)
        build=await prepare_buy(client)
        await warmup(client)
        t0_local = t0
        atts=[]; rem=QUOTE_AMOUNT
        buy_times = [t0_local-10, t0_local-5, t0_local]

        if build is not None:
            # Są dane orderbook: próbujemy LIMIT
            for sa in buy_times:
                if rem <= 0:
                    break
                q=round(rem/build["limit_price"],6)
                att=await place_buy(client,build,offset,sa,q)
                atts.append(att)
                if att["status"]=="OK" and att["exec_qty"]>0:
                    rem -= att["exec_qty"]*build["limit_price"]
                    print("[BOT] Udało się kupić LIMIT — przerywam kolejne próby.")
                    break
        else:
            # Orderbook pusty: próbujemy MARKET
            print("[BOT] Próba market (orderbook pusty/lub fail limitów)...")
            for sa in buy_times:
                if rem <= 0:
                    break
                att=await place_market(client,offset,sa,rem)
                atts.append(att)
                if att["status"]=="OK" and att["exec_qty"]>0:
                    rem -= att["exec_qty"]
                    print("[BOT] Udało się kupić MARKET — przerywam kolejne próby.")
                    break

        log_attempts(atts)
        bought=QUOTE_AMOUNT-rem
        if bought<=0: print("[BOT] no buy"); return
        # SELL jak dotąd
        if build is not None:
            sell_price=round((build["limit_price"])*(1+PROFIT_PCT/100),8)
            sell_qty=bought/(build["limit_price"])
        else:
            # fallback — jak nie wiemy po jakiej cenie kupione, podajemy z QUOTE_AMOUNT
            sell_price = 0  # (do obliczenia)
            sell_qty = bought  # (do obliczenia)
        print(f"[BOT] SELL {sell_qty}@{sell_price}")
        sp={
            "symbol": SYMBOL,
            "side": "SELL",
            "type": "LIMIT",
            "price": str(sell_price),
            "quantity": str(round(sell_qty,6)),
            "timeInForce":"GTC",
            "recvWindow":"5000",
            "timestamp":str(int(time.time()*1000)+offset)
        }
        # Kolejność parametrów!
        sp = {k: sp[k] for k in ["symbol","side","type","price","quantity","timeInForce","recvWindow","timestamp"]}
        sp["signature"]=sign(sp,API_SECRET)
        sent=datetime.now().strftime("%H:%M:%S.%f")[:-3]; start=time.perf_counter()
        resp=await client.post(f"{REST_URL}/api/v3/order",params=sp,headers={"X-MEXC-APIKEY":API_KEY})
        lat=(time.perf_counter()-start)*1000; d=resp.json(); status="OK" if "orderId" in d else "ERR"
        log_attempts([{"sent":sent,"recv":datetime.now().strftime("%H:%M:%S.%f")[:-3],"lat":lat,"status":status,"exec_qty":sell_qty,"msg":d.get("msg","")}])

if __name__=="__main__":
    asyncio.run(main())
