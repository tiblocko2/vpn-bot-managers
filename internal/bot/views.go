package bot

import (
	"fmt"
	"math"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"vpn-bot/internal/config"
	"vpn-bot/internal/db"
	"vpn-bot/internal/panel"
)

// sendOrEdit sends a new message or edits an existing one in place.
func (b *Bot) sendOrEdit(userID int64, editMsgID int, text, parseMode string, markup tgbotapi.InlineKeyboardMarkup) {
	if editMsgID != 0 {
		edit := tgbotapi.NewEditMessageText(userID, editMsgID, text)
		edit.ParseMode = parseMode
		edit.ReplyMarkup = &markup
		b.api.Send(edit)
		return
	}
	msg := tgbotapi.NewMessage(userID, text)
	msg.ParseMode = parseMode
	msg.ReplyMarkup = markup
	b.api.Send(msg)
}

func (b *Bot) showMenu(userID int64) {
	btns := [][]tgbotapi.InlineKeyboardButton{
		{tgbotapi.NewInlineKeyboardButtonData("➕ Добавить пользователя", "add_user")},
		{tgbotapi.NewInlineKeyboardButtonData("👥 Список клиентов", "client_list_new")},
	}
	if userID == config.Cfg.SuperUserID {
		btns = append(btns,
			[]tgbotapi.InlineKeyboardButton{
				tgbotapi.NewInlineKeyboardButtonData("🔄 Импорт из 3X-UI", "import_panel"),
			},
			[]tgbotapi.InlineKeyboardButton{
				tgbotapi.NewInlineKeyboardButtonData("👤 Управление операторами", "ops_manage"),
			},
			[]tgbotapi.InlineKeyboardButton{
				tgbotapi.NewInlineKeyboardButtonData("🌐 Сменить домен подписок", "change_domain"),
			},
			[]tgbotapi.InlineKeyboardButton{
				tgbotapi.NewInlineKeyboardButtonData("⚙️ Управление inbound", "inbound_settings"),
			},
		)
	}
	msg := tgbotapi.NewMessage(userID, "🔧 Выберите действие:")
	msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(btns...)
	b.api.Send(msg)
}

func (b *Bot) showAddUser(userID int64) {
	if userID != config.Cfg.SuperUserID {
		count := db.GetClientCountByOwner(userID)
		if count >= 6 {
			b.send(userID, fmt.Sprintf("❌ Достигнут лимит: вы можете добавить не более 6 клиентов (у вас %d/6)", count))
			return
		}
	}
	b.userState[userID] = "waiting_name"
	msg := tgbotapi.NewMessage(userID, "📝 Введите Фамилию и Имя нового пользователя.\n\nПример: `Иванов Иван`")
	msg.ParseMode = "Markdown"
	msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(
		[]tgbotapi.InlineKeyboardButton{tgbotapi.NewInlineKeyboardButtonData("❌ Отмена", "cancel")},
	)
	b.api.Send(msg)
}

// showClientList renders a paginated list of clients as clickable buttons.
// editMsgID > 0 means edit an existing message in place (for pagination).
func (b *Bot) showClientList(userID int64, page int, editMsgID int) {
	var ownerID int64
	if userID != config.Cfg.SuperUserID {
		ownerID = userID
	}
	clients, total, err := db.GetClientsPage(page, ownerID)
	if err != nil {
		b.send(userID, "❌ Ошибка получения списка клиентов")
		return
	}
	if total == 0 {
		b.send(userID, "📭 Список клиентов пуст")
		return
	}

	totalPages := int(math.Ceil(float64(total) / float64(db.ClientsPerPage)))
	listTitle := "👥 Клиенты"
	if userID != config.Cfg.SuperUserID {
		listTitle = "👥 Мои клиенты"
	}
	text := fmt.Sprintf("%s (всего: %d, стр. %d/%d):", listTitle, total, page+1, totalPages)

	var buttons [][]tgbotapi.InlineKeyboardButton
	for _, c := range clients {
		label := c.Comment
		if len([]rune(label)) > 50 {
			label = string([]rune(label)[:50])
		}
		buttons = append(buttons, []tgbotapi.InlineKeyboardButton{
			tgbotapi.NewInlineKeyboardButtonData(label, fmt.Sprintf("client_detail:%d", c.ID)),
		})
	}

	if totalPages > 1 {
		var nav []tgbotapi.InlineKeyboardButton
		if page > 0 {
			nav = append(nav, tgbotapi.NewInlineKeyboardButtonData("⬅️", fmt.Sprintf("client_list:%d", page-1)))
		}
		nav = append(nav, tgbotapi.NewInlineKeyboardButtonData(fmt.Sprintf("%d/%d", page+1, totalPages), "noop"))
		if page < totalPages-1 {
			nav = append(nav, tgbotapi.NewInlineKeyboardButtonData("➡️", fmt.Sprintf("client_list:%d", page+1)))
		}
		buttons = append(buttons, nav)
	}
	buttons = append(buttons, []tgbotapi.InlineKeyboardButton{
		tgbotapi.NewInlineKeyboardButtonData("⬅️ Назад", "back_to_menu"),
	})

	b.sendOrEdit(userID, editMsgID, text, "", tgbotapi.NewInlineKeyboardMarkup(buttons...))
}

// showClientDetail renders the detail view for a single client.
func (b *Bot) showClientDetail(userID int64, clientID int64, editMsgID int) {
	if userID != config.Cfg.SuperUserID {
		owner, err := db.ClientOwner(clientID)
		if err != nil || owner != userID {
			b.send(userID, "❌ Клиент не найден")
			return
		}
	}
	details, err := db.GetClientDetails(clientID)
	if err != nil {
		b.send(userID, "❌ Клиент не найден")
		return
	}

	ibLabels := make(map[int64]string)
	for _, ib := range config.Cfg.Inbounds {
		ibLabels[ib.ID] = ib.Label
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("👤 <b>%s</b>\n\n", escapeHTML(details.Comment)))

	if config.Cfg.SubDomain != "" && details.Subscription != "" {
		sb.WriteString(fmt.Sprintf("🔗 Ссылка на подписку:\n<code>%s/%s</code>\n\n", config.Cfg.SubDomain, details.Subscription))
	}

	sb.WriteString("📡 Подключённые inbound:\n")
	if len(details.Emails) == 0 {
		sb.WriteString("  нет\n")
	} else {
		for ibID := range details.Emails {
			label := ibLabels[ibID]
			if label == "" {
				label = fmt.Sprintf("ID %d", ibID)
			}
			sb.WriteString(fmt.Sprintf("  ✅ [%d] %s\n", ibID, escapeHTML(label)))
		}
	}

	var buttons [][]tgbotapi.InlineKeyboardButton
	for _, ib := range config.Cfg.Inbounds {
		if _, connected := details.Emails[ib.ID]; !connected {
			label := fmt.Sprintf("➕ Добавить в [%d] %s", ib.ID, ib.Label)
			buttons = append(buttons, []tgbotapi.InlineKeyboardButton{
				tgbotapi.NewInlineKeyboardButtonData(label, fmt.Sprintf("client_ib_add:%d:%d", clientID, ib.ID)),
			})
		}
	}

	buttons = append(buttons,
		[]tgbotapi.InlineKeyboardButton{
			tgbotapi.NewInlineKeyboardButtonData("🗑 Удалить клиента", fmt.Sprintf("del_confirm:%d", clientID)),
		},
		[]tgbotapi.InlineKeyboardButton{
			tgbotapi.NewInlineKeyboardButtonData("⬅️ К списку клиентов", "client_list:0"),
		},
	)

	b.sendOrEdit(userID, editMsgID, sb.String(), "HTML", tgbotapi.NewInlineKeyboardMarkup(buttons...))
}

// showDeleteConfirm asks for confirmation before deleting a client.
func (b *Bot) showDeleteConfirm(userID int64, clientID int64, editMsgID int) {
	if userID != config.Cfg.SuperUserID {
		owner, err := db.ClientOwner(clientID)
		if err != nil || owner != userID {
			b.send(userID, "❌ Клиент не найден")
			return
		}
	}
	details, err := db.GetClientDetails(clientID)
	name := "клиента"
	if err == nil {
		name = fmt.Sprintf("'%s'", details.Comment)
	}
	text := fmt.Sprintf("⚠️ Удалить пользователя %s?\nЭто действие нельзя отменить.", name)
	buttons := tgbotapi.NewInlineKeyboardMarkup(
		[]tgbotapi.InlineKeyboardButton{
			tgbotapi.NewInlineKeyboardButtonData("✅ Да, удалить", fmt.Sprintf("del_confirm_yes:%d", clientID)),
			tgbotapi.NewInlineKeyboardButtonData("❌ Отмена", fmt.Sprintf("client_detail:%d", clientID)),
		},
	)
	b.sendOrEdit(userID, editMsgID, text, "", buttons)
}

func (b *Bot) showOpsManage(userID int64) {
	operators, err := db.GetAllOperators()
	if err != nil {
		b.send(userID, "❌ Ошибка получения списка операторов")
		return
	}

	text := "👤 Операторы:\n"
	if len(operators) == 0 {
		text += "_список пуст_"
	} else {
		text += fmt.Sprintf("Всего: %d\n\n", len(operators))
		for _, id := range operators {
			text += fmt.Sprintf("• %d\n", id)
		}
	}

	var buttons [][]tgbotapi.InlineKeyboardButton
	for _, opID := range operators {
		buttons = append(buttons, []tgbotapi.InlineKeyboardButton{
			tgbotapi.NewInlineKeyboardButtonData(
				"❌ "+fmt.Sprintf("%d", opID),
				fmt.Sprintf("ops_remove:%d", opID),
			),
		})
	}
	buttons = append(buttons,
		[]tgbotapi.InlineKeyboardButton{
			tgbotapi.NewInlineKeyboardButtonData("➕ Добавить оператора", "ops_add"),
		},
		[]tgbotapi.InlineKeyboardButton{
			tgbotapi.NewInlineKeyboardButtonData("⬅️ Назад", "back_to_menu"),
		},
	)

	msg := tgbotapi.NewMessage(userID, text)
	msg.ParseMode = "Markdown"
	msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(buttons...)
	b.api.Send(msg)
}

func (b *Bot) showAddOp(userID int64) {
	b.userState[userID] = "waiting_op_id"
	msg := tgbotapi.NewMessage(userID, "🆔 Введите Telegram ID нового оператора или перешлите сообщение от него.")
	msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(
		[]tgbotapi.InlineKeyboardButton{tgbotapi.NewInlineKeyboardButtonData("❌ Отмена", "cancel")},
	)
	b.api.Send(msg)
}

func (b *Bot) showInboundSettings(userID int64) {
	inbounds := config.Cfg.Inbounds
	text := "⚙️ Настроенные inbound:\n\n"
	if len(inbounds) == 0 {
		text += "_ни один не добавлен_"
	} else {
		for _, ib := range inbounds {
			text += fmt.Sprintf("• [%d] %s\n", ib.ID, ib.Label)
		}
	}

	var buttons [][]tgbotapi.InlineKeyboardButton
	for _, ib := range inbounds {
		label := fmt.Sprintf("❌ [%d] %s", ib.ID, ib.Label)
		if len([]rune(label)) > 50 {
			label = string([]rune(label)[:50])
		}
		buttons = append(buttons, []tgbotapi.InlineKeyboardButton{
			tgbotapi.NewInlineKeyboardButtonData(label, fmt.Sprintf("ib_remove:%d", ib.ID)),
		})
	}
	buttons = append(buttons,
		[]tgbotapi.InlineKeyboardButton{
			tgbotapi.NewInlineKeyboardButtonData("➕ Добавить inbound", "ib_add"),
		},
		[]tgbotapi.InlineKeyboardButton{
			tgbotapi.NewInlineKeyboardButtonData("⬅️ Назад", "back_to_menu"),
		},
	)

	msg := tgbotapi.NewMessage(userID, text)
	msg.ParseMode = "Markdown"
	msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(buttons...)
	b.api.Send(msg)
}

func (b *Bot) showInboundPicker(userID int64) {
	b.send(userID, "⏳ Загружаю список inbound из панели...")

	inbounds, err := panel.GetInboundList()
	if err != nil {
		b.send(userID, "❌ Ошибка загрузки inbound: "+err.Error())
		return
	}
	if len(inbounds) == 0 {
		b.send(userID, "❌ Inbound не найдены в панели")
		return
	}

	configured := make(map[int64]bool)
	for _, ib := range config.Cfg.Inbounds {
		configured[ib.ID] = true
	}

	var buttons [][]tgbotapi.InlineKeyboardButton
	for _, ib := range inbounds {
		if configured[ib.ID] {
			continue
		}
		status := ""
		if !ib.Enable {
			status = " ⚫"
		}
		label := fmt.Sprintf("[%d] %s (%s)%s", ib.ID, ib.Remark, ib.Protocol, status)
		buttons = append(buttons, []tgbotapi.InlineKeyboardButton{
			tgbotapi.NewInlineKeyboardButtonData(label, fmt.Sprintf("ib_add_pick:%d", ib.ID)),
		})
	}
	if len(buttons) == 0 {
		b.send(userID, "✅ Все inbound из панели уже добавлены в конфиг")
		b.showInboundSettings(userID)
		return
	}
	buttons = append(buttons, []tgbotapi.InlineKeyboardButton{
		tgbotapi.NewInlineKeyboardButtonData("⬅️ Назад", "inbound_settings"),
	})

	msg := tgbotapi.NewMessage(userID, "Выберите inbound для добавления:")
	msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(buttons...)
	b.api.Send(msg)
}

func (b *Bot) showImportSelection(userID int64) {
	inbounds := config.Cfg.Inbounds
	if len(inbounds) == 0 {
		b.send(userID, "❌ Нет настроенных inbound для импорта")
		return
	}

	sel := b.importSelection[userID]
	if sel == nil {
		sel = make(map[int64]bool)
		b.importSelection[userID] = sel
	}

	var buttons [][]tgbotapi.InlineKeyboardButton
	for _, ib := range inbounds {
		mark := "⬜"
		if sel[ib.ID] {
			mark = "✅"
		}
		label := fmt.Sprintf("%s [%d] %s", mark, ib.ID, ib.Label)
		buttons = append(buttons, []tgbotapi.InlineKeyboardButton{
			tgbotapi.NewInlineKeyboardButtonData(label, fmt.Sprintf("import_toggle:%d", ib.ID)),
		})
	}

	anySelected := false
	for _, v := range sel {
		if v {
			anySelected = true
			break
		}
	}

	runLabel := "⚠️ Выберите хотя бы один inbound"
	runData := "noop"
	if anySelected {
		runLabel = "▶️ Запустить импорт"
		runData = "import_run"
	}
	buttons = append(buttons,
		[]tgbotapi.InlineKeyboardButton{
			tgbotapi.NewInlineKeyboardButtonData(runLabel, runData),
		},
		[]tgbotapi.InlineKeyboardButton{
			tgbotapi.NewInlineKeyboardButtonData("❌ Отмена", "back_to_menu"),
		},
	)

	msg := tgbotapi.NewMessage(userID, "📥 Выберите inbound для импорта клиентов:")
	msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(buttons...)
	b.api.Send(msg)
}

func (b *Bot) showChangeDomain(userID int64) {
	b.userState[userID] = "waiting_domain"
	msg := tgbotapi.NewMessage(userID, fmt.Sprintf(
		"🌐 Текущий домен подписок:\n`%s`\n\nВведите новый базовый URL без слеша в конце.\nПример: `https://akvilon2.nemesh-vpn.ru:2096/sub`",
		config.Cfg.SubDomain,
	))
	msg.ParseMode = "Markdown"
	msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(
		[]tgbotapi.InlineKeyboardButton{tgbotapi.NewInlineKeyboardButtonData("❌ Отмена", "cancel")},
	)
	b.api.Send(msg)
}

func escapeHTML(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}
