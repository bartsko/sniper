#!/usr/bin/env python3
# scheduler.py

import json, time, subprocess
from datetime import datetime, timezone
from threading import Thread
from pathlib import Path

# Ścieżki
REPO_DIR      = Path(__file__).parent
LISTINGS_FILE = REPO_DIR / "listings.json"
CURRENT_FILE  = REPO_DIR / "current_listing.json"
BOT_SCRIPT    = REPO_DIR / "bot.py"

def parse_dt(iso_str: str) -> datetime:
    # Zamienia ISO8601+offset na datetime UTC
    return datetime.fromisoformat(iso_str).astimezone(timezone.utc)

def run_bot_for(listing: dict):
    # Nadpisujemy current_listing.json, aby bot.py mógł je wczytać
    with open(CURRENT_FILE, "w") as f:
        json.dump(listing, f, indent=2)
    print(f"[SCHED] Uruchamiam bot dla {listing['symbol']} o {listing['listing_time']}")
    # Uruchom bot.py w tle
    subprocess.Popen(["python3", str(BOT_SCRIPT)])

def schedule_listings():
    # Wczytujemy tablicę listingów
    if not LISTINGS_FILE.exists():
        print(f"⚠️  Brak {LISTINGS_FILE}")
        return
    with open(LISTINGS_FILE) as f:
        all_listings = json.load(f)

    # Dziś UTC
    now = datetime.now(timezone.utc)

    for listing in all_listings:
        dt_utc = parse_dt(listing["listing_time"])
        delta = (dt_utc - now).total_seconds() - 3  # startujemy 3 sekundy wcześniej dla warmupu
        delay = max(delta, 0)
        print(f"[SCHED] {listing['symbol']} w {delay:.1f}s")
        # Uruchamiamy w osobnym wątku timer
        Thread(target=lambda lst=listing: (time.sleep(delay), run_bot_for(lst))).start()

if __name__ == "__main__":
    print("[SCHED] Scheduler wystartowany")
    schedule_listings()
    # Trzymaj proces przy życiu
    try:
        while True:
            time.sleep(60)
    except KeyboardInterrupt:
        print("\n[SCHED] Zatrzymano")    
