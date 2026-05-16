package bot

import (
	"crypto/tls"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"golang.org/x/net/proxy"

	"vpn-bot/internal/config"
	"vpn-bot/internal/db"
	"vpn-bot/internal/panel"
)

// Bot wraps the Telegram API client and holds per-user conversation state.
type Bot struct {
	api             *tgbotapi.BotAPI
	userState       map[int64]string
	importSelection map[int64]map[int64]bool // userID → inboundID → selected
}

func New() (*Bot, error) {
	httpClient, err := buildHTTPClient()
	if err != nil {
		return nil, fmt.Errorf("ошибка создания HTTP-клиента: %v", err)
	}
	api, err := tgbotapi.NewBotAPIWithClient(config.Cfg.BotToken, tgbotapi.APIEndpoint, httpClient)
	if err != nil {
		return nil, err
	}
	api.Debug = false
	log.Printf("✅ Бот запущен, username: @%s", api.Self.UserName)
	return &Bot{
		api:             api,
		userState:       make(map[int64]string),
		importSelection: make(map[int64]map[int64]bool),
	}, nil
}

func (b *Bot) Run() {
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	for update := range b.api.GetUpdatesChan(u) {
		if update.CallbackQuery != nil {
			b.handleCallback(&update)
			continue
		}
		if update.Message != nil {
			b.handleMessage(&update)
		}
	}
}

func (b *Bot) send(userID int64, text string) {
	b.api.Send(tgbotapi.NewMessage(userID, text))
}

func (b *Bot) handleMessage(update *tgbotapi.Update) {
	userID := update.Message.From.ID

	// Forwarded message as a shortcut for operator registration.
	// ForwardDate != 0 means it's a forward regardless of sender privacy settings.
	if update.Message.ForwardDate != 0 && b.userState[userID] == "waiting_op_id" {
		if userID != config.Cfg.SuperUserID {
			delete(b.userState, userID)
			return
		}
		if update.Message.ForwardFrom == nil {
			// User has "Forwarding" privacy enabled — ID is hidden.
			name := update.Message.ForwardSenderName
			if name == "" {
				name = "неизвестный"
			}
			b.send(userID, fmt.Sprintf(
				"❌ Не удалось получить ID пользователя %q: включена приватность пересылок.\n\nПопросите его узнать свой ID через @userinfobot и введите вручную.",
				name,
			))
			return
		}
		fwd := update.Message.ForwardFrom
		label := formatUsername(fwd.FirstName, fwd.UserName)
		if err := db.AddOperator(fwd.ID, label); err != nil {
			b.send(userID, "❌ Ошибка добавления оператора")
		} else {
			b.send(userID, fmt.Sprintf("✅ Менеджер %s (ID: %d) добавлен", label, fwd.ID))
		}
		delete(b.userState, userID)
		return
	}

	if !db.IsOperator(userID) {
		b.send(userID, "❌ У вас нет доступа к боту")
		return
	}

	if update.Message.Text == "/start" {
		b.showMenu(userID)
		return
	}

	b.handleTextState(userID, update.Message.Text)
}

func (b *Bot) handleTextState(userID int64, text string) {
	switch b.userState[userID] {
	case "waiting_name":
		name := strings.TrimSpace(text)
		if name == "" {
			b.send(userID, "❌ Имя не может быть пустым. Введите Фамилию и Имя.")
			return
		}
		if db.ClientExists(name) {
			b.send(userID, fmt.Sprintf("❌ Клиент '%s' уже существует.", name))
			return
		}
		if userID != config.Cfg.SuperUserID {
			limit := db.GetOperatorMaxClients(userID)
			if db.GetClientCountByOwner(userID) >= limit {
				b.send(userID, fmt.Sprintf("❌ Достигнут лимит: вы можете добавить не более %d клиентов", limit))
				delete(b.userState, userID)
				return
			}
		}
		link, err := panel.AddClient(name, userID)
		if err != nil {
			b.send(userID, "❌ Ошибка добавления: "+err.Error())
			log.Printf("Ошибка добавления клиента: %v", err)
		} else {
			msg := tgbotapi.NewMessage(userID, fmt.Sprintf(
				"✅ Пользователь '%s' успешно добавлен!\n\n🔗 Ссылка на подписку:\n%s", name, link,
			))
			msg.DisableWebPagePreview = true
			b.api.Send(msg)
		}
		delete(b.userState, userID)

	case "waiting_op_id":
		var opID int64
		if _, err := fmt.Sscan(strings.TrimSpace(text), &opID); err != nil {
			b.send(userID, "❌ Неверный формат ID. Введите число.")
			return
		}
		if err := db.AddOperator(opID, ""); err != nil {
			b.send(userID, "❌ Ошибка добавления оператора")
		} else {
			b.send(userID, fmt.Sprintf("✅ Менеджер с ID %d добавлен", opID))
		}
		delete(b.userState, userID)

	case "waiting_domain":
		if userID != config.Cfg.SuperUserID {
			b.send(userID, "❌ Нет доступа")
			delete(b.userState, userID)
			return
		}
		newDomain := strings.TrimRight(strings.TrimSpace(text), "/")
		if !strings.HasPrefix(newDomain, "http") {
			b.send(userID, "❌ Домен должен начинаться с http:// или https://")
			return
		}
		if err := config.SetSubDomain(newDomain); err != nil {
			b.send(userID, "❌ Ошибка сохранения: "+err.Error())
		} else {
			b.send(userID, fmt.Sprintf("✅ Домен подписок изменён на:\n%s", newDomain))
		}
		delete(b.userState, userID)

	default:
		if strings.HasPrefix(b.userState[userID], "waiting_label:") {
			if userID != config.Cfg.SuperUserID {
				delete(b.userState, userID)
				return
			}
			opIDStr := strings.TrimPrefix(b.userState[userID], "waiting_label:")
			opID, err := strconv.ParseInt(opIDStr, 10, 64)
			if err != nil {
				b.send(userID, "❌ Внутренняя ошибка")
				delete(b.userState, userID)
				return
			}
			label := strings.TrimSpace(text)
			if err := db.SetOperatorLabel(opID, label); err != nil {
				b.send(userID, "❌ Ошибка сохранения: "+err.Error())
			} else {
				b.send(userID, fmt.Sprintf("✅ Подпись для менеджера %d установлена: %s", opID, label))
			}
			delete(b.userState, userID)
			return
		}
		if strings.HasPrefix(b.userState[userID], "waiting_limit:") {
			if userID != config.Cfg.SuperUserID {
				delete(b.userState, userID)
				return
			}
			opIDStr := strings.TrimPrefix(b.userState[userID], "waiting_limit:")
			opID, err := strconv.ParseInt(opIDStr, 10, 64)
			if err != nil {
				b.send(userID, "❌ Внутренняя ошибка")
				delete(b.userState, userID)
				return
			}
			var limit int
			if _, err := fmt.Sscan(strings.TrimSpace(text), &limit); err != nil || limit < 1 || limit > 100 {
				b.send(userID, "❌ Введите число от 1 до 100")
				return
			}
			if err := db.SetOperatorMaxClients(opID, limit); err != nil {
				b.send(userID, "❌ Ошибка сохранения: "+err.Error())
			} else {
				b.send(userID, fmt.Sprintf("✅ Лимит для менеджера %d установлен: %d клиентов", opID, limit))
			}
			delete(b.userState, userID)
			return
		}
		if strings.HasPrefix(b.userState[userID], "waiting_expiry:") {
			if userID != config.Cfg.SuperUserID {
				delete(b.userState, userID)
				return
			}
			opIDStr := strings.TrimPrefix(b.userState[userID], "waiting_expiry:")
			opID, err := strconv.ParseInt(opIDStr, 10, 64)
			if err != nil {
				b.send(userID, "❌ Внутренняя ошибка")
				delete(b.userState, userID)
				return
			}
			t, err := time.ParseInLocation("02.01.2006", strings.TrimSpace(text), time.Local)
			if err != nil {
				b.send(userID, "❌ Неверный формат даты. Введите в формате ДД.ММ.ГГГГ (например: 01.02.2026)")
				return
			}
			expiryMs := t.UnixMilli()
			if err := db.SetOperatorExpiry(opID, expiryMs); err != nil {
				b.send(userID, "❌ Ошибка сохранения: "+err.Error())
				delete(b.userState, userID)
				return
			}
			b.send(userID, fmt.Sprintf("⏳ Устанавливаю срок и обновляю клиентов..."))
			count, err := panel.UpdateManagerClientsExpiry(opID, expiryMs)
			if err != nil {
				b.send(userID, fmt.Sprintf("⚠️ Срок установлен, но ошибка обновления в панели: %v", err))
			} else {
				b.send(userID, fmt.Sprintf("✅ Подписка менеджера %d установлена до %s\n👥 Обновлено клиентов: %d",
					opID, t.Format("02.01.2006"), count))
			}
			delete(b.userState, userID)
		}
	}
}

func buildHTTPClient() (*http.Client, error) {
	if config.Cfg.ProxyURL == "" {
		log.Println("ℹ️ Прокси не используется")
		return &http.Client{Timeout: 90 * time.Second}, nil
	}
	parsed, err := url.Parse(config.Cfg.ProxyURL)
	if err != nil {
		return nil, fmt.Errorf("ошибка парсинга прокси URL: %v", err)
	}
	if strings.ToLower(parsed.Scheme) == "socks5" {
		dialer, err := proxy.SOCKS5("tcp", parsed.Host, nil, proxy.Direct)
		if err != nil {
			return nil, fmt.Errorf("ошибка SOCKS5 dialer: %v", err)
		}
		log.Printf("🔗 Используем SOCKS5 прокси: %s", config.Cfg.ProxyURL)
		return &http.Client{
			Timeout: 90 * time.Second,
			Transport: &http.Transport{
				Dial:            dialer.Dial,
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			},
		}, nil
	}
	log.Printf("🔗 Используем %s прокси: %s", parsed.Scheme, config.Cfg.ProxyURL)
	return &http.Client{
		Timeout: 90 * time.Second,
		Transport: &http.Transport{
			Proxy:           http.ProxyURL(parsed),
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}, nil
}

func formatUsername(firstName, username string) string {
	if username != "" {
		return "@" + username
	}
	if firstName != "" {
		return firstName
	}
	return "Неизвестный"
}
