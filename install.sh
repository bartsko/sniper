#!/bin/bash
set -e

# === 0) Ścieżki i zmienne ===
BOT_DIR="/root/sniper"
REPO_URL="https://github.com/bartsko/sniper.git"
SERVICE_PATH="/etc/systemd/system/sniper-backend.service"

# === 1) Zainstaluj zależności systemowe ===
apt update && apt upgrade -y
apt install -y git python3 python3-pip golang-go libsqlite3-dev

# === 2) Usuń stary katalog (jeśli przypadkiem jeszcze był) ===
rm -rf "$BOT_DIR"

# === 3) Sklonuj repozytorium ===
git clone "$REPO_URL" "$BOT_DIR"
cd "$BOT_DIR"

# === 4) Python: zainstaluj zależności ===
pip3 install --upgrade pip
pip3 install -r requirements.txt

# === 5) Go: zbuduj bota ===
cd bot-go
go mod tidy
GOOS=linux GOARCH=amd64 go build -o ../sniper-bot
cd "$BOT_DIR"

# === 6) Skonfiguruj i uruchom FastAPI jako systemd ===
cat <<EOF > /tmp/sniper-backend.service
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

mv /tmp/sniper-backend.service "$SERVICE_PATH"
chmod 644 "$SERVICE_PATH"

systemctl daemon-reload
systemctl enable sniper-backend.service
systemctl start  sniper-backend.service

# === 7) Podsumowanie ===
echo "✅ Backend FastAPI wystartował jako systemd."
echo "   ➜ Sprawdź: sudo systemctl status sniper-backend.service"
echo "   ➜ Test lokalny: curl http://localhost:8000/status"
echo ""
echo "✅ Bot Go zbudowany: $BOT_DIR/sniper-bot"
echo "   ➜ Możesz wywołać: $BOT_DIR/sniper-bot --help"
