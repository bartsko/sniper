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

BASE_DIR      = Path(__file__).parent
LISTINGS_FILE = BASE_DIR / "listings.json"
CURRENT_FILE  = BASE_DIR / "current_listing.json"
BOT_BINARY    = BASE_DIR / "sniper-bot"

app = FastAPI()
app.add_middleware(
    CORSMiddleware, allow_origins=["*"], allow_methods=["*"], allow_headers=["*"]
)

jobstores = {'default': SQLAlchemyJobStore(url='sqlite:///jobs.sqlite')}
scheduler = BackgroundScheduler(jobstores=jobstores, timezone=pytz.UTC)

def _listener(evt):
    if evt.code == EVENT_JOB_EXECUTED:
        print(f"[SCHED] Job {evt.job_id} done")
    else:
        print(f"[SCHED] Job {evt.job_id} ERROR", evt.exception)

scheduler.add_listener(_listener, EVENT_JOB_EXECUTED | EVENT_JOB_ERROR)

@app.on_event("startup")
def start_sched():
    scheduler.start()
    print("[SCHED] Started. Existing jobs:")
    for j in scheduler.get_jobs(): print(" ", j)

class ListingIn(BaseModel):
    exchange: str
    api_key: str
    api_secret: str
    symbol: str
    quote_amount: float
    price_markup_pct: int
    profit_pct: int = 200
    listing_time: datetime

def run_bot(listing_id: str):
    # zapis pod aktualny listing
    all_listings = json.loads(LISTINGS_FILE.read_text())
    entry = next(x for x in all_listings if x["id"] == listing_id)
    CURRENT_FILE.write_text(json.dumps(entry, indent=2))
    # uruchom binarkę Go z argumentem listing_id
    subprocess.Popen([str(BOT_BINARY), listing_id], cwd=str(BASE_DIR))
    print(f"[SCHED] Launched bot-for-{listing_id}")

def job_trigger(listing_id: str):
    print(f"[SCHED] Trigger for {listing_id}")
    run_bot(listing_id)

@app.post("/add_listing")
async def add_listing(data: ListingIn):
    try:
        lst = json.loads(LISTINGS_FILE.read_text())
    except FileNotFoundError:
        lst = []
    new_id = str(uuid.uuid4())
    ent = data.dict()
    ent["id"] = new_id
    ent["listing_time"] = data.listing_time.isoformat()
    lst.append(ent)
    LISTINGS_FILE.write_text(json.dumps(lst, indent=2))
    # schedule T0 - 10s
    t0 = data.listing_time.astimezone(pytz.UTC) - timedelta(seconds=10)
    scheduler.add_job(job_trigger, 'date', run_date=t0, args=[new_id], id=new_id)
    return {"status":"ok","id":new_id}

@app.get("/listings")
async def get_listings():
    if LISTINGS_FILE.exists():
        return json.loads(LISTINGS_FILE.read_text())
    return []

@app.delete("/listings/{lid}")
async def del_listing(lid:str):
    if not LISTINGS_FILE.exists(): raise HTTPException(404)
    lst = json.loads(LISTINGS_FILE.read_text())
    filtered = [x for x in lst if x["id"] != lid]
    if len(filtered)==len(lst): raise HTTPException(404)
    LISTINGS_FILE.write_text(json.dumps(filtered, indent=2))
    scheduler.remove_job(lid)
    return {"status":"ok"}
