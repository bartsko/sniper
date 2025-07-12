from fastapi import FastAPI, HTTPException
from fastapi.middleware.cors import CORSMiddleware
from pydantic import BaseModel
import json
from pathlib import Path
from typing import List

app = FastAPI()

# CORS dla połączeń z iOS
app.add_middleware(
    CORSMiddleware,
    allow_origins=["*"],
    allow_methods=["*"],
    allow_headers=["*"],
)

LISTINGS_PATH = Path("listings.json")

class Listing(BaseModel):
    api_key: str
    api_secret: str
    symbol: str
    quote_amount: float
    listing_time: str       # ISO8601+offset
    price_markup_pct: float = 20
    profit_pct: float = 200

class CancelRequest(BaseModel):
    symbol: str
    listing_time: str

@app.post("/login")
async def login(data: dict):
    # tutaj można dokładać walidację hasła, SSH itp.
    return {"status": "ok"}

@app.post("/add_listing")
async def add_listing(listing: Listing):
    listings: List[dict] = []
    if LISTINGS_PATH.exists():
        with open(LISTINGS_PATH, "r", encoding="utf-8") as f:
            listings = json.load(f)
    listings.append(listing.dict())
    with open(LISTINGS_PATH, "w", encoding="utf-8") as f:
        json.dump(listings, f, indent=2, ensure_ascii=False)
    return {"status": "ok"}

@app.get("/listings", response_model=List[Listing])
async def get_listings():
    if LISTINGS_PATH.exists():
        with open(LISTINGS_PATH, "r", encoding="utf-8") as f:
            return json.load(f)
    return []

@app.delete("/listings")
async def cancel_listing(req: CancelRequest):
    if not LISTINGS_PATH.exists():
        raise HTTPException(status_code=404, detail="No listings found")
    with open(LISTINGS_PATH, "r", encoding="utf-8") as f:
        listings = json.load(f)
    # filtrujemy wszystkie wpisy, które NIE pasują do symbolu + listing_time
    filtered = [
        l for l in listings
        if not (l.get("symbol") == req.symbol and l.get("listing_time") == req.listing_time)
    ]
    if len(filtered) == len(listings):
        # nic nie usunięto
        raise HTTPException(status_code=404, detail="Listing not found")
    with open(LISTINGS_PATH, "w", encoding="utf-8") as f:
        json.dump(filtered, f, indent=2, ensure_ascii=False)
    return {"status": "ok"}

@app.get("/status")
async def status():
    return {"ok": True}
