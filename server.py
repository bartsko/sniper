from fastapi import FastAPI, HTTPException
from pydantic import BaseModel
from datetime import datetime, timedelta
from apscheduler.schedulers.background import BackgroundScheduler
from apscheduler.jobstores.sqlalchemy import SQLAlchemyJobStore
import pytz
import uuid
import json

app = FastAPI()

# Configure persistent job store to survive restarts
jobstores = {
    'default': SQLAlchemyJobStore(url='sqlite:///jobs.sqlite')
}
scheduler = BackgroundScheduler(jobstores=jobstores, timezone=pytz.UTC)
scheduler.start()

class ListingIn(BaseModel):
    exchange: str
    api_key: str
    api_secret: str
    symbol: str
    quote_amount: float
    price_markup_pct: int
    listing_time: datetime  # ISO format, Z or offset


def start_bot(listing_id: str):
    # Your bot-start logic here
    print(f"🚀 Starting bot for listing {listing_id}")


def schedule_bot_job(listing_id: str, listing_time: datetime):
    run_at = listing_time.astimezone(pytz.UTC) - timedelta(seconds=10)
    now = datetime.now(pytz.UTC)
    if run_at <= now:
        return  # too late
    job_id = f"bot-for-{listing_id}"
    scheduler.add_job(
        start_bot,
        'date',
        run_date=run_at,
        args=[listing_id],
        id=job_id,
        replace_existing=True
    )

@app.post("/add_listing")
def add_listing(payload: ListingIn):
    # Append to listings.json
    try:
        with open("listings.json", "r+") as f:
            data = json.load(f)
    except FileNotFoundError:
        data = []
    new_id = str(uuid.uuid4())
    entry = {
        "id": new_id,
        "exchange": payload.exchange,
        "api_key": payload.api_key,
        "api_secret": payload.api_secret,
        "symbol": payload.symbol,
        "quote_amount": payload.quote_amount,
        "price_markup_pct": payload.price_markup_pct,
        "listing_time": payload.listing_time.isoformat()
    }
    data.append(entry)
    with open("listings.json", "w") as f:
        json.dump(data, f, indent=2)

    # Schedule bot job
    schedule_bot_job(new_id, payload.listing_time)

    return {"status": "ok", "id": new_id}
