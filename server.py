#!/usr/bin/env python3
import json, uuid, subprocess
from pathlib import Path
from datetime import datetime, timedelta

import pytz
from fastapi import FastAPI, HTTPException
from fastapi.middleware.cors import CORSMiddleware
from pydantic import BaseModel
from apscheduler.schedulers.background import BackgroundScheduler
from apscheduler.jobstores.sqlalchemy import SQLAlchemyJobStore
from apscheduler.events import EVENT_JOB_EXECUTED, EVENT_JOB_ERROR

# —————————————————————————————————————————————
# Ścieżki i skrypty
BASE_DIR       = Path(__file__).parent
LISTINGS_FILE  = BASE_DIR / "listings.json"
CURRENT_FILE   = BASE_DIR / "current_listing.json"
BOT_SCRIPT     = BASE_DIR / "bot.py"

# —————————————————————————————————————————————
# FastAPI + CORS
app = FastAPI()
app.add_middleware(
    CORSMiddleware,
    allow_origins=["*"],
    allow_methods=["*"],
    allow_headers=["*"],
)

# —————————————————————————————————————————————
# APScheduler z trwałym jobstore
jobstores = {'default': SQLAlchemyJobStore(url='sqlite:///jobs.sqlite')}
scheduler = BackgroundScheduler(jobstores=jobstores, timezone=pytz.UTC)

# Listener, aby widzieć wykonanie lub błędy zadań
def _listener(event):
    if event.code == EVENT_JOB_EXECUTED:
        print(f"[SCHED-EVENT] Job {event.job_id} executed")
    elif event.code == EVENT_JOB_ERROR:
        print(f"[SCHED-EVENT] Job {event.job_id} error:", event.exception)

scheduler.add_listener(_listener, EVENT_JOB_EXECUTED | EVENT_JOB_ERROR)

# —————————————————————————————————————————————
# Funkcja pomocnicza dumpująca joby
def dump_jobs(label: str):
    print(f"[SCHED] {label}:")
    for job in scheduler.get_jobs():
        print(f"  • {job.id} -> next_run_time={job.next_run_time}")

# —————————————————————————————————————————————
# Start schedulera na starcie FastAPI
@app.on_event("startup")
def _start_scheduler():
    if not scheduler.running:
        scheduler.start()
        print("[SCHED] Scheduler started")
        dump_jobs("Jobs at startup")

# —————————————————————————————————————————————
# Model przychodzącego JSON
class ListingIn(BaseModel):
    exchange: str
    api_key: str
    api_secret: str
    symbol: str
    quote_amount: float
    price_markup_pct: int
    profit_pct: int = 200
    listing_time: datetime

# —————————————————————————————————————————————
# Uruchomienie bot.py dla pojedynczego listing
def run_bot(listing: dict):
    with open(CURRENT_FILE, "w", encoding="utf-8") as f:
        json.dump(listing, f, indent=2)
    proc = subprocess.Popen(
        ["python3", str(BOT_SCRIPT)],
        cwd=str(BASE_DIR)
    )
    print(f"[SCHED] Launched bot.py (pid {proc.pid}) for listing {listing['id']}")

# —————————————————————————————————————————————
# Funkcja wywoływana przez APScheduler
def start_bot_job(listing_id: str):
    dump_jobs("Jobs at trigger")
    if not LISTINGS_FILE.exists():
        return
    all_l = json.loads(LISTINGS_FILE.read_text())
    entry = next((l for l in all_l if l["id"] == listing_id), None)
    if not entry:
        print(f"[SCHED] Listing {listing_id} not found")
        return
    print(f"[SCHED] Triggering bot for {entry['symbol']} @ {entry['listing_time']}")
    run_bot(entry)

# —————————————————————————————————————————————
# Dodanie zadania do schedulera
def schedule_bot_job(listing_id: str, listing_time: datetime):
    # wymuś timezone UTC jeśli brakuje
    if listing_time.tzinfo is None:
        listing_time = listing_time.replace(tzinfo=pytz.UTC)
    run_at = listing_time.astimezone(pytz.UTC) - timedelta(seconds=10)
    now   = datetime.now(pytz.UTC)
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
    dump_jobs("Jobs after scheduling")

# —————————————————————————————————————————————
# Endpoints
@app.post("/add_listing")
async def add_listing(payload: ListingIn):
    try:
        lst = json.loads(LISTINGS_FILE.read_text())
    except FileNotFoundError:
        lst = []
    new_id = str(uuid.uuid4())
    entry = payload.dict()
    entry["id"]           = new_id
    entry["listing_time"] = payload.listing_time.isoformat()
    lst.append(entry)
    with open(LISTINGS_FILE, "w", encoding="utf-8") as f:
        json.dump(lst, f, indent=2)

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
    lst = json.loads(LISTINGS_FILE.read_text())
    filtered = [l for l in lst if l["id"] != listing_id]
    if len(filtered) == len(lst):
        raise HTTPException(404, "Not found")
    with open(LISTINGS_FILE, "w", encoding="utf-8") as f:
        json.dump(filtered, f, indent=2)
    job_id = f"bot-for-{listing_id}"
    if scheduler.get_job(job_id):
        scheduler.remove_job(job_id)
    return {"status": "ok"}
