#!/bin/bash
# VPN Bot — интерактивный установщик для Ubuntu/Debian
# Использование: sudo bash install.sh
set -e

INSTALL_DIR="/opt/vpn-bot"
SERVICE_NAME="vpn-bot"
REPO="tiblocko2/vpn-bot-managers"
BINARY="vpn-bot"

RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; NC='\033[0m'
info()  { echo -e "${GREEN}$1${NC}"; }
warn()  { echo -e "${YELLOW}$1${NC}"; }
error() { echo -e "${RED}$1${NC}"; exit 1; }

[ "$EUID" -ne 0 ] && error "Запустите скрипт с правами root: sudo bash install.sh"

# --- Download binary from GitHub Releases ---

ARCH=$(uname -m)
case "$ARCH" in
    x86_64)  BIN_ARCH="amd64" ;;
    aarch64) BIN_ARCH="arm64" ;;
    *) error "Неподдерживаемая архитектура: $ARCH" ;;
esac

mkdir -p "$INSTALL_DIR"

# Use local binary if present (for dev/testing), otherwise download from release
if [ -f "./$BINARY" ]; then
    warn "⚠️ Найден локальный бинарный файл — использую его."
    cp "./$BINARY" "$INSTALL_DIR/$BINARY"
else
    DOWNLOAD_URL="https://github.com/$REPO/releases/latest/download/vpn-bot-linux-$BIN_ARCH"
    info "📥 Загрузка vpn-bot (linux/$BIN_ARCH) из $DOWNLOAD_URL ..."
    if ! curl -fsSL "$DOWNLOAD_URL" -o "$INSTALL_DIR/$BINARY"; then
        error "Не удалось загрузить бинарный файл. Проверьте наличие релиза на GitHub."
    fi
fi
chmod +x "$INSTALL_DIR/$BINARY"

# --- Interactive configuration ---

echo ""
info "=== Настройка VPN Bot ==="
echo ""

read -p "Telegram Bot Token: " BOT_TOKEN
[ -z "$BOT_TOKEN" ] && error "Bot Token не может быть пустым"

read -p "Ваш Telegram ID (SuperUser): " SUPER_USER_ID
[[ ! "$SUPER_USER_ID" =~ ^[0-9]+$ ]] && error "Telegram ID должен быть числом"

read -p "URL панели 3X-UI (например: https://srv.example.com:808): " PANEL_URL
[ -z "$PANEL_URL" ] && error "URL панели не может быть пустым"

read -p "Логин панели: " PANEL_USERNAME
[ -z "$PANEL_USERNAME" ] && error "Логин не может быть пустым"

read -s -p "Пароль панели: " PANEL_PASSWORD
echo ""
[ -z "$PANEL_PASSWORD" ] && error "Пароль не может быть пустым"

read -p "Домен для подписок (например: https://srv.example.com:2096/sub): " SUB_DOMAIN
[ -z "$SUB_DOMAIN" ] && error "Домен подписок не может быть пустым"

read -p "ID VLESS inbound [по умолчанию: 1]: " VLESS_ID
VLESS_ID="${VLESS_ID:-1}"

read -p "ID VMess inbound [по умолчанию: 5]: " VMESS_ID
VMESS_ID="${VMESS_ID:-5}"

read -p "Прокси URL (оставьте пустым если не нужен): " PROXY_URL

DB_PATH="$INSTALL_DIR/db.sqlite"

# --- Write config ---

info "\n📝 Создание $INSTALL_DIR/config.json..."
cat > "$INSTALL_DIR/config.json" <<EOF
{
  "bot_token": "$BOT_TOKEN",
  "super_user_id": $SUPER_USER_ID,
  "panel_url": "$PANEL_URL",
  "panel_username": "$PANEL_USERNAME",
  "panel_password": "$PANEL_PASSWORD",
  "sub_domain": "$SUB_DOMAIN",
  "proxy_url": "$PROXY_URL",
  "vless_inbound_id": $VLESS_ID,
  "vmess_inbound_id": $VMESS_ID,
  "db_path": "$DB_PATH"
}
EOF
chmod 600 "$INSTALL_DIR/config.json"

# Migrate existing database if present
if [ -f "./db.sqlite" ] && [ ! -f "$DB_PATH" ]; then
    warn "📦 Найдена существующая db.sqlite — копирую в $INSTALL_DIR/"
    cp "./db.sqlite" "$DB_PATH"
fi

# --- Systemd service ---

info "⚙️ Создание systemd-сервиса $SERVICE_NAME..."
cat > "/etc/systemd/system/$SERVICE_NAME.service" <<EOF
[Unit]
Description=VPN Telegram Bot
After=network.target

[Service]
Type=simple
User=root
WorkingDirectory=$INSTALL_DIR
ExecStart=$INSTALL_DIR/$BINARY
Restart=always
RestartSec=5
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable "$SERVICE_NAME"
systemctl restart "$SERVICE_NAME"

echo ""
info "✅ Установка завершена!"
echo ""
echo "  Статус:  systemctl status $SERVICE_NAME"
echo "  Логи:    journalctl -u $SERVICE_NAME -f"
echo "  Конфиг:  $INSTALL_DIR/config.json"
echo ""
warn "Домен подписок можно сменить прямо через бота (кнопка в меню суперпользователя)."
