package main

import (
	"fmt"
	"path/filepath"
	"strconv"
)

func adminKeyboard() *InlineKeyboardMarkup {
	return keyboard([][]InlineKeyboardButton{
		{{Text: "👥 Пользователи", CallbackData: "users:menu"}, {Text: "📝 Заявки", CallbackData: "users:requested:0"}},
		{{Text: "📊 Статистика", CallbackData: "stats"}, {Text: "☁️ Админ cloud", CallbackData: "admincloud"}},
		{{Text: "💾 Продажи", CallbackData: "commerce"}, {Text: "🎟 Промокоды", CallbackData: "promos"}},
		{{Text: "📣 Рассылка", CallbackData: "broadcast"}, {Text: "🔄 Синхр./восстановление", CallbackData: "maintenance"}},
		{{Text: "✏️ Тексты и кнопки", CallbackData: "content"}, {Text: "✨ Стикеры", CallbackData: "stickers"}},
	})
}

func backAdminKeyboard() *InlineKeyboardMarkup {
	return keyboard([][]InlineKeyboardButton{{{Text: "🛠️ В админку", CallbackData: "admin"}}})
}

func requestReviewKeyboard(id int64) *InlineKeyboardMarkup {
	return keyboard([][]InlineKeyboardButton{{{Text: "✅ Одобрить", CallbackData: fmt.Sprintf("approve:%d", id)}, {Text: "❌ Отклонить", CallbackData: fmt.Sprintf("reject:%d", id)}}})
}

func (a *App) accountKeyboard(lang string) *InlineKeyboardMarkup {
	suffix := "ru"
	if lang == "en" {
		suffix = "en"
	}
	cloud := a.content.Button("cloud_" + suffix)
	changePassword := a.content.Button("change_password_" + suffix)
	support := a.content.Button("support_" + suffix)
	donate := a.content.Button("donate_" + suffix)
	buyStorage := a.content.Button("buy_storage_" + suffix)
	promo := a.content.Button("promo_" + suffix)
	info := a.content.Button("info_" + suffix)
	language := a.content.Button("language_" + suffix)
	rows := [][]InlineKeyboardButton{{{Text: cloud, URL: a.cfg.NextcloudURL}}, {{Text: changePassword, CallbackData: "account:change_password"}}}
	rows = append(rows, []InlineKeyboardButton{{Text: buyStorage, CallbackData: "account:buy_storage"}, {Text: promo, CallbackData: "account:promo"}})
	if a.cfg.EnableSupportBlock {
		rows = append(rows, []InlineKeyboardButton{{Text: support, CallbackData: "account:support"}})
	}
	if a.cfg.EnableDonateBlock {
		rows = append(rows, []InlineKeyboardButton{{Text: donate, CallbackData: "account:donate"}})
	}
	rows = append(rows, []InlineKeyboardButton{{Text: info, CallbackData: "account:info"}, {Text: language, CallbackData: "account:language"}})
	return keyboard(rows)
}

func accountBackKeyboard() *InlineKeyboardMarkup {
	return keyboard([][]InlineKeyboardButton{{{Text: "⬅️ Назад", CallbackData: "account:home"}}})
}

func languageKeyboard() *InlineKeyboardMarkup {
	return keyboard([][]InlineKeyboardButton{{{Text: "🇷🇺 Русский", CallbackData: "lang:ru"}, {Text: "🇬🇧 English", CallbackData: "lang:en"}}, {{Text: "⬅️ Назад", CallbackData: "account:home"}}})
}

func donateKeyboard(cfg Config) *InlineKeyboardMarkup {
	rows := [][]InlineKeyboardButton{}
	if cfg.TelegramStarsEnabled && len(cfg.TelegramStarsAmounts) > 0 {
		rows = append(rows, []InlineKeyboardButton{{Text: "⭐ Telegram Stars", CallbackData: "donate:stars"}})
	}
	if cfg.PlategaVisible() {
		rows = append(rows, []InlineKeyboardButton{{Text: "💳 Platega", CallbackData: "donate:platega"}})
	}
	providers := []InlineKeyboardButton{}
	if cfg.PallyVisible() {
		providers = append(providers, InlineKeyboardButton{Text: "💳 Pally", CallbackData: "donate:pally"})
	}
	if cfg.CryptoBotVisible() {
		providers = append(providers, InlineKeyboardButton{Text: "🪙 CryptoBot", CallbackData: "donate:cryptobot"})
	}
	if len(providers) > 0 {
		rows = append(rows, providers)
	}
	if cfg.HeleketVisible() {
		rows = append(rows, []InlineKeyboardButton{{Text: "₿ Heleket", CallbackData: "donate:heleket"}})
	}
	if cfg.DonateURL != "" {
		rows = append(rows, []InlineKeyboardButton{{Text: "💙 Донат", URL: cfg.DonateURL}})
	}
	rows = append(rows, []InlineKeyboardButton{{Text: "⬅️ Назад", CallbackData: "account:home"}})
	return keyboard(rows)
}

func (cfg Config) PlategaVisible() bool {
	return cfg.PlategaEnabled && (cfg.PlategaPaymentURL() != "" || (cfg.PlategaMerchantID != "" && cfg.PlategaSecret != ""))
}

func (cfg Config) PallyVisible() bool {
	return cfg.PallyEnabled && cfg.PallyToken != "" && cfg.PallyShopID != ""
}

func (cfg Config) CryptoBotVisible() bool {
	return cfg.CryptoBotEnabled && cfg.CryptoBotToken != ""
}

func (cfg Config) HeleketVisible() bool {
	return cfg.HeleketEnabled && cfg.HeleketMerchantID != "" && cfg.HeleketAPIKey != ""
}

func (cfg Config) PlategaPaymentURL() string {
	if cfg.PlategaEnabled {
		return cfg.PlategaURL
	}
	return ""
}

func starsKeyboard(cfg Config) *InlineKeyboardMarkup {
	rows := [][]InlineKeyboardButton{}
	row := []InlineKeyboardButton{}
	for _, amount := range cfg.TelegramStarsAmounts {
		row = append(row, InlineKeyboardButton{Text: fmt.Sprintf("⭐ %d", amount), CallbackData: fmt.Sprintf("stars:%d", amount)})
		if len(row) == 3 {
			rows = append(rows, row)
			row = []InlineKeyboardButton{}
		}
	}
	if len(row) > 0 {
		rows = append(rows, row)
	}
	rows = append(rows, []InlineKeyboardButton{{Text: "⬅️ Назад", CallbackData: "account:donate"}})
	return keyboard(rows)
}

func plategaKeyboard(cfg Config, apiEnabled bool) *InlineKeyboardMarkup {
	rows := [][]InlineKeyboardButton{}
	if apiEnabled {
		row := []InlineKeyboardButton{}
		for _, amount := range cfg.PlategaAmountsRUB {
			row = append(row, InlineKeyboardButton{Text: fmt.Sprintf("💳 %d RUB", amount), CallbackData: fmt.Sprintf("platega:%d", amount)})
			if len(row) == 2 {
				rows = append(rows, row)
				row = []InlineKeyboardButton{}
			}
		}
		if len(row) > 0 {
			rows = append(rows, row)
		}
	}
	if cfg.PlategaPaymentURL() != "" {
		rows = append(rows, []InlineKeyboardButton{{Text: "💳 Оплатить", URL: cfg.PlategaPaymentURL()}})
	}
	rows = append(rows, []InlineKeyboardButton{{Text: "⬅️ Назад", CallbackData: "account:donate"}})
	return keyboard(rows)
}

func paymentAmountKeyboard(prefix string, amounts []int, back string) *InlineKeyboardMarkup {
	rows := [][]InlineKeyboardButton{}
	row := []InlineKeyboardButton{}
	for _, amount := range amounts {
		row = append(row, InlineKeyboardButton{Text: fmt.Sprintf("💳 %d RUB", amount), CallbackData: fmt.Sprintf("%s:%d", prefix, amount)})
		if len(row) == 2 {
			rows = append(rows, row)
			row = []InlineKeyboardButton{}
		}
	}
	if len(row) > 0 {
		rows = append(rows, row)
	}
	rows = append(rows, []InlineKeyboardButton{{Text: "⬅️ Назад", CallbackData: back}})
	return keyboard(rows)
}

func plategaPaymentKeyboard(paymentURL, transactionID string) *InlineKeyboardMarkup {
	return paymentLinkKeyboard(paymentURL, transactionID, "donate:platega")
}

func paymentLinkKeyboard(paymentURL, transactionID, back string) *InlineKeyboardMarkup {
	return keyboard([][]InlineKeyboardButton{
		{{Text: "💳 Оплатить", URL: paymentURL}},
		{{Text: "🔎 Проверить оплату", CallbackData: "platega_check:" + transactionID}},
		{{Text: "⬅️ Назад", CallbackData: back}},
	})
}

func buyStorageKeyboard(cfg Config) *InlineKeyboardMarkup {
	rows := [][]InlineKeyboardButton{}
	if cfg.PlategaVisible() {
		rows = append(rows, []InlineKeyboardButton{{Text: "💳 Купить через Platega", CallbackData: "storage_buy:platega"}})
	}
	if cfg.PallyVisible() {
		rows = append(rows, []InlineKeyboardButton{{Text: "💳 Купить через Pally", CallbackData: "storage_buy:pally"}})
	}
	if cfg.CryptoBotVisible() {
		rows = append(rows, []InlineKeyboardButton{{Text: "🪙 Купить через CryptoBot", CallbackData: "storage_buy:cryptobot"}})
	}
	if cfg.HeleketVisible() {
		rows = append(rows, []InlineKeyboardButton{{Text: "₿ Купить через Heleket", CallbackData: "storage_buy:heleket"}})
	}
	rows = append(rows, []InlineKeyboardButton{{Text: "⬅️ Назад", CallbackData: "account:home"}})
	return keyboard(rows)
}

func promoApplyKeyboard() *InlineKeyboardMarkup {
	return keyboard([][]InlineKeyboardButton{{{Text: "⬅️ Назад", CallbackData: "account:home"}}})
}

func usersMenuKeyboard() *InlineKeyboardMarkup {
	return keyboard([][]InlineKeyboardButton{
		{{Text: "👥 Все", CallbackData: "users:all:0"}, {Text: "✅ Одобренные", CallbackData: "users:approved:0"}},
		{{Text: "📝 Заявки", CallbackData: "users:requested:0"}, {Text: "❌ Отклоненные", CallbackData: "users:rejected:0"}},
		{{Text: "🔎 Поиск", CallbackData: "users:search"}},
		{{Text: "🛠️ В админку", CallbackData: "admin"}},
	})
}

func usersKeyboard(users []User, status string, page int, hasNext bool) *InlineKeyboardMarkup {
	rows := [][]InlineKeyboardButton{}
	for _, user := range users {
		name := strPtr(user.Username, strPtr(user.FirstName, strconv.FormatInt(user.TelegramID, 10)))
		label := fmt.Sprintf("%s | %s | %dGB", name, user.Status, user.QuotaGB)
		if len([]rune(label)) > 60 {
			label = string([]rune(label)[:60])
		}
		rows = append(rows, []InlineKeyboardButton{{Text: label, CallbackData: fmt.Sprintf("user:%d:%s:%d", user.TelegramID, status, page)}})
	}
	nav := []InlineKeyboardButton{}
	if page > 0 {
		nav = append(nav, InlineKeyboardButton{Text: "⬅️ Назад", CallbackData: fmt.Sprintf("users:%s:%d", status, page-1)})
	}
	if hasNext {
		nav = append(nav, InlineKeyboardButton{Text: "➡️ Вперед", CallbackData: fmt.Sprintf("users:%s:%d", status, page+1)})
	}
	if len(nav) > 0 {
		rows = append(rows, nav)
	}
	rows = append(rows, []InlineKeyboardButton{{Text: "🔎 Поиск", CallbackData: "users:search"}, {Text: "⬅️ Пользователи", CallbackData: "users:menu"}})
	return keyboard(rows)
}

func userKeyboard(user *User, backStatus string, backPage int) *InlineKeyboardMarkup {
	rows := [][]InlineKeyboardButton{}
	if user.Status == "requested" || user.Status == "rejected" {
		rows = append(rows, []InlineKeyboardButton{{Text: "✅ Одобрить", CallbackData: fmt.Sprintf("approve:%d", user.TelegramID)}})
	}
	if user.Status == "requested" {
		rows = append(rows, []InlineKeyboardButton{{Text: "❌ Отклонить", CallbackData: fmt.Sprintf("reject:%d", user.TelegramID)}})
	}
	if user.Status == "approved" {
		rows = append(rows, []InlineKeyboardButton{
			{Text: "➕ 1GB", CallbackData: fmt.Sprintf("quotaadd:%d:1", user.TelegramID)},
			{Text: "➕ 5GB", CallbackData: fmt.Sprintf("quotaadd:%d:5", user.TelegramID)},
			{Text: "➕ 10GB", CallbackData: fmt.Sprintf("quotaadd:%d:10", user.TelegramID)},
		})
		rows = append(rows, []InlineKeyboardButton{{Text: "⚙️ Другое", CallbackData: fmt.Sprintf("quotacustom:%d", user.TelegramID)}, {Text: "🔐 Сбросить пароль", CallbackData: fmt.Sprintf("resetpass:%d", user.TelegramID)}})
		if isPremium(user) {
			rows = append(rows, []InlineKeyboardButton{{Text: "⭐ Убрать премиум", CallbackData: fmt.Sprintf("supporter:%d:0", user.TelegramID)}})
		} else {
			rows = append(rows, []InlineKeyboardButton{{Text: "⭐ Сделать премиум", CallbackData: fmt.Sprintf("supporter:%d:1", user.TelegramID)}})
		}
		if user.IsDisabled == 1 {
			rows = append(rows, []InlineKeyboardButton{{Text: "🟢 Включить", CallbackData: fmt.Sprintf("enable:%d", user.TelegramID)}})
		} else {
			rows = append(rows, []InlineKeyboardButton{{Text: "🔴 Отключить", CallbackData: fmt.Sprintf("disable:%d", user.TelegramID)}})
		}
		rows = append(rows, []InlineKeyboardButton{{Text: "🗑️ Удалить", CallbackData: fmt.Sprintf("deleteask:%d", user.TelegramID)}})
	}
	rows = append(rows, []InlineKeyboardButton{{Text: "⬅️ Назад", CallbackData: fmt.Sprintf("users:%s:%d", backStatus, backPage)}})
	return keyboard(rows)
}

func deleteConfirmKeyboard(id int64) *InlineKeyboardMarkup {
	return keyboard([][]InlineKeyboardButton{{{Text: "🗑️ Да, удалить", CallbackData: fmt.Sprintf("deleteyes:%d", id)}}, {{Text: "⬅️ Отмена", CallbackData: fmt.Sprintf("user:%d:all:0", id)}}})
}

func backupKeyboard() *InlineKeyboardMarkup {
	return keyboard([][]InlineKeyboardButton{
		{{Text: "🗄️ Создать PostgreSQL", CallbackData: "backup:db"}, {Text: "📦 Создать JSON", CallbackData: "backup:json"}},
		{{Text: "📋 Список", CallbackData: "backup:list"}, {Text: "♻️ Восстановить", CallbackData: "backup:restore"}},
		{{Text: "⬅️ Сервис", CallbackData: "maintenance"}},
	})
}

func maintenanceKeyboard() *InlineKeyboardMarkup {
	return keyboard([][]InlineKeyboardButton{
		{{Text: "🔄 Синхронизировать пользователей", CallbackData: "sync"}},
		{{Text: "☁️ Проверить мой клауд", CallbackData: "admincloud"}},
		{{Text: "🗄️ Бекапы и восстановление", CallbackData: "backup"}},
		{{Text: "🛠️ В админку", CallbackData: "admin"}},
	})
}

func commerceKeyboard() *InlineKeyboardMarkup {
	return keyboard([][]InlineKeyboardButton{
		{{Text: "💾 GB в пакете", CallbackData: "set:storage_pack_gb"}, {Text: "💰 Цена RUB", CallbackData: "set:storage_pack_price_rub"}},
		{{Text: "⭐ Скидка %", CallbackData: "set:premium_discount_percent"}, {Text: "📝 Описание премиума", CallbackData: "content:message:premium_info"}},
		{{Text: "🎁 Триал on/off", CallbackData: "set:trial_enabled"}, {Text: "🎁 Квота триала", CallbackData: "set:trial_quota_gb"}},
		{{Text: "⭐ Дней премиума триала", CallbackData: "set:trial_premium_days"}},
		{{Text: "🛠️ Техработы on/off", CallbackData: "set:maintenance_enabled"}},
		{{Text: "🛠️ В админку", CallbackData: "admin"}},
	})
}

func promosKeyboard() *InlineKeyboardMarkup {
	return keyboard([][]InlineKeyboardButton{
		{{Text: "➕ Создать промокод", CallbackData: "promo:create"}},
		{{Text: "🛠️ В админку", CallbackData: "admin"}},
	})
}

func contentKeyboard() *InlineKeyboardMarkup {
	return keyboard([][]InlineKeyboardButton{
		{{Text: "💬 Сообщения", CallbackData: "content:messages"}, {Text: "🔘 Кнопки", CallbackData: "content:buttons"}},
		{{Text: "🧾 Форматирование", CallbackData: "content:formatting"}},
		{{Text: "🛠️ В админку", CallbackData: "admin"}},
	})
}

func contentListKeyboard(kind string) *InlineKeyboardMarkup {
	rows := [][]InlineKeyboardButton{}
	items := contentMessages
	if kind == "button" {
		items = contentButtons
	}
	for _, key := range sortedContentKeys(items) {
		rows = append(rows, []InlineKeyboardButton{{Text: contentTitle(items, key), CallbackData: "content:" + kind + ":" + key}})
	}
	rows = append(rows, []InlineKeyboardButton{{Text: "⬅️ Тексты", CallbackData: "content"}})
	return keyboard(rows)
}

func contentEditKeyboard(kind, key string) *InlineKeyboardMarkup {
	rows := [][]InlineKeyboardButton{
		{{Text: "✏️ Изменить", CallbackData: "content:set:" + kind + ":" + key}},
		{{Text: "↩️ Сбросить", CallbackData: "content:reset:" + kind + ":" + key}},
	}
	if kind == "message" {
		rows = append(rows, []InlineKeyboardButton{{Text: "🖼 Фото", CallbackData: "content:photo:" + key}, {Text: "🧹 Убрать фото", CallbackData: "content:photoreset:" + key}})
	}
	rows = append(rows, []InlineKeyboardButton{{Text: "⬅️ Назад", CallbackData: "content:" + mapBool(kind == "message", "messages", "buttons")}})
	return keyboard(rows)
}

func stickersKeyboard(store *StickerStore, packURL string) *InlineKeyboardMarkup {
	rows := [][]InlineKeyboardButton{}
	for _, event := range stickerEvents {
		mark := eventMark(event)
		if value, ok := store.Get(event); ok {
			if value.Kind == StickerKindCustomEmoji {
				mark = "🧩"
			} else {
				mark = "🖼️"
			}
		}
		rows = append(rows, []InlineKeyboardButton{{Text: mark + " " + event, CallbackData: "sticker:event:" + event}})
	}
	if packURL != "" {
		rows = append(rows, []InlineKeyboardButton{{Text: "🧪 Открыть CPT_Emoji", URL: packURL}})
	}
	rows = append(rows, []InlineKeyboardButton{{Text: "🛠️ В админку", CallbackData: "admin"}})
	return keyboard(rows)
}

func stickerEventKeyboard(event string, hasValue bool, packURL string) *InlineKeyboardMarkup {
	rows := [][]InlineKeyboardButton{
		{{Text: "➕ Установить", CallbackData: "sticker:set:" + event}},
	}
	if hasValue {
		rows = append(rows, []InlineKeyboardButton{
			{Text: "👁️ Предпросмотр", CallbackData: "sticker:preview:" + event},
			{Text: "🧹 Очистить", CallbackData: "sticker:clear:" + event},
		})
	}
	if packURL != "" {
		rows = append(rows, []InlineKeyboardButton{{Text: "🧪 Открыть CPT_Emoji", URL: packURL}})
	}
	rows = append(rows, []InlineKeyboardButton{{Text: "⬅️ Стикеры", CallbackData: "stickers"}})
	return keyboard(rows)
}

func restoreBackupKeyboard(files []string) *InlineKeyboardMarkup {
	rows := [][]InlineKeyboardButton{}
	for index, file := range files {
		label := filepath.Base(file)
		if len([]rune(label)) > 60 {
			label = string([]rune(label)[:60])
		}
		rows = append(rows, []InlineKeyboardButton{{Text: label, CallbackData: fmt.Sprintf("restore:%d", index)}})
	}
	rows = append(rows, []InlineKeyboardButton{{Text: "⬅️ Отмена", CallbackData: "backup"}})
	return keyboard(rows)
}

func keyboard(rows [][]InlineKeyboardButton) *InlineKeyboardMarkup {
	return &InlineKeyboardMarkup{InlineKeyboard: rows}
}
