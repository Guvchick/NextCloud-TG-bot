package main

import (
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"
)

func (a *App) start(msg *Message) {
	if a.isAdmin(msg.From.ID) {
		_, _ = a.tg.SendMessage(msg.Chat.ID, a.adminSummary(), adminKeyboard())
		return
	}
	user, err := a.db.UpsertRequest(msg.From.ID, ptrOrNil(msg.From.Username), ptrOrNil(msg.From.FirstName), ptrOrNil(msg.From.LastName))
	if err != nil {
		_, _ = a.tg.SendMessage(msg.Chat.ID, "⚠️ Ошибка базы: <code>"+esc(err.Error())+"</code>", nil)
		return
	}
	if user.Status == "approved" {
		if user.NCUserID != nil {
			exists, err := a.nc.UserExists(*user.NCUserID)
			if err == nil && !exists {
				_ = a.db.DeleteUser(user.TelegramID)
				_, _ = a.tg.SendMessage(msg.Chat.ID, "Аккаунт не найден в облаке, запись бота очищена. Отправьте /start еще раз.", nil)
				return
			}
		}
		_ = a.sendEventSticker(msg.Chat.ID, "welcome")
		_, _ = a.tg.SendMessage(msg.Chat.ID, a.accountText(user), accountKeyboard(a.cfg, langOf(user)))
		return
	}
	if user.Status == "rejected" {
		_, _ = a.tg.SendMessage(msg.Chat.ID, "Ваша заявка на beta-тест сейчас отклонена.", nil)
		return
	}
	_, _ = a.tg.SendMessage(msg.Chat.ID, "<b>Заявка отправлена ✨</b>\n\nАдминистратор проверит доступ к beta-тесту. Я сообщу, когда аккаунт будет готов.", nil)
	for adminID := range a.cfg.AdminIDs {
		text := "<b>Новая заявка на beta-тест</b>\n<code>━━━━━━━━━━━━━━━━━━━━</code>\n\n" +
			"Пользователь: " + displayName(user) + "\n" +
			fmt.Sprintf("Telegram ID: <code>%d</code>", user.TelegramID)
		_, _ = a.tg.SendMessage(adminID, text, requestReviewKeyboard(user.TelegramID))
	}
	log.Printf("beta request: telegram_id=%d username=%s", msg.From.ID, msg.From.Username)
}

func (a *App) handleCallback(cb *CallbackQuery) {
	data := cb.Data
	if !a.isAdmin(cb.From.ID) &&
		!strings.HasPrefix(data, "account:") &&
		!strings.HasPrefix(data, "lang:") &&
		!strings.HasPrefix(data, "donate:") &&
		!strings.HasPrefix(data, "stars:") &&
		!strings.HasPrefix(data, "platega:") &&
		!strings.HasPrefix(data, "platega_check:") {
		a.tg.AnswerCallback(cb.ID, "Нет доступа", true)
		return
	}
	switch {
	case data == "admin":
		a.edit(cb, a.adminSummary(), adminKeyboard())
	case data == "users:search":
		a.states.Set(cb.From.ID, State{Kind: StateAdminSearch})
		_, _ = a.tg.SendMessage(cb.Message.Chat.ID, "🔎 Отправьте Telegram ID или тег пользователя. Например: <code>8799317819</code> или <code>@username</code>.", usersMenuKeyboard())
	case data == "stickers":
		a.edit(cb, a.stickersText(), stickersKeyboard(a.stickers))
	case strings.HasPrefix(data, "sticker:event:"):
		a.stickerEvent(cb)
	case strings.HasPrefix(data, "sticker:set:"):
		a.stickerSetFromButton(cb)
	case strings.HasPrefix(data, "sticker:preview:"):
		a.stickerPreview(cb)
	case strings.HasPrefix(data, "sticker:clear:"):
		a.stickerClear(cb)
	case strings.HasPrefix(data, "users:"):
		a.usersList(cb)
	case strings.HasPrefix(data, "user:"):
		a.userDetails(cb)
	case strings.HasPrefix(data, "approve:"):
		a.approveUser(cb)
	case strings.HasPrefix(data, "reject:"):
		a.rejectUser(cb)
	case strings.HasPrefix(data, "quotaadd:"):
		a.quotaAdd(cb)
	case strings.HasPrefix(data, "quotacustom:"):
		id := parseLastInt(data)
		a.states.Set(cb.From.ID, State{Kind: StateQuotaCustom, TargetID: id})
		_, _ = a.tg.SendMessage(cb.Message.Chat.ID, "Введите, сколько GB добавить пользователю.", backAdminKeyboard())
	case strings.HasPrefix(data, "resetpass:"):
		a.resetPassword(cb)
	case strings.HasPrefix(data, "supporter:"):
		a.setSupporter(cb)
	case strings.HasPrefix(data, "disable:"):
		a.setEnabled(cb, false)
	case strings.HasPrefix(data, "enable:"):
		a.setEnabled(cb, true)
	case strings.HasPrefix(data, "deleteask:"):
		id := parseLastInt(data)
		a.edit(cb, fmt.Sprintf("<b>Удаление пользователя</b>\n\nБудет удален аккаунт облака и запись в базе бота.\nПользователь: <code>%d</code>", id), deleteConfirmKeyboard(id))
	case strings.HasPrefix(data, "deleteyes:"):
		a.deleteUser(cb)
	case data == "account:home":
		a.accountHome(cb)
	case data == "account:support":
		a.accountSupport(cb)
	case data == "account:donate":
		a.accountDonate(cb)
	case data == "account:language":
		a.edit(cb, "<b>🌐 Выберите язык</b>", languageKeyboard())
	case data == "account:change_password":
		a.states.Set(cb.From.ID, State{Kind: StateChangePassword})
		_, _ = a.tg.SendMessage(cb.Message.Chat.ID, "🔐 Отправьте новый пароль для облака.\n\nМинимум 8 символов. После смены бот обновит сохраненный пароль для загрузок.", accountBackKeyboard())
	case strings.HasPrefix(data, "lang:"):
		a.setLanguage(cb)
	case strings.HasPrefix(data, "donate:"):
		a.donateCallback(cb)
	case strings.HasPrefix(data, "stars:"):
		a.starsDonate(cb)
	case strings.HasPrefix(data, "platega:"):
		a.plategaCreate(cb)
	case strings.HasPrefix(data, "platega_check:"):
		a.plategaCheck(cb)
	case data == "sync":
		checked, removed, err := a.syncNextcloudUsers()
		if err != nil {
			a.tg.AnswerCallback(cb.ID, "Ошибка", true)
			_, _ = a.tg.SendMessage(cb.Message.Chat.ID, "⚠️ Синхронизация не удалась: <code>"+esc(err.Error())+"</code>", nil)
			return
		}
		a.edit(cb, fmt.Sprintf("🔄 <b>Синхронизация завершена</b>\n\nПроверено: <b>%d</b>\nУдалено из БД бота: <b>%d</b>", checked, removed), maintenanceKeyboard())
	case data == "maintenance":
		a.edit(cb, a.maintenanceText(), maintenanceKeyboard())
	case data == "admincloud":
		a.adminCloudCheck(cb)
	case strings.HasPrefix(data, "backup"):
		a.backupCallback(cb)
	case strings.HasPrefix(data, "restore:"):
		a.restoreBackupCallback(cb)
	case data == "broadcast":
		a.states.Set(cb.From.ID, State{Kind: StateBroadcast})
		_, _ = a.tg.SendMessage(cb.Message.Chat.ID, "📣 Отправьте любое сообщение для рассылки: текст, фото, документ, видео или другой тип сообщения.", backAdminKeyboard())
	default:
		a.tg.AnswerCallback(cb.ID, "Неизвестное действие", true)
		return
	}
	a.tg.AnswerCallback(cb.ID, "", false)
}

func (a *App) handleStateMessage(msg *Message, st State) {
	switch st.Kind {
	case StateAdminSearch:
		a.states.Clear(msg.From.ID)
		a.renderSearch(msg.Chat.ID, msg.Text)
	case StateBroadcast:
		a.states.Clear(msg.From.ID)
		a.broadcastMessage(msg)
	case StateSticker:
		a.saveSticker(msg, st)
	case StateQuotaCustom:
		amount, err := strconv.Atoi(strings.TrimSpace(msg.Text))
		if err != nil || amount <= 0 {
			_, _ = a.tg.SendMessage(msg.Chat.ID, "Введите целое число GB больше нуля.", backAdminKeyboard())
			return
		}
		a.states.Clear(msg.From.ID)
		a.addQuotaToUser(msg.Chat.ID, st.TargetID, amount)
	case StateChangePassword:
		a.applyUserPassword(msg)
	}
}

func (a *App) adminSummary() string {
	total, _ := a.db.CountUsers("")
	requested, _ := a.db.CountUsers("requested")
	approved, _ := a.db.CountUsers("approved")
	rejected, _ := a.db.CountUsers("rejected")
	return fmt.Sprintf("<b>🛠️ Админ-панель облака</b>\n<code>━━━━━━━━━━━━━━━━━━━━</code>\n\n👥 Всего пользователей: <b>%d</b>\n📝 Заявок: <b>%d</b>\n✅ Одобрено: <b>%d</b>\n❌ Отклонено: <b>%d</b>", total, requested, approved, rejected)
}

func (a *App) maintenanceText() string {
	return "🔄 <b>Синхронизация и восстановление</b>\n<code>━━━━━━━━━━━━━━━━━━━━</code>\n\n" +
		"Здесь проверяется связь с облаком, синхронизируются пользователи и управляются сжатые бекапы PostgreSQL."
}

func (a *App) health(chatID int64) {
	status := "✅ Nextcloud API доступен"
	if err := a.nc.CheckConnection(); err != nil {
		status = "⚠️ Nextcloud API недоступен: <code>" + esc(err.Error()) + "</code>"
	}
	_, _ = a.tg.SendMessage(chatID, "<b>Проверка Nextcloud</b>\n\nПубличный URL: <code>"+esc(a.cfg.NextcloudURL)+"</code>\nВнутренний URL: <code>"+esc(a.cfg.NextcloudInternalURL)+"</code>\n\n"+status, nil)
}

func (a *App) adminCloudCheck(cb *CallbackQuery) {
	status := "✅ API доступен"
	if err := a.nc.CheckConnection(); err != nil {
		status = "⚠️ API недоступен: <code>" + esc(err.Error()) + "</code>"
	}
	quota := ""
	used, available, err := a.nc.GetQuota(a.cfg.NextcloudAdminUser, a.cfg.NextcloudAdminPassword)
	if err != nil {
		quota = "\n\n☁️ Квота админского аккаунта: <code>" + esc(err.Error()) + "</code>"
	} else {
		total := used + available
		percent := 0.0
		if total > 0 {
			percent = float64(used) / float64(total) * 100
		}
		quota = fmt.Sprintf("\n\n☁️ Админский аккаунт: <code>%s</code>\nЗанято: <b>%s</b> / <b>%s</b>\n📊 <code>%s</code> %.1f%%", esc(a.cfg.NextcloudAdminUser), formatBytes(used), formatBytes(total), usageBar(used, available), percent)
	}
	a.edit(cb, "<b>☁️ Проверка моего клауда</b>\n\nПубличный URL: <code>"+esc(a.cfg.NextcloudURL)+"</code>\nВнутренний URL: <code>"+esc(a.cfg.NextcloudInternalURL)+"</code>\n\n"+status+quota, maintenanceKeyboard())
}

func (a *App) renderSearch(chatID int64, query string) {
	query = strings.TrimSpace(query)
	if query == "" {
		_, _ = a.tg.SendMessage(chatID, "Введите Telegram ID или тег для поиска.", usersMenuKeyboard())
		return
	}
	users, err := a.db.SearchUsers(query, 10)
	if err != nil {
		_, _ = a.tg.SendMessage(chatID, "⚠️ Поиск не удался: <code>"+esc(err.Error())+"</code>", usersMenuKeyboard())
		return
	}
	text := fmt.Sprintf("🔎 <b>Поиск</b>: <code>%s</code>\n\n", esc(query))
	if len(users) == 0 {
		text += "Ничего не найдено."
	} else {
		text += "Выберите пользователя."
	}
	_, _ = a.tg.SendMessage(chatID, text, usersKeyboard(users, "all", 0, false))
}

func (a *App) setStickerStart(msg *Message, parts []string) {
	if len(parts) != 2 || !stickerEventAllowed(parts[1]) {
		_, _ = a.tg.SendMessage(msg.Chat.ID, "Используйте: <code>/setsticker welcome</code> или настройте через кнопку ✨ Стикеры.", stickersKeyboard(a.stickers))
		return
	}
	a.states.Set(msg.From.ID, State{Kind: StateSticker, Event: parts[1]})
	_, _ = a.tg.SendMessage(msg.Chat.ID, "Отправьте стикер или custom emoji для события <code>"+esc(parts[1])+"</code>.\n\nРекомендуемый набор: "+esc(a.cfg.CustomEmojiPackURL), stickerEventKeyboard(parts[1], false))
}

func (a *App) saveSticker(msg *Message, st State) {
	kind := ""
	id := ""
	if msg.Sticker != nil && msg.Sticker.FileID != "" {
		kind = StickerKindSticker
		id = msg.Sticker.FileID
	} else if customEmojiID := firstCustomEmojiID(msg); customEmojiID != "" {
		kind = StickerKindCustomEmoji
		id = customEmojiID
	}
	if id == "" {
		_, _ = a.tg.SendMessage(msg.Chat.ID, "Нужно отправить стикер или custom emoji.", stickerEventKeyboard(st.Event, false))
		return
	}
	if err := a.stickers.Set(st.Event, kind, id); err != nil {
		_, _ = a.tg.SendMessage(msg.Chat.ID, "Не удалось сохранить стикер: <code>"+esc(err.Error())+"</code>", adminKeyboard())
		return
	}
	a.states.Clear(msg.From.ID)
	_, _ = a.tg.SendMessage(msg.Chat.ID, "✨ Оформление для <code>"+esc(st.Event)+"</code> сохранено в файл. Если Telegram его отклонит, бот оставит базовый маркер "+eventMark(st.Event)+".", stickerEventKeyboard(st.Event, true))
	_ = a.previewSticker(msg.Chat.ID, st.Event)
}

func (a *App) stickersText() string {
	line := func(event string) string {
		mode := "базовый"
		if value, ok := a.stickers.Get(event); ok {
			if value.Kind == StickerKindCustomEmoji {
				mode = "custom emoji"
			} else {
				mode = "стикер"
			}
		}
		return event + ": <b>" + mode + "</b> " + eventMark(event)
	}
	return "<b>✨ Стикеры</b>\n\n" +
		"Хранение: <code>" + esc(a.cfg.StickerStoreFile) + "</code>\n" +
		"Рекомендуемый набор emoji: " + esc(a.cfg.CustomEmojiPackURL) + "\n\n" +
		line("welcome") + "\n" +
		line("approved") + "\n" +
		line("upload_ok") + "\n" +
		line("error") + "\n" +
		line("support") + "\n" +
		line("donate") + "\n" +
		line("language") + "\n" +
		line("password") + "\n" +
		line("premium") + "\n" +
		line("backup") + "\n" +
		line("sync")
}

func (a *App) stickerEvent(cb *CallbackQuery) {
	event := strings.TrimPrefix(cb.Data, "sticker:event:")
	if !stickerEventAllowed(event) {
		a.tg.AnswerCallback(cb.ID, "Неизвестное событие", true)
		return
	}
	value, ok := a.stickers.Get(event)
	mode := "базовый emoji " + eventMark(event)
	if ok {
		mode = value.Kind
	}
	text := "✨ <b>Оформление события</b>\n\nСобытие: <code>" + esc(event) + "</code>\nТекущее: <b>" + esc(mode) + "</b>\n\nМожно поставить обычный Telegram-стикер или custom emoji из набора:\n" + esc(a.cfg.CustomEmojiPackURL)
	a.edit(cb, text, stickerEventKeyboard(event, ok))
}

func (a *App) stickerSetFromButton(cb *CallbackQuery) {
	event := strings.TrimPrefix(cb.Data, "sticker:set:")
	if !stickerEventAllowed(event) {
		a.tg.AnswerCallback(cb.ID, "Неизвестное событие", true)
		return
	}
	a.states.Set(cb.From.ID, State{Kind: StateSticker, Event: event})
	a.edit(cb, "➕ <b>Установка оформления</b>\n\nОтправьте следующим сообщением Telegram-стикер или custom emoji для события <code>"+esc(event)+"</code>.\n\nНабор: "+esc(a.cfg.CustomEmojiPackURL), stickerEventKeyboard(event, false))
}

func (a *App) stickerPreview(cb *CallbackQuery) {
	event := strings.TrimPrefix(cb.Data, "sticker:preview:")
	if err := a.previewSticker(cb.Message.Chat.ID, event); err != nil {
		a.tg.AnswerCallback(cb.ID, "Нет сохраненного оформления", true)
	}
}

func (a *App) previewSticker(chatID int64, event string) error {
	value, ok := a.stickers.Get(event)
	if !ok {
		return errors.New("empty sticker")
	}
	if value.Kind == StickerKindSticker {
		return a.tg.SendSticker(chatID, value.ID)
	}
	_, err := a.tg.SendMessage(chatID, "Предпросмотр "+esc(event)+": "+customEmojiHTML(value.ID, eventMark(event)), nil)
	return err
}

func (a *App) stickerClear(cb *CallbackQuery) {
	event := strings.TrimPrefix(cb.Data, "sticker:clear:")
	if !stickerEventAllowed(event) {
		a.tg.AnswerCallback(cb.ID, "Неизвестное событие", true)
		return
	}
	if err := a.stickers.Clear(event); err != nil {
		a.tg.AnswerCallback(cb.ID, "Не удалось очистить", true)
		return
	}
	a.edit(cb, "🧹 Оформление для <code>"+esc(event)+"</code> очищено. Остался базовый маркер "+eventMark(event)+".", stickerEventKeyboard(event, false))
}

func (a *App) usersList(cb *CallbackQuery) {
	parts := strings.Split(cb.Data, ":")
	if len(parts) == 2 && parts[1] == "menu" {
		a.edit(cb, "👥 <b>Пользователи</b>\n\nВыберите список или поиск.", usersMenuKeyboard())
		return
	}
	if len(parts) != 3 {
		return
	}
	status := parts[1]
	page, _ := strconv.Atoi(parts[2])
	queryStatus := status
	if status == "all" {
		queryStatus = ""
	}
	users, err := a.db.ListUsers(queryStatus, pageSize+1, page*pageSize)
	if err != nil {
		a.tg.AnswerCallback(cb.ID, "Ошибка базы", true)
		return
	}
	hasNext := len(users) > pageSize
	if hasNext {
		users = users[:pageSize]
	}
	title := "Все пользователи"
	if status != "all" {
		title = "Пользователи: " + status
	}
	text := "<b>" + esc(title) + "</b>\n\n"
	if len(users) == 0 {
		text += "Пока пусто."
	} else {
		text += "Выберите пользователя."
	}
	a.edit(cb, text, usersKeyboard(users, status, page, hasNext))
}

func (a *App) userDetails(cb *CallbackQuery) {
	parts := strings.Split(cb.Data, ":")
	if len(parts) < 4 {
		return
	}
	id, _ := strconv.ParseInt(parts[1], 10, 64)
	backStatus := parts[2]
	backPage, _ := strconv.Atoi(parts[3])
	text, markup, err := a.userDetailsView(id, backStatus, backPage)
	if err != nil {
		a.tg.AnswerCallback(cb.ID, err.Error(), true)
		return
	}
	a.edit(cb, text, markup)
}

func (a *App) userDetailsView(id int64, backStatus string, backPage int) (string, *InlineKeyboardMarkup, error) {
	user, err := a.db.GetUser(id)
	if err != nil || user == nil {
		return "", nil, errors.New("Пользователь не найден")
	}
	storage := "☁️ Занято: <b>нет данных</b>"
	if user.Status == "approved" && user.NCUserID != nil && user.NCPassword != nil {
		storage = a.storageText(user)
	}
	premium := "нет"
	if isPremium(user) {
		premium = "⭐ до " + premiumUntilText(user)
	}
	text := "<b>👤 Пользователь</b>\n\n" +
		"Имя: " + displayName(user) + "\n" +
		fmt.Sprintf("Telegram ID: <code>%d</code>\n", user.TelegramID) +
		"Cloud ID: <code>" + esc(strPtr(user.NCUserID, "-")) + "</code>\n" +
		"Статус: <b>" + esc(user.Status) + "</b>\n" +
		"Премиум: <b>" + esc(premium) + "</b>\n" +
		fmt.Sprintf("Квота: <b>%d GB</b>\n", user.QuotaGB) +
		storage + "\n" +
		"Доступ: <b>" + mapBool(user.IsDisabled == 1, "отключен", "активен") + "</b>"
	return text, userKeyboard(user, backStatus, backPage), nil
}

func (a *App) approveUser(cb *CallbackQuery) {
	id := parseLastInt(cb.Data)
	user, err := a.db.GetUser(id)
	if err != nil || user == nil {
		a.tg.AnswerCallback(cb.ID, "Пользователь не найден", true)
		return
	}
	ncUserID := strconv.FormatInt(id, 10)
	password := generatePassword(18)
	if err := a.nc.EnsureUser(ncUserID, password, a.cfg.DefaultQuotaGB); err != nil {
		a.tg.AnswerCallback(cb.ID, "Ошибка Nextcloud", true)
		_, _ = a.tg.SendMessage(cb.Message.Chat.ID, "Не удалось выдать доступ: <code>"+esc(err.Error())+"</code>", nil)
		return
	}
	if err := a.db.ApproveUser(id, ncUserID, password, a.cfg.DefaultQuotaGB); err != nil {
		a.tg.AnswerCallback(cb.ID, "Ошибка базы", true)
		return
	}
	approved, _ := a.db.GetUser(id)
	_ = a.sendEventSticker(id, "approved")
	_, _ = a.tg.SendMessage(id,
		"✅ <b>Ваша заявка одобрена</b>\n<code>━━━━━━━━━━━━━━━━━━━━</code>\n\n"+
			"🆔 Логин: <code>"+esc(ncUserID)+"</code>\n"+
			"🔐 Пароль: <code>"+esc(password)+"</code>\n"+
			fmt.Sprintf("💾 Квота: <b>%d GB</b>\n\n", a.cfg.DefaultQuotaGB)+
			"📤 Файлы можно отправлять прямо сюда: бот загрузит их в облако.\nПароль всегда виден в /start, там же его можно сменить.",
		accountKeyboard(a.cfg, langOf(approved)),
	)
	a.edit(cb, fmt.Sprintf("✅ Доступ выдан пользователю <code>%d</code>: %d GB.", id, a.cfg.DefaultQuotaGB), adminKeyboard())
	log.Printf("user approved: telegram_id=%d nc_user_id=%s", id, ncUserID)
}

func (a *App) rejectUser(cb *CallbackQuery) {
	id := parseLastInt(cb.Data)
	user, _ := a.db.GetUser(id)
	_ = a.db.RejectUser(id)
	_, _ = a.tg.SendMessage(id, "Ваша заявка на beta-тест сейчас отклонена.", nil)
	a.edit(cb, fmt.Sprintf("❌ Заявка пользователя <code>%d</code> отклонена.", id), adminKeyboard())
	log.Printf("user rejected: telegram_id=%d user=%v", id, user != nil)
}

func (a *App) quotaAdd(cb *CallbackQuery) {
	parts := strings.Split(cb.Data, ":")
	if len(parts) != 3 {
		return
	}
	id, _ := strconv.ParseInt(parts[1], 10, 64)
	amount, _ := strconv.Atoi(parts[2])
	a.addQuotaToUser(cb.Message.Chat.ID, id, amount)
	text, markup, err := a.userDetailsView(id, "all", 0)
	if err == nil {
		a.edit(cb, text, markup)
	}
}

func (a *App) addQuotaToUser(chatID int64, id int64, amount int) {
	user, err := a.db.GetUser(id)
	if err != nil || user == nil || user.NCUserID == nil {
		_, _ = a.tg.SendMessage(chatID, "Пользователь не найден или еще не одобрен.", adminKeyboard())
		return
	}
	newQuota := user.QuotaGB + amount
	if err := a.nc.SetQuota(*user.NCUserID, newQuota); err != nil {
		_, _ = a.tg.SendMessage(chatID, "Не удалось изменить квоту: <code>"+esc(err.Error())+"</code>", adminKeyboard())
		return
	}
	_ = a.db.SetQuota(id, newQuota)
	_, _ = a.tg.SendMessage(chatID, fmt.Sprintf("💾 Квота пользователя <code>%d</code> теперь <b>%d GB</b>.", id, newQuota), adminKeyboard())
}

func (a *App) resetPassword(cb *CallbackQuery) {
	id := parseLastInt(cb.Data)
	user, err := a.db.GetUser(id)
	if err != nil || user == nil || user.NCUserID == nil {
		a.tg.AnswerCallback(cb.ID, "Nextcloud-пользователь еще не создан", true)
		return
	}
	password := generatePassword(18)
	if err := a.nc.SetUserValue(*user.NCUserID, "password", password); err != nil {
		a.tg.AnswerCallback(cb.ID, "Ошибка Nextcloud", true)
		_, _ = a.tg.SendMessage(cb.Message.Chat.ID, "Не удалось сбросить пароль: <code>"+esc(err.Error())+"</code>", nil)
		return
	}
	_ = a.db.SetNextcloudPassword(id, password)
	_, _ = a.tg.SendMessage(id, "🔐 Администратор сбросил пароль для вашего облака.\n\nЛогин: <code>"+esc(*user.NCUserID)+"</code>\nНовый пароль: <code>"+esc(password)+"</code>", accountKeyboard(a.cfg, langOf(user)))
	_, _ = a.tg.SendMessage(cb.Message.Chat.ID, fmt.Sprintf("🔐 Пароль пользователя <code>%d</code> сброшен.", id), adminKeyboard())
}

func (a *App) setSupporter(cb *CallbackQuery) {
	parts := strings.Split(cb.Data, ":")
	if len(parts) != 3 {
		return
	}
	id, _ := strconv.ParseInt(parts[1], 10, 64)
	enabled := parts[2] == "1"
	var until *string
	if enabled {
		value := time.Now().UTC().Add(time.Duration(a.cfg.PremiumDays) * 24 * time.Hour).Format(time.RFC3339)
		until = &value
	}
	_ = a.db.SetSupporter(id, enabled, until)
	text, markup, err := a.userDetailsView(id, "all", 0)
	if err == nil {
		a.edit(cb, text, markup)
	}
}

func (a *App) setEnabled(cb *CallbackQuery, enabled bool) {
	id := parseLastInt(cb.Data)
	user, err := a.db.GetUser(id)
	if err != nil || user == nil || user.NCUserID == nil {
		a.tg.AnswerCallback(cb.ID, "Nextcloud-пользователь еще не создан", true)
		return
	}
	if enabled {
		err = a.nc.EnableUser(*user.NCUserID)
	} else {
		err = a.nc.DisableUser(*user.NCUserID)
	}
	if err != nil {
		a.tg.AnswerCallback(cb.ID, "Ошибка Nextcloud", true)
		_, _ = a.tg.SendMessage(cb.Message.Chat.ID, "Не удалось изменить доступ: <code>"+esc(err.Error())+"</code>", nil)
		return
	}
	_ = a.db.SetDisabled(id, !enabled)
	text, markup, err := a.userDetailsView(id, "all", 0)
	if err == nil {
		a.edit(cb, text, markup)
	}
}

func (a *App) deleteUser(cb *CallbackQuery) {
	id := parseLastInt(cb.Data)
	user, err := a.db.GetUser(id)
	if err != nil || user == nil {
		a.tg.AnswerCallback(cb.ID, "Пользователь уже удален", true)
		return
	}
	if user.NCUserID != nil {
		if err := a.nc.DeleteUser(*user.NCUserID); err != nil {
			a.tg.AnswerCallback(cb.ID, "Ошибка Nextcloud", true)
			_, _ = a.tg.SendMessage(cb.Message.Chat.ID, "Не удалось удалить аккаунт облака: <code>"+esc(err.Error())+"</code>", nil)
			return
		}
	}
	_ = a.db.DeleteUser(id)
	_, _ = a.tg.SendMessage(id, "Ваш beta-доступ к облаку был удален администратором.", nil)
	a.edit(cb, fmt.Sprintf("🗑️ Пользователь <code>%d</code> удален.", id), adminKeyboard())
}
