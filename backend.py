from fastapi import FastAPI, Request
from fastapi.middleware.cors import CORSMiddleware
import json
from pathlib import Path

app = FastAPI()

# POZWÓL na połączenia z iOS
app.add_middleware(
    CORSMiddleware,
    allow_origins=["*"],
    allow_methods=["*"],
    allow_headers=["*"],
)

LISTINGS_PATH = Path("listings.json")

@app.post("/add_listing")
async def add_listing(listing: dict):
    listings = []
    if LISTINGS_PATH.exists():
        with open(LISTINGS_PATH, "r") as f:
            listings = json.load(f)
    listings.append(listing)
    with open(LISTINGS_PATH, "w") as f:
        json.dump(listings, f, indent=2, ensure_ascii=False)
    return {"status": "ok"}

@app.get("/listings")
def get_listings():
    if LISTINGS_PATH.exists():
        with open(LISTINGS_PATH) as f:
            return json.load(f)
    return []

@app.get("/status")
def status():
    return {"ok": True}
