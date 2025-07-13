#!/usr/bin/env python3
import json
import uuid
import subprocess
from pathlib import Path
from datetime import datetime, timedelta

import pytz
from fastapi import FastAPI, HTTPException
from fastapi.middleware.cors import CORSMiddleware
from pydantic import BaseModel
from apscheduler.schedulers.background import BackgroundScheduler
from apscheduler.jobstores.sqlalchemy import SQLAlchemyJobStore
from apscheduler.events import EVENT_JOB_EXECUTED, EVENT_JOB_ERROR

# ──────────────────────────────────────────────────────────────────────────────
# Ścieżki i konfiguracja
# ──────────────────────────────────────────────────────────────────────────────
BASE_DIR      = Path(__file__).parent
LISTINGS_FILE = BASE_DIR / "listings.json"
CURRENT_FILE  = BASE_DIR / "current_listing.json"
BOT_BINARY    = BASE_DIR / "sniper-bot"        # skompilowana binarka Go

# ──────────────────────────────────────────────────────────────────────────────
# FastAPI + CORS
# ──────────────────────────────────────────────────────────────────────────────
app = FastAPI()
app.add_middleware(
    CORSMiddleware,
    allow_origins=["*"],
    allow_methods=["*"],
    allow_headers=["*"],
)

# ──────────────────────────────────────────────────────────────────────────────
# APScheduler z trwałym jobstore SQLite
# ──────────────────────────────────────────────────────────────────────────────
jobstores = {'default': SQLAlchemyJobStore(url='sqlite:///jobs.sqlite')}
scheduler = BackgroundScheduler(jobstores=jobstores, timezone=pytz.UTC)

def _listener(event):
    if event.code == EVENT_JOB_EXECUTED:
        print(f"[SCHED] Job {event.job_id} executed")
    else:
        print(f"[SCHED] Job {event.job_id} error:", event.exception)

scheduler.add_listener(_listener, EVENT_JOB_EXECUTED | EVENT_JOB_ERROR)

@app.on_event("startup")
def _start_scheduler():
    if not scheduler.running:
        scheduler.start()
        print("[SCHED] Scheduler started. Existing jobs:")
        for job in scheduler.get_jobs():
            print("  •", job.id, "->", job.next_run_time)


# ──────────────────────────────────────────────────────────────────────────────
# Prosty endpoint do sprawdzenia, czy backend działa
# ──────────────────────────────────────────────────────────────────────────────
@app.get("/status")
async def status():
    return {"ok": True}


# ──────────────────────────────────────────────────────────────────────────────
# Model przychodzącego JSON
# ──────────────────────────────────────────────────────────────────────────────
class ListingIn(BaseModel):
    exchange: str
    api_key: str
    api_secret: str
    symbol: str
    quote_amount: float
    price_markup_pct: int
    profit_pct: int = 200
    listing_time: datetime


# ──────────────────────────────────────────────────────────────────────────────
# Funkcja uruchamiająca bota Go
# ──────────────────────────────────────────────────────────────────────────────
def run_bot(listing_id: str):
    # Zapisujemy dane listing do current_listing.json
    all_listings = json.loads(LISTINGS_FILE.read_text())
    entry = next(x for x in all_listings if x["id"] == listing_id)
    CURRENT_FILE.write_text(json.dumps(entry, indent=2))
    # Uruchamiamy binarkę Go z argumentem ID
    subprocess.Popen([str(BOT_BINARY), listing_id], cwd=str(BASE_DIR))
    print(f"[SCHED] Launched sniper-bot for listing {listing_id}")


def job_trigger(listing_id: str):
    print(f"[SCHED] Triggering bot for {listing_id}")
    run_bot(listing_id)


# ──────────────────────────────────────────────────────────────────────────────
# Endpointy CRUD dla listingów
# ──────────────────────────────────────────────────────────────────────────────
@app.post("/add_listing")
async def add_listing(data: ListingIn):
    # Wczytujemy lub tworzymy listę
    try:
        lst = json.loads(LISTINGS_FILE.read_text())
    except FileNotFoundError:
        lst = []
    # Dodajemy nowy wpis
    new_id = str(uuid.uuid4())
    ent = data.dict()
    ent["id"] = new_id
    ent["listing_time"] = data.listing_time.isoformat()
    lst.append(ent)
    LISTINGS_FILE.write_text(json.dumps(lst, indent=2))
    # Planowanie joba: 10s przed listing_time
    run_at = data.listing_time.astimezone(pytz.UTC) - timedelta(seconds=10)
    scheduler.add_job(job_trigger, 'date', run_date=run_at, args=[new_id], id=new_id)
    print(f"[SCHED] Scheduled job {new_id} at {run_at.isoformat()}")
    return {"status": "ok", "id": new_id}


@app.get("/listings")
async def get_listings():
    if LISTINGS_FILE.exists():
        return json.loads(LISTINGS_FILE.read_text())
    return []


@app.delete("/listings/{listing_id}")
async def delete_listing(listing_id: str):
    if not LISTINGS_FILE.exists():
        raise HTTPException(404, "No listings")
    lst = json.loads(LISTINGS_FILE.read_text())
    filtered = [x for x in lst if x["id"] != listing_id]
    if len(filtered) == len(lst):
        raise HTTPException(404, "Not found")
    LISTINGS_FILE.write_text(json.dumps(filtered, indent=2))
    scheduler.remove_job(listing_id)
    print(f"[SCHED] Removed job {listing_id}")
    return {"status": "ok"}
