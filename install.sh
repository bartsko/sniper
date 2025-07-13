#!/bin/bash

set -e

# === KONFIGURACJA ===
BOT_DIR="/root/sniper"
REPO_URL="https://github.com/bartsko/sniper.git"
SERVICE_PATH="/etc/systemd/system/sniper-backend.service"

# === KROK 0: usuń poprzednią instalkę (opcjonalnie) ===
if [ -d "$BOT_DIR" ]; then
  echo "⚠️  Uwaga: katalog $BOT_DIR już istnieje. Usuwam dla czystej instalacji..."
  rm -rf "$BOT_DIR"
fi

# === KROK 1: aktualizacja systemu ===
echo "🔧 Aktualizuję system..."
sudo apt update && sudo apt upgrade -y

# === KROK 1a: brakująca zależność (sqlite3) ===
sudo apt install -y libsqlite3-dev

# === KROK 2: instalacja Pythona, Git, crona, net-tools (netstat do debugowania) ===
echo "🐍 Instaluję Pythona, Git i Cron..."
sudo apt install -y python3 python3-pip git cron net-tools

# === KROK 3: uruchomienie i aktywacja crona ===
echo "🕓 Upewniam się, że cron działa..."
sudo systemctl enable cron
sudo systemctl start cron

# === KROK 4: klonowanie repo ===
echo "📦 Klonuję repozytorium..."
mkdir -p "$BOT_DIR"
git clone "$REPO_URL" "$BOT_DIR"
cd "$BOT_DIR"

# === KROK 5: instalacja zależności Pythona ===
echo "📚 Instaluję zależności..."
pip3 install -r requirements.txt

# === KROK 6: generuj dynamicznie plik systemd (uruchomienie przez uvicorn) ===
echo "🛠️ Konfiguruję usługę backendu jako systemd..."
cat <<EOF | sudo tee $SERVICE_PATH > /dev/null
[Unit]
Description=Sniper Backend FastAPI Server
After=network.target

[Service]
Type=simple
User=root
WorkingDirectory=$BOT_DIR
ExecStart=$(which python3) -m uvicorn server:app --host 0.0.0.0 --port 8000
Restart=always
RestartSec=3
Environment=PYTHONUNBUFFERED=1

[Install]
WantedBy=multi-user.target
EOF

# === KROK 7: reload + enable + start usługi ===
sudo systemctl daemon-reload
sudo systemctl enable sniper-backend.service
sudo systemctl restart sniper-backend.service

echo "✅ Instalacja zakończona. Usługa backend działa jako systemd."
echo "➡️ Sprawdź status: sudo systemctl status sniper-backend.service"
echo "💡 Test lokalny: curl http://localhost:8000/listings"
echo "🌍 Test zdalny: curl http://TWOJE_IP_VPS:8000/listings"

cd "$BOT_DIR"
