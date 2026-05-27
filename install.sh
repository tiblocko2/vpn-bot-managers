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

echo ""
echo "API-токен панели (необязательно)."
echo "Позволяет работать без логина/пароля. Создаётся в настройках 3X-UI → API."
read -p "API Token 3X-UI (оставьте пустым, если не используете): " PANEL_API_TOKEN

read -p "Домен для подписок (например: https://srv.example.com:2096/sub): " SUB_DOMAIN
[ -z "$SUB_DOMAIN" ] && error "Домен подписок не может быть пустым"

read -p "Прокси URL (оставьте пустым если не нужен): " PROXY_URL

# --- Inbound configuration ---

echo ""
info "Настройка inbound-ов 3X-UI."
echo "Добавьте все inbound, которые должны получать клиентов."
echo "Для каждого введите ID (число) и метку (например: VLESS, VMess, Reality)."
echo "Нажмите Enter с пустым ID, чтобы закончить ввод."
echo ""

INBOUNDS_JSON=""
INBOUND_COUNT=0

while true; do
    read -p "ID inbound (или Enter для завершения): " IB_ID
    [ -z "$IB_ID" ] && break
    [[ ! "$IB_ID" =~ ^[0-9]+$ ]] && warn "ID должен быть числом. Попробуйте снова." && continue

    read -p "Метка для inbound $IB_ID (например: VLESS): " IB_LABEL
    [ -z "$IB_LABEL" ] && IB_LABEL="Inbound $IB_ID"

    if [ -n "$INBOUNDS_JSON" ]; then
        INBOUNDS_JSON="$INBOUNDS_JSON,"
    fi
    INBOUNDS_JSON="$INBOUNDS_JSON
    {\"id\": $IB_ID, \"label\": \"$IB_LABEL\"}"
    INBOUND_COUNT=$((INBOUND_COUNT + 1))
    info "  ✅ Добавлен: [$IB_ID] $IB_LABEL"
done

if [ "$INBOUND_COUNT" -eq 0 ]; then
    warn "⚠️ Inbound-ы не добавлены. Можно добавить через бота позже."
    INBOUNDS_JSON=""
fi

DB_PATH="$INSTALL_DIR/db.sqlite"

# --- Write config ---

info "\n📝 Создание $INSTALL_DIR/config.json..."

# Build optional api token field
API_TOKEN_FIELD=""
if [ -n "$PANEL_API_TOKEN" ]; then
    API_TOKEN_FIELD="\"panel_api_token\": \"$PANEL_API_TOKEN\","
fi

cat > "$INSTALL_DIR/config.json" <<EOF
{
  "bot_token": "$BOT_TOKEN",
  "super_user_id": $SUPER_USER_ID,
  "panel_url": "$PANEL_URL",
  "panel_username": "$PANEL_USERNAME",
  "panel_password": "$PANEL_PASSWORD",
  $API_TOKEN_FIELD
  "sub_domain": "$SUB_DOMAIN",
  "proxy_url": "$PROXY_URL",
  "inbounds": [$INBOUNDS_JSON
  ],
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
warn "Домен подписок и inbound-ы можно изменить прямо через бота (меню суперпользователя)."
