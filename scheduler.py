#!/usr/bin/env python3
# scheduler.py

import json, time, subprocess
from datetime import datetime, timezone
from threading import Thread
from pathlib import Path

# Ścieżki w repozytorium
REPO_DIR      = Path(__file__).parent
LISTINGS_FILE = REPO_DIR / "listings.json"
CURRENT_FILE  = REPO_DIR / "current_listing.json"
BOT_SCRIPT    = REPO_DIR / "bot.py"

# Funkcja parsująca ISO8601+offset do UTC
def parse_dt(iso_str: str) -> datetime:
    return datetime.fromisoformat(iso_str).astimezone(timezone.utc)

# Uruchomienie bota dla pojedynczego listingu def run_bot_for(listing: dict):
    # Nadpisz current_listing.json, aby bot.py wczytał właściwy rekord
    with open(CURRENT_FILE, "w") as f:
        json.dump(listing, f, indent=2)
    print(f"[SCHED] Uruchamiam bot dla {listing['symbol']} @ {listing['listing_time']}")
    subprocess.Popen(["python3", str(BOT_SCRIPT)])

# Harmonogram dla wszystkich listingów
def schedule_listings():
    if not LISTINGS_FILE.exists():
        print(f"⚠️ Brak {LISTINGS_FILE}")
        return
    with open(LISTINGS_FILE) as f:
        all_listings = json.load(f)

    now = datetime.now(timezone.utc)
    for listing in all_listings:
        # oblicz timestamp UTC listingu
        dt_utc = parse_dt(listing["listing_time"])
        # ile sekund do startu minus 10s wstępnie
        delay = (dt_utc - now).total_seconds() - 10
        if delay < 0:
            delay = 0
        print(f"[SCHED] {listing['symbol']} uruchomię za {delay:.1f}s")
        Thread(target=lambda lst=listing: (time.sleep(delay), run_bot_for(lst))).start()

if __name__ == "__main__":
    print("[SCHED] Scheduler wystartował")
    schedule_listings()
    # utrzymaj działanie procesu
    try:
        while True:
            time.sleep(60)
    except KeyboardInterrupt:
        print("[SCHED] Zatrzymano")
