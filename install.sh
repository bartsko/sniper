#!/bin/bash
set -e

# === KONFIGURACJA ===
BOT_DIR="/root/sniper"
REPO_URL="https://github.com/bartsko/sniper.git"
SERVICE_PATH="/etc/systemd/system/sniper-backend.service"

# === KROK 0: USUŃ WSZYSTKO Z POPRZEDNIEJ INSTALACJI ===
sudo systemctl stop sniper-backend.service  || true
sudo systemctl disable sniper-backend.service || true
sudo rm -f $SERVICE_PATH
sudo systemctl daemon-reload
sudo rm -rf "$BOT_DIR"

# === KROK 1: ZAINSTALUJ DEPENDENCJE SYSTEMOWE ===
sudo apt update && sudo apt upgrade -y
sudo apt install -y python3 python3-pip git cron libsqlite3-dev golang-go

# === KROK 2: Sklonuj repozytorium i zbuduj aplikacje ===
git clone "$REPO_URL" "$BOT_DIR"
cd "$BOT_DIR"

# 2a) Pythona: instalacja bibliotek
pip3 install -r requirements.txt

# 2b) Go: kompilacja bota
cd bot-go
go mod tidy
GOOS=linux GOARCH=amd64 go build -o ../sniper-bot bot.go

# === KROK 3: Utwórz i uruchom usługę FastAPI przez systemd ===
cd "$BOT_DIR"
sudo tee "$SERVICE_PATH" > /dev/null <<EOF
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

sudo systemctl daemon-reload
sudo systemctl enable sniper-backend.service
sudo systemctl restart sniper-backend.service

echo "✅ Backend uruchomiony jako systemd."
echo "➡️  curl http://localhost:8000/status"
echo "✔️ Bot Go skompilowany: $BOT_DIR/sniper-bot"
