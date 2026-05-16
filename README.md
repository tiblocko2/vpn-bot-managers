# VPN Bot — Multi-Manager Edition

Telegram-бот для управления VPN-клиентами 3X-UI с поддержкой менеджеров.

## Возможности

- **Суперпользователь (admin)**: полный доступ — управление inbound, импорт из 3X-UI, смена домена подписок, управление менеджерами и их сроками
- **Менеджеры**: каждый менеджер управляет только своими клиентами (до 6 штук), добавляет их в inbound и получает ссылки. Срок подписки контролирует суперпользователь — при установке срока все клиенты менеджера обновляются в 3X-UI

## Установка на сервере Ubuntu/Debian

```bash
git clone https://github.com/tiblocko2/vpn-bot-managers.git
cd vpn-bot-managers
sudo bash install.sh
```

Скрипт интерактивно спросит все необходимые параметры и установит бота как systemd-сервис.
Бинарный файл автоматически загружается из последнего GitHub Release.

### Управление сервисом

```bash
systemctl status vpn-bot      # статус
journalctl -u vpn-bot -f      # логи в реальном времени
systemctl restart vpn-bot     # перезапуск
```

## Обновление

```bash
ARCH=$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')
curl -fsSL https://github.com/tiblocko2/vpn-bot-managers/releases/latest/download/vpn-bot-linux-$ARCH -o /opt/vpn-bot/vpn-bot
chmod +x /opt/vpn-bot/vpn-bot
systemctl restart vpn-bot
```

## Конфигурация

Файл: `/opt/vpn-bot/config.json` (шаблон: [`config.example.json`](config.example.json))

| Параметр | Описание |
|---|---|
| `bot_token` | Токен Telegram-бота (@BotFather) |
| `super_user_id` | Telegram ID суперпользователя |
| `panel_url` | URL панели 3X-UI без слеша в конце |
| `panel_username` / `panel_password` | Логин и пароль от панели |
| `sub_domain` | Базовый URL для ссылок на подписки |
| `proxy_url` | Прокси для Telegram API (необязательно) |
| `db_path` | Путь к SQLite-базе данных |

Inbound добавляются прямо через бота (кнопка "⚙️ Управление inbound" в меню суперпользователя).

## Роли

### Суперпользователь
- Добавление клиентов (без лимита)
- Список всех клиентов
- Импорт из 3X-UI
- Управление inbound
- Смена домена подписок
- Управление менеджерами: добавление, удаление, установка срока подписки

### Менеджер
- Добавление клиентов (до 6 штук)
- Просмотр и управление только своими клиентами
- Подключение клиентов к inbound, получение ссылок на подписки, удаление
- Клиенты создаются с `expiryTime` из срока подписки менеджера

## Структура проекта

```
vpn-bot/
├── cmd/bot/           # точка входа (main.go)
├── internal/
│   ├── config/        # загрузка и сохранение конфига
│   ├── db/            # работа с SQLite
│   ├── panel/         # API-клиент 3X-UI
│   └── bot/           # Telegram-бот (bot, views, callbacks)
├── .github/workflows/ # CI/CD: сборка релизов при пуше тега
├── config.example.json
└── install.sh
```

## Сборка из исходников

```bash
# Linux amd64
GOOS=linux GOARCH=amd64 go build -o vpn-bot ./cmd/bot/

# Linux arm64
GOOS=linux GOARCH=arm64 go build -o vpn-bot ./cmd/bot/
```
