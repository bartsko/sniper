import json
import time
import subprocess
from datetime import datetime, timezone
from threading import Thread

BOT_PATH = "./bot.py"  # Ścieżka do Twojego bota

def run_bot(listing):
    # Zapisz pojedynczy listing jako listings.json (tymczasowo, bo bot tego oczekuje)
    with open("listings.json", "w") as f:
        json.dump(listing, f)
    print(f"[SCHEDULER] Odpalam bota dla {listing['symbol']} o {listing['listing_time']}")
    subprocess.Popen(["python3", BOT_PATH])

def parse_dt(dt_str):
    return datetime.fromisoformat(dt_str).astimezone(timezone.utc)

def schedule_all():
    with open("listings.json") as f:
        listings = json.load(f)
    now = datetime.now(timezone.utc)

    for listing in listings:
        lt_utc = parse_dt(listing["listing_time"])
        seconds_until = (lt_utc - now).total_seconds() - 10  # uruchom 10 sekund przed listingiem
        seconds_until = max(0, seconds_until)
        print(f"[SCHEDULER] {listing['symbol']} odpali się za {seconds_until:.1f} sek.")
        Thread(target=lambda: (time.sleep(seconds_until), run_bot(listing))).start()

if __name__ == "__main__":
    schedule_all()
    while True:
        time.sleep(60)  # Trzymaj scheduler przy życiu, aż wszystkie wątki się wykonają
