#!/usr/bin/env python3
from fastapi import FastAPI, HTTPException
from fastapi.middleware.cors import CORSMiddleware
from pydantic import BaseModel
from datetime import datetime, timedelta
from apscheduler.schedulers.background import BackgroundScheduler
from apscheduler.jobstores.sqlalchemy import SQLAlchemyJobStore
from apscheduler.events import EVENT_JOB_EXECUTED, EVENT_JOB_ERROR
import pytz
import uuid
import json
import subprocess
from pathlib import Path

# —————————————————————————————————————————————
# Ustaw FastAPI
app = FastAPI()
app.add_middleware(
    CORSMiddleware,
    allow_origins=["*"],
    allow_methods=["*"],
    allow_headers=["*"],
)

# —————————————————————————————————————————————
# Ścieżki + skrypty
BASE_DIR       = Path(__file__).parent
LISTINGS_FILE  = BASE_DIR / "listings.json"
CURRENT_FILE   = BASE_DIR / "current_listing.json"
BOT_SCRIPT     = BASE_DIR / "bot.py"

# —————————————————————————————————————————————
# Konfiguracja APScheduler z jobstore
jobstores = {
    'default': SQLAlchemyJobStore(url='sqlite:///jobs.sqlite')
}
scheduler = BackgroundScheduler(jobstores=jobstores, timezone=pytz.UTC)

# Logger zdarzeń
def listener(event):
    if event.code == EVENT_JOB_EXECUTED:
        print(f"[SCHED-EVENT] Job {event.job_id} executed successfully")
    elif event.code == EVENT_JOB_ERROR:
        print(f"[SCHED-EVENT] Job {event.job_id} failed: {event.exception}")

scheduler.add_listener(listener, EVENT_JOB_EXECUTED | EVENT_JOB_ERROR)

# —————————————————————————————————————————————
# Start schedulera przy starcie FastAPI
@app.on_event("startup")
def start_scheduler():
    if not scheduler.running:
        scheduler.start()
        print("[SCHED] Scheduler started")
        # pokaż wszystkie zaplanowane zadania
        print("[SCHED] Current jobs:", scheduler.get_jobs())

# —————————————————————————————————————————————
# Model wejściowy
class ListingIn(BaseModel):
    exchange: str
    api_key: str
    api_secret: str
    symbol: str
    quote_amount: float
    price_markup_pct: int
    profit_pct: int = 200
    listing_time: datetime  # ISO z Z lub offsetem

# —————————————————————————————————————————————
def run_bot(listing: dict):
    # zapisz current_listing.json
    with open(CURRENT_FILE, 'w', encoding='utf-8') as f:
        json.dump(listing, f, indent=2)
    # odpalenie bot.py
    subprocess.Popen(
        ["python3", str(BOT_SCRIPT)],
        cwd=str(BASE_DIR)
    )

def start_bot_job(listing_id: str):
    # loguj wszystkie joby przed triggerem
    print("[SCHED] Jobs at trigger:", scheduler.get_jobs())
    # wczytaj listing
    listings = json.loads(LISTINGS_FILE.read_text())
    entry = next((l for l in listings if l.get('id') == listing_id), None)
    if not entry:
        print(f"[SCHED] start_bot_job: listing {listing_id} not found")
        return
    print(f"[SCHED] Triggering bot for {entry['symbol']} @ {entry['listing_time']}")
    run_bot(entry)

def schedule_bot_job(listing_id: str, listing_time: datetime):
    run_at = listing_time.astimezone(pytz.UTC) - timedelta(seconds=10)
    now = datetime.now(pytz.UTC)
    if run_at <= now:
        print(f"[SCHED] Too late to schedule {listing_id}")
        return
    job_id = f"bot-for-{listing_id}"
    scheduler.add_job(
        start_bot_job,
        'date',
        run_date=run_at,
        args=[listing_id],
        id=job_id,
        replace_existing=True
    )
    print(f"[SCHED] Scheduled {listing_id} at {run_at.isoformat()}")
    print("[SCHED] Jobs after scheduling:", scheduler.get_jobs())

# —————————————————————————————————————————————
# Endpoints
@app.post("/add_listing")
async def add_listing(payload: ListingIn):
    try:
        listings = json.loads(LISTINGS_FILE.read_text())
    except FileNotFoundError:
        listings = []

    new_id = str(uuid.uuid4())
    entry = payload.dict()
    entry['id'] = new_id
    entry['listing_time'] = payload.listing_time.isoformat()
    listings.append(entry)

    with open(LISTINGS_FILE, 'w', encoding='utf-8') as f:
        json.dump(listings, f, indent=2)

    schedule_bot_job(new_id, payload.listing_time)
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
    listings = json.loads(LISTINGS_FILE.read_text())
    filtered = [l for l in listings if l.get('id') != listing_id]
    if len(filtered) == len(listings):
        raise HTTPException(404, "Listing not found")
    with open(LISTINGS_FILE, 'w', encoding='utf-8') as f:
        json.dump(filtered, f, indent=2)
    job_id = f"bot-for-{listing_id}"
    if scheduler.get_job(job_id):
        scheduler.remove_job(job_id)
    return {"status": "ok"}
