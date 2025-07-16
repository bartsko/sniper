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
apt install -y git wget python3 python3-pip libsqlite3-dev php php-curl

# —————————————————————————————————————————————
# 2) Usuń poprzednią instalację (jeśli była)
rm -rf "$BOT_DIR"

# —————————————————————————————————————————————
# 3) Klon repo i instalacja zależności
git clone "$REPO_URL" "$BOT_DIR"
cd "$BOT_DIR"
pip3 install --upgrade pip
pip3 install -r requirements.txt

# —————————————————————————————————————————————
# 4) Ustawiamy uprawnienia na bota PHP
chmod +x "$BOT_DIR/mexc_sniper.php"

# —————————————————————————————————————————————
# —————————————————————————————————————————————
# 5) Tworzymy i włączamy usługę systemd dla FastAPI
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
# 5.5) Otwieramy port 8000 w firewallu (UFW)
if command -v ufw >/dev/null 2>&1; then
  ufw allow 8000/tcp || true
  ufw reload || true
fi

# —————————————————————————————————————————————
# 6) Podsumowanie
echo
echo "✅ FastAPI działa jako systemd:"
echo "   sudo systemctl status sniper-backend.service"
echo "   curl http://localhost:8000/status"
echo
echo "✅ Bot PHP gotowy tutaj: $BOT_DIR/mexc_sniper.php"
echo "   php $BOT_DIR/mexc_sniper.php --help"
