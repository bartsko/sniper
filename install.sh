#!/bin/bash
set -e

BOT_DIR="/root/sniper"
REPO_URL="https://github.com/bartsko/sniper.git"
SERVICE_PATH="/etc/systemd/system/sniper-backend.service"

# czysta instalacja
sudo rm -rf "$BOT_DIR"

# system + Go + Python
sudo apt update && sudo apt upgrade -y
sudo apt install -y golang-go git python3 python3-pip libsqlite3-dev

# repo
git clone "$REPO_URL" "$BOT_DIR"
cd "$BOT_DIR"

# Python deps
pip3 install -r requirements.txt

# Go compile
cd bot
go mod tidy
go build -o sniper-bot
mv sniper-bot ..

# systemd dla FastAPI
cd "$BOT_DIR"
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

sudo systemctl daemon-reload
sudo systemctl enable sniper-backend.service
sudo systemctl restart sniper-backend.service

echo "✅ Backend działa pod FastAPI."
echo "➡️  curl http://localhost:8000/status"

echo "✔️ Bot Go skompilowany jako $BOT_DIR/sniper-bot"
