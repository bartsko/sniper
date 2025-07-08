#!/bin/bash

# === KONFIGURACJA ===
BOT_DIR="$HOME/sniper"
REPO_URL="https://github.com/bartsko/sniper.git"

# === KROK 1: aktualizacja systemu ===
echo "🔧 Aktualizuję system..."
sudo apt update && sudo apt upgrade -y

# === KROK 2: instalacja Pythona i Git ===
echo "🐍 Instaluję Pythona i Git..."
sudo apt install -y python3 python3-pip git

# === KROK 3: klonowanie repo ===
echo "📦 Klonuję repozytorium..."
mkdir -p "$BOT_DIR"
cd "$BOT_DIR"
git clone "$REPO_URL" .

# === KROK 4: instalacja zależności Pythona ===
echo "📚 Instaluję zależności..."
pip3 install -r requirements.txt

# === KROK 5: zakończenie ===
echo "✅ Instalacja zakończona. Boty dostępne w: $BOT_DIR"
echo "📌 Użyj scheduler.py, aby zaplanować snajp na listingu."

echo "💡 Przykład:"
echo "python3 scheduler/scheduler.py --exchange mexc --symbol XYZ/USDT --time '2025-07-09 11:00:00' --timezone Europe/Warsaw --amount 20 --roi 10"
