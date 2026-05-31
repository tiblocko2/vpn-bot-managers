package bot

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"vpn-bot/internal/config"
	"vpn-bot/internal/db"
	"vpn-bot/internal/panel"
)

func (b *Bot) handleCallback(update *tgbotapi.Update) {
	cb := update.CallbackQuery
	userID := cb.From.ID

	if !db.IsOperator(userID) {
		b.api.Request(tgbotapi.NewCallback(cb.ID, ""))
		b.send(userID, "❌ У вас нет доступа к боту")
		return
	}

	ack := func(text string) { b.api.Request(tgbotapi.NewCallback(cb.ID, text)) }
	data := cb.Data
	msgID := cb.Message.MessageID

	switch {
	case data == "add_user":
		ack("")
		b.showAddUser(userID)

	// --- client list ---

	case data == "client_list_new":
		ack("")
		b.showClientList(userID, 0, 0)

	case strings.HasPrefix(data, "client_list:"):
		ack("")
		page, _ := strconv.Atoi(strings.TrimPrefix(data, "client_list:"))
		b.showClientList(userID, page, msgID)

	case strings.HasPrefix(data, "client_detail:"):
		ack("")
		id, _ := strconv.ParseInt(strings.TrimPrefix(data, "client_detail:"), 10, 64)
		b.showClientDetail(userID, id, msgID)

	case strings.HasPrefix(data, "client_ib_add:"):
		ack("")
		parts := strings.SplitN(strings.TrimPrefix(data, "client_ib_add:"), ":", 2)
		if len(parts) != 2 {
			return
		}
		clientID, _ := strconv.ParseInt(parts[0], 10, 64)
		inboundID, _ := strconv.ParseInt(parts[1], 10, 64)
		if userID != config.Cfg.SuperUserID {
			owner, err := db.ClientOwner(clientID)
			if err != nil || owner != userID {
				b.send(userID, "❌ Нет доступа")
				return
			}
		}
		b.send(userID, "⏳ Добавляю в inbound...")
		if err := panel.AddExistingClientToInbound(clientID, inboundID); err != nil {
			b.send(userID, "❌ Ошибка: "+err.Error())
		} else {
			b.showClientDetail(userID, clientID, 0)
		}

	case strings.HasPrefix(data, "sub_connect:"):
		ack("")
		id, _ := strconv.ParseInt(strings.TrimPrefix(data, "sub_connect:"), 10, 64)
		b.showSubConnect(userID, id, msgID)

	// --- delete flow ---

	case strings.HasPrefix(data, "del_confirm:"):
		ack("")
		id, _ := strconv.ParseInt(strings.TrimPrefix(data, "del_confirm:"), 10, 64)
		b.showDeleteConfirm(userID, id, msgID)

	case strings.HasPrefix(data, "del_confirm_yes:"):
		ack("")
		id, _ := strconv.ParseInt(strings.TrimPrefix(data, "del_confirm_yes:"), 10, 64)
		if userID != config.Cfg.SuperUserID {
			owner, err := db.ClientOwner(id)
			if err != nil || owner != userID {
				b.send(userID, "❌ Нет доступа")
				return
			}
		}
		name, err := panel.DeleteClient(id)
		if err != nil {
			b.send(userID, "❌ Ошибка: "+err.Error())
		} else {
			b.sendOrEdit(userID, msgID, fmt.Sprintf("✅ Пользователь '%s' удалён", name), "",
				tgbotapi.NewInlineKeyboardMarkup(
					[]tgbotapi.InlineKeyboardButton{
						tgbotapi.NewInlineKeyboardButtonData("👥 Список клиентов", "client_list_new"),
						tgbotapi.NewInlineKeyboardButtonData("🏠 Меню", "back_to_menu"),
					},
				))
		}

	// --- operators (superuser only) ---

	case data == "ops_manage":
		if userID != config.Cfg.SuperUserID {
			ack("❌ Нет доступа")
			return
		}
		ack("")
		b.showOpsManage(userID)

	case data == "ops_add":
		if userID != config.Cfg.SuperUserID {
			ack("❌ Нет доступа")
			return
		}
		ack("")
		b.showAddOp(userID)

	case strings.HasPrefix(data, "ops_remove:"):
		if userID != config.Cfg.SuperUserID {
			ack("❌ Нет доступа")
			return
		}
		ack("")
		opID, _ := strconv.ParseInt(strings.TrimPrefix(data, "ops_remove:"), 10, 64)
		b.showDeleteManagerConfirm(userID, opID, msgID)

	case strings.HasPrefix(data, "ops_remove_yes:"):
		if userID != config.Cfg.SuperUserID {
			ack("❌ Нет доступа")
			return
		}
		ack("")
		opID, _ := strconv.ParseInt(strings.TrimPrefix(data, "ops_remove_yes:"), 10, 64)
		b.send(userID, "⏳ Удаляю клиентов из 3X-UI...")
		count, err := panel.DeleteManagerClients(opID)
		if err != nil {
			b.send(userID, fmt.Sprintf("⚠️ Ошибка удаления клиентов: %v", err))
			return
		}
		if err := db.RemoveOperator(opID); err != nil {
			b.send(userID, "❌ Ошибка удаления менеджера из БД")
			return
		}
		b.send(userID, fmt.Sprintf("✅ Менеджер %d удалён, клиентов удалено из 3X-UI: %d", opID, count))
		b.showOpsManage(userID)

	case strings.HasPrefix(data, "op_detail:"):
		if userID != config.Cfg.SuperUserID {
			ack("❌ Нет доступа")
			return
		}
		ack("")
		opID, _ := strconv.ParseInt(strings.TrimPrefix(data, "op_detail:"), 10, 64)
		b.showOpDetail(userID, opID, msgID)

	case strings.HasPrefix(data, "op_extend:"):
		if userID != config.Cfg.SuperUserID {
			ack("❌ Нет доступа")
			return
		}
		ack("")
		parts := strings.SplitN(strings.TrimPrefix(data, "op_extend:"), ":", 2)
		if len(parts) != 2 {
			return
		}
		opID, _ := strconv.ParseInt(parts[0], 10, 64)
		months, _ := strconv.Atoi(parts[1])
		current := db.GetOperatorExpiry(opID)
		var base time.Time
		if current == 0 || time.UnixMilli(current).Before(time.Now()) {
			base = time.Now()
		} else {
			base = time.UnixMilli(current)
		}
		newExpiry := base.AddDate(0, months, 0).UnixMilli()
		if err := db.SetOperatorExpiry(opID, newExpiry); err != nil {
			b.send(userID, "❌ Ошибка сохранения: "+err.Error())
			return
		}
		b.send(userID, "⏳ Обновляю клиентов в панели...")
		count, err := panel.UpdateManagerClientsExpiry(opID, newExpiry)
		if err != nil {
			b.send(userID, fmt.Sprintf("⚠️ Срок обновлён, но ошибка в панели: %v", err))
		} else {
			b.send(userID, fmt.Sprintf("✅ Подписка продлена на %d мес. до %s\n👥 Обновлено клиентов: %d",
				months, time.UnixMilli(newExpiry).Format("02.01.2006"), count))
		}
		b.showOpDetail(userID, opID, 0)

	case strings.HasPrefix(data, "op_set_expiry:"):
		if userID != config.Cfg.SuperUserID {
			ack("❌ Нет доступа")
			return
		}
		ack("")
		opID, _ := strconv.ParseInt(strings.TrimPrefix(data, "op_set_expiry:"), 10, 64)
		b.userState[userID] = fmt.Sprintf("waiting_expiry:%d", opID)
		msg := tgbotapi.NewMessage(userID, fmt.Sprintf(
			"📅 Введите дату окончания подписки для менеджера %d\nФормат: ДД.ММ.ГГГГ (например: 01.02.2027)",
			opID,
		))
		msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(
			[]tgbotapi.InlineKeyboardButton{tgbotapi.NewInlineKeyboardButtonData("❌ Отмена", "cancel")},
		)
		b.api.Send(msg)

	case strings.HasPrefix(data, "op_set_limit:"):
		if userID != config.Cfg.SuperUserID {
			ack("❌ Нет доступа")
			return
		}
		ack("")
		opID, _ := strconv.ParseInt(strings.TrimPrefix(data, "op_set_limit:"), 10, 64)
		b.userState[userID] = fmt.Sprintf("waiting_limit:%d", opID)
		msg2 := tgbotapi.NewMessage(userID, fmt.Sprintf(
			"✏️ Введите новый лимит клиентов для менеджера %d (число от 1 до 100):",
			opID,
		))
		msg2.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(
			[]tgbotapi.InlineKeyboardButton{tgbotapi.NewInlineKeyboardButtonData("❌ Отмена", "cancel")},
		)
		b.api.Send(msg2)

	case strings.HasPrefix(data, "op_set_label:"):
		if userID != config.Cfg.SuperUserID {
			ack("❌ Нет доступа")
			return
		}
		ack("")
		opID, _ := strconv.ParseInt(strings.TrimPrefix(data, "op_set_label:"), 10, 64)
		b.userState[userID] = fmt.Sprintf("waiting_label:%d", opID)
		current := db.GetOperatorLabel(opID)
		prompt := fmt.Sprintf("🏷 Введите подпись для менеджера %d\n(имя, никнейм — любой текст):", opID)
		if current != "" {
			prompt = fmt.Sprintf("🏷 Текущая подпись: <b>%s</b>\n\nВведите новую подпись для менеджера %d:", escapeHTML(current), opID)
		}
		msg3 := tgbotapi.NewMessage(userID, prompt)
		msg3.ParseMode = "HTML"
		msg3.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(
			[]tgbotapi.InlineKeyboardButton{tgbotapi.NewInlineKeyboardButtonData("❌ Отмена", "cancel")},
		)
		b.api.Send(msg3)

	// --- import flow (superuser only) ---

	case data == "import_panel":
		if userID != config.Cfg.SuperUserID {
			ack("❌ Нет доступа")
			return
		}
		ack("")
		delete(b.importSelection, userID)
		b.showImportSelection(userID)

	case strings.HasPrefix(data, "import_toggle:"):
		if userID != config.Cfg.SuperUserID {
			ack("❌ Нет доступа")
			return
		}
		ack("")
		id, _ := strconv.ParseInt(strings.TrimPrefix(data, "import_toggle:"), 10, 64)
		if b.importSelection[userID] == nil {
			b.importSelection[userID] = make(map[int64]bool)
		}
		b.importSelection[userID][id] = !b.importSelection[userID][id]
		b.showImportSelection(userID)

	case data == "import_run":
		if userID != config.Cfg.SuperUserID {
			ack("❌ Нет доступа")
			return
		}
		ack("")
		sel := b.importSelection[userID]
		var ids []int64
		for id, on := range sel {
			if on {
				ids = append(ids, id)
			}
		}
		delete(b.importSelection, userID)
		if len(ids) == 0 {
			b.send(userID, "❌ Не выбрано ни одного inbound")
			return
		}
		b.send(userID, "⏳ Импортирую клиентов из 3X-UI...")
		res, err := panel.ImportClientsFromPanel(ids)
		if err != nil {
			b.send(userID, "❌ Ошибка импорта: "+err.Error())
		} else {
			b.send(userID, fmt.Sprintf(
				"✅ Импорт завершён:\n• Добавлено: %d\n• Уже существуют: %d",
				res.Imported, res.Skipped,
			))
		}

	// --- inbound management (superuser only) ---

	case data == "inbound_settings":
		if userID != config.Cfg.SuperUserID {
			ack("❌ Нет доступа")
			return
		}
		ack("")
		b.showInboundSettings(userID)

	case data == "ib_add":
		if userID != config.Cfg.SuperUserID {
			ack("❌ Нет доступа")
			return
		}
		ack("")
		b.showInboundPicker(userID)

	case strings.HasPrefix(data, "ib_add_pick:"):
		if userID != config.Cfg.SuperUserID {
			ack("❌ Нет доступа")
			return
		}
		ack("")
		id, _ := strconv.ParseInt(strings.TrimPrefix(data, "ib_add_pick:"), 10, 64)
		label := fmt.Sprintf("Inbound %d", id)
		if list, err := panel.GetInboundList(); err == nil {
			for _, ib := range list {
				if ib.ID == id {
					label = ib.Remark
					break
				}
			}
		}
		if err := config.AddInbound(config.InboundConfig{ID: id, Label: label}); err != nil {
			b.send(userID, "❌ Ошибка сохранения: "+err.Error())
		} else {
			b.send(userID, fmt.Sprintf("✅ Inbound [%d] %s добавлен", id, label))
			b.showInboundSettings(userID)
		}

	case strings.HasPrefix(data, "ib_remove:"):
		if userID != config.Cfg.SuperUserID {
			ack("❌ Нет доступа")
			return
		}
		ack("")
		id, _ := strconv.ParseInt(strings.TrimPrefix(data, "ib_remove:"), 10, 64)
		if err := config.RemoveInbound(id); err != nil {
			b.send(userID, "❌ Ошибка сохранения: "+err.Error())
		} else {
			b.send(userID, fmt.Sprintf("✅ Inbound %d удалён из конфига", id))
			b.showInboundSettings(userID)
		}

	case data == "change_domain":
		if userID != config.Cfg.SuperUserID {
			ack("❌ Нет доступа")
			return
		}
		ack("")
		b.showChangeDomain(userID)

	case data == "cancel":
		ack("❌ Отменено")
		delete(b.userState, userID)
		b.showMenu(userID)

	case data == "back_to_menu":
		ack("")
		b.showMenu(userID)

	case data == "noop":
		ack("")
	}
}
