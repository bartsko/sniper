#!/bin/bash
set -e

# === ZMIENNE ===
BOT_DIR="/root/sniper"
REPO_URL="https://github.com/bartsko/sniper.git"
SERVICE_PATH="/etc/systemd/system/sniper-backend.service"

# === 1. System: aktualizacja + zależności ===
apt update && apt upgrade -y
apt install -y git python3 python3-pip libsqlite3-dev wget tar

# === 2. Usuń starą instalację ===
rm -rf "$BOT_DIR"

# === 3. Zainstaluj Go 1.21 ręcznie ===
cd /tmp
wget https://go.dev/dl/go1.21.2.linux-amd64.tar.gz
rm -rf /usr/local/go
tar -C /usr/local -xzf go1.21.2.linux-amd64.tar.gz
export PATH=/usr/local/go/bin:$PATH
echo 'export PATH=/usr/local/go/bin:$PATH' >> /etc/profile

# === 4. Sklonuj repozytorium ===
git clone "$REPO_URL" "$BOT_DIR"
cd "$BOT_DIR"

# === 5. Python: zainstaluj deps ===
pip3 install --upgrade pip
pip3 install -r requirements.txt

# === 6. Go: zbuduj bota ===
cd bot-go
go mod tidy        # teraz działa bo masz Go 1.21
GOOS=linux GOARCH=amd64 go build -o ../sniper-bot bot.go
cd "$BOT_DIR"

# === 7. Utwórz plik systemd i włącz serwis ===
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

# === 8. Podsumowanie ===
echo
echo "✅ Backend FastAPI startuje jako systemd:"
echo "   sudo systemctl status sniper-backend.service"
echo "   curl http://localhost:8000/status"
echo
echo "✅ Bot Go tu: $BOT_DIR/sniper-bot"
echo "   $BOT_DIR/sniper-bot --help"
