#!/bin/bash
set -e

BOT_DIR="/root/sniper"
REPO_URL="https://github.com/bartsko/sniper.git"
SERVICE_PATH="/etc/systemd/system/sniper-backend.service"

# 0) usuń poprzednią instalację
sudo rm -rf "$BOT_DIR"

# 1) zainstaluj Go, Pythona i zależności systemowe
sudo apt update && sudo apt upgrade -y
sudo apt install -y golang-go python3 python3-pip git libsqlite3-dev

# 2) sklonuj repo i zbuduj wszystko
git clone "$REPO_URL" "$BOT_DIR"
cd "$BOT_DIR"

# 2a) Python deps
pip3 install -r requirements.txt

# 2b) Go deps i build bota
cd bot-go
go mod tidy
GOOS=linux GOARCH=amd64 go build -o ../sniper-bot bot.go

# 3) ustaw service dla FastAPI
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
echo "✔️ Bot Go skompilowany: $BOT_DIR/sniper-bot"
