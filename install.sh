#!/bin/bash
set -e

# —————————————————————————————————————————————
# 0) Zmienne
BOT_DIR="/root/sniper"
REPO_URL="https://github.com/bartsko/sniper.git"
SERVICE_PATH="/etc/systemd/system/sniper-backend.service"

# —————————————————————————————————————————————
# 1) System: update + niezbędne pakiety
apt update && apt upgrade -y
apt install -y git wget tar python3 python3-pip libsqlite3-dev

# —————————————————————————————————————————————
# 2) Usuń poprzednią instalację (jeśli była)
rm -rf "$BOT_DIR"

# —————————————————————————————————————————————
# 3) Ręczna instalacja Go 1.21
cd /tmp
wget https://go.dev/dl/go1.21.2.linux-amd64.tar.gz
rm -rf /usr/local/go
tar -C /usr/local -xzf go1.21.2.linux-amd64.tar.gz
# dodajemy go do ścieżki natychmiast dla tego skryptu
export PATH="/usr/local/go/bin:$PATH"

# —————————————————————————————————————————————
# 4) Klon repo i python deps
git clone "$REPO_URL" "$BOT_DIR"
cd "$BOT_DIR"
pip3 install --upgrade pip
pip3 install -r requirements.txt

# —————————————————————————————————————————————
# 5) Budujemy bota w Go
cd bot-go
go mod tidy
GOOS=linux GOARCH=amd64 go build -o ../sniper-bot bot.go
cd "$BOT_DIR"

# —————————————————————————————————————————————
# 6) Tworzymy i włączamy usługę systemd dla FastAPI
cat > /tmp/sniper-backend.service <<EOF
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
systemctl restart sniper-backend.service

# —————————————————————————————————————————————
# 7) Podsumowanie
echo
echo "✅ FastAPI działa jako systemd:"
echo "   sudo systemctl status sniper-backend.service"
echo "   curl http://localhost:8000/status"
echo
echo "✅ Bot Go skompilowany tutaj: $BOT_DIR/sniper-bot"
echo "   $BOT_DIR/sniper-bot --help"
