#!/bin/bash

# === KONFIGURACJA ===
BOT_DIR="$HOME/sniper"
REPO_URL="https://github.com/bartsko/sniper.git"

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

# === KROK 6: zakończenie ===
echo "✅ Instalacja zakończona. Boty dostępne w: $BOT_DIR"
echo "📌 Użyj scheduler.py, aby zaplanować snajp na listingu."

echo "💡 Przykład:"
echo "python3 scheduler/scheduler.py --exchange mexc --symbol XYZ/USDT --time '2025-07-09 11:00:00' --timezone Europe/Warsaw --amount 20 --roi 10"
