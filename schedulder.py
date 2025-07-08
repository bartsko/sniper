# Folder: scheduler/

import argparse
from datetime import datetime
import pytz
import os
import subprocess

# === Funkcja do dodania zadania cron ===
def add_cron_job(command, run_time):
    cron_time = run_time.strftime('%M %H %d %m *')
    cron_command = f"{cron_time} {command}"
    existing_crontab = subprocess.getoutput("crontab -l || true")

    if cron_command in existing_crontab:
        print("🟡 Zadanie już istnieje w cronie.")
        return

    new_crontab = existing_crontab + f"\n{cron_command}\n"
    process = subprocess.Popen(['crontab'], stdin=subprocess.PIPE)
    process.communicate(input=new_crontab.encode())
    print(f"✅ Dodano zadanie do crona: {cron_command}")


# === Funkcja główna ===
def schedule_task(exchange, symbol, time_str, timezone_str, amount, roi):
    tz = pytz.timezone(timezone_str)
    local_time = tz.localize(datetime.strptime(time_str, "%Y-%m-%d %H:%M:%S"))
    system_time = local_time.astimezone(pytz.timezone('Etc/UTC')).astimezone()

    print(f"📅 Lokalna data (użytkownika): {local_time}")
    print(f"🖥 Czas VPS: {system_time}")

    command = f"/usr/bin/python3 $HOME/sniper/{exchange}/bot.py --symbol '{symbol}' --amount {amount} --roi {roi}"
    add_cron_job(command, system_time)


# === Argumenty CLI ===
if __name__ == "__main__":
    parser = argparse.ArgumentParser(description="Scheduler snajper bota")
    parser.add_argument("--exchange", required=True, help="Nazwa giełdy (mexc, binance, itp.)")
    parser.add_argument("--symbol", required=True)
    parser.add_argument("--time", required=True, help="Data i czas (np. '2025-07-09 11:00:00')")
    parser.add_argument("--timezone", required=True, help="Strefa czasowa (np. Europe/Warsaw)")
    parser.add_argument("--amount", type=float, required=True)
    parser.add_argument("--roi", type=float, required=True)

    args = parser.parse_args()
    schedule_task(args.exchange, args.symbol, args.time, args.timezone, args.amount, args.roi)
