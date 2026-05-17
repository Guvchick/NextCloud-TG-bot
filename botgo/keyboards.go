package main

import (
	"fmt"
	"path/filepath"
	"strconv"
)

func adminKeyboard() *InlineKeyboardMarkup {
	return keyboard([][]InlineKeyboardButton{
		{{Text: "👥 Пользователи", CallbackData: "users:all:0"}, {Text: "🔎 Поиск", CallbackData: "admin:search"}},
		{{Text: "📝 Заявки", CallbackData: "users:requested:0"}, {Text: "🗄️ Бекапы", CallbackData: "backup"}},
		{{Text: "📣 Рассылка", CallbackData: "broadcast"}, {Text: "🔄 Синхронизация", CallbackData: "sync"}},
		{{Text: "✨ Стикеры", CallbackData: "stickers"}},
	})
}

func backAdminKeyboard() *InlineKeyboardMarkup {
	return keyboard([][]InlineKeyboardButton{{{Text: "🛠️ В админку", CallbackData: "admin"}}})
}

func requestReviewKeyboard(id int64) *InlineKeyboardMarkup {
	return keyboard([][]InlineKeyboardButton{{{Text: "✅ Одобрить", CallbackData: fmt.Sprintf("approve:%d", id)}, {Text: "❌ Отклонить", CallbackData: fmt.Sprintf("reject:%d", id)}}})
}

func accountKeyboard(cfg Config, lang string) *InlineKeyboardMarkup {
	changePassword := "🔐 Сменить пароль"
	support := "💬 Поддержка"
	donate := "💙 Донат"
	language := "🌐 Язык"
	cloud := "☁️ Войти в облако"
	if lang == "en" {
		changePassword = "🔐 Change password"
		support = "💬 Support"
		donate = "💙 Donate"
		language = "🌐 Language"
		cloud = "☁️ Open cloud"
	}
	rows := [][]InlineKeyboardButton{{{Text: cloud, URL: cfg.NextcloudURL}}, {{Text: changePassword, CallbackData: "account:change_password"}}}
	if cfg.EnableSupportBlock {
		rows = append(rows, []InlineKeyboardButton{{Text: support, CallbackData: "account:support"}})
	}
	if cfg.EnableDonateBlock {
		rows = append(rows, []InlineKeyboardButton{{Text: donate, CallbackData: "account:donate"}})
	}
	rows = append(rows, []InlineKeyboardButton{{Text: language, CallbackData: "account:language"}})
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
	if cfg.DonateURL != "" {
		rows = append(rows, []InlineKeyboardButton{{Text: "💙 Донат", URL: cfg.DonateURL}})
	}
	rows = append(rows, []InlineKeyboardButton{{Text: "⬅️ Назад", CallbackData: "account:home"}})
	return keyboard(rows)
}

func (cfg Config) PlategaVisible() bool {
	return cfg.PlategaEnabled && (cfg.PlategaPaymentURL() != "" || (cfg.PlategaMerchantID != "" && cfg.PlategaSecret != ""))
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

func plategaPaymentKeyboard(paymentURL, transactionID string) *InlineKeyboardMarkup {
	return keyboard([][]InlineKeyboardButton{
		{{Text: "💳 Оплатить", URL: paymentURL}},
		{{Text: "🔎 Проверить оплату", CallbackData: "platega_check:" + transactionID}},
		{{Text: "⬅️ Назад", CallbackData: "donate:platega"}},
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
	rows = append(rows, []InlineKeyboardButton{{Text: "🛠️ В админку", CallbackData: "admin"}})
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
		{{Text: "🛠️ В админку", CallbackData: "admin"}},
	})
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

