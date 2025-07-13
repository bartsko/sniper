#!/bin/bash

# === KONFIGURACJA ===
BOT_DIR="/root/sniper"
REPO_URL="https://github.com/bartsko/sniper.git"
SERVICE_FILE="sniper-backend.service"
SERVICE_PATH="/etc/systemd/system/sniper-backend.service"

set -e

# === KROK 1: aktualizacja systemu ===
echo "🔧 Aktualizuję system..."
sudo apt update && sudo apt upgrade -y

# === KROK 2: instalacja Pythona, Git i crona ===
echo "🐍 Instaluję Pythona, Git i Cron..."
sudo apt install -y python3 python3-pip git cron

# === KROK 3: uruchomienie i aktywacja crona ===
echo "🕓 Upewniam się, że cron działa..."
sudo systemctl enable cron
sudo systemctl start cron

# === KROK 4: klonowanie repo ===
echo "📦 Klonuję repozytorium..."
mkdir -p "$BOT_DIR"
cd "$BOT_DIR"
git clone "$REPO_URL" .

# === KROK 5: instalacja zależności Pythona ===
echo "📚 Instaluję zależności..."
pip3 install -r requirements.txt

# === KROK 6: kopiowanie pliku serwisowego ===
echo "🛠️ Konfiguruję usługę backendu jako systemd..."
sudo cp "$BOT_DIR/$SERVICE_FILE" "$SERVICE_PATH"

# === KROK 7: reload + enable + start usługi ===
sudo systemctl daemon-reload
sudo systemctl enable sniper-backend.service
sudo systemctl restart sniper-backend.service

echo "✅ Instalacja zakończona. Usługa backend działa jako systemd."
echo "➡️ Sprawdź status: sudo systemctl status sniper-backend.service"
