package main

import (
	"fmt"
	"log"
	"strings"
	"time"
)

func (a *App) accountHome(cb *CallbackQuery) {
	user, err := a.db.GetUser(cb.From.ID)
	if err != nil || user == nil || user.Status != "approved" || user.IsDisabled == 1 {
		a.tg.AnswerCallback(cb.ID, "Доступ не активен", true)
		return
	}
	a.edit(cb, a.accountText(user), accountKeyboard(a.cfg, langOf(user)))
}

func (a *App) accountSupport(cb *CallbackQuery) {
	if !a.cfg.EnableSupportBlock {
		a.tg.AnswerCallback(cb.ID, "Раздел поддержки отключен", true)
		return
	}
	lines := []string{"<b>💬 Поддержка</b>", ""}
	if a.cfg.SupportTelegram != "" {
		tg := strings.TrimSpace(a.cfg.SupportTelegram)
		if strings.HasPrefix(tg, "http://") || strings.HasPrefix(tg, "https://") {
			lines = append(lines, `Telegram: <a href="`+esc(tg)+`">`+esc(tg)+`</a>`)
		} else {
			username := strings.TrimPrefix(tg, "@")
			lines = append(lines, `Telegram: <a href="https://t.me/`+esc(username)+`">@`+esc(username)+`</a>`)
		}
	}
	if a.cfg.SupportEmail != "" {
		lines = append(lines, `Email: <a href="mailto:`+esc(a.cfg.SupportEmail)+`">`+esc(a.cfg.SupportEmail)+`</a>`)
	}
	if len(lines) == 2 {
		lines = append(lines, "Контакты поддержки пока не настроены.")
	}
	_ = a.sendEventSticker(cb.Message.Chat.ID, "support")
	a.edit(cb, strings.Join(lines, "\n"), accountBackKeyboard())
}

func (a *App) accountDonate(cb *CallbackQuery) {
	if !a.cfg.EnableDonateBlock {
		a.tg.AnswerCallback(cb.ID, "Раздел доната отключен", true)
		return
	}
	text := "<b>💙 Поддержать проект</b>\n\nМожно поддержать проект через Telegram Stars или внешнюю ссылку.\nВыберите способ поддержки."
	_ = a.sendEventSticker(cb.Message.Chat.ID, "donate")
	a.edit(cb, text, donateKeyboard(a.cfg))
}

func (a *App) donateCallback(cb *CallbackQuery) {
	if cb.Data == "donate:stars" {
		a.edit(cb, "⭐ <b>Telegram Stars</b>\n\nВыберите сумму в звездах.", starsKeyboard(a.cfg))
		return
	}
	if cb.Data == "donate:platega" {
		if a.platega == nil && a.cfg.PlategaURL == "" {
			a.tg.AnswerCallback(cb.ID, "Platega не настроена", true)
			return
		}
		a.edit(cb, "💳 <b>Platega</b>\n\nВыберите сумму в рублях. Бот создаст платежную ссылку Platega.", plategaKeyboard(a.cfg, a.platega != nil))
		return
	}
	a.tg.AnswerCallback(cb.ID, "Недоступно", true)
}

func (a *App) starsDonate(cb *CallbackQuery) {
	if !a.cfg.EnableDonateBlock || !a.cfg.TelegramStarsEnabled {
		a.tg.AnswerCallback(cb.ID, "Донат отключен", true)
		return
	}
	amount := parseLastInt(cb.Data)
	if !containsInt(a.cfg.TelegramStarsAmounts, int(amount)) {
		a.tg.AnswerCallback(cb.ID, "Недоступная сумма", true)
		return
	}
	payload := fmt.Sprintf("stars_donate:%d:%d", cb.From.ID, amount)
	if err := a.tg.SendInvoice(cb.Message.Chat.ID, "Поддержка проекта", "Спасибо за поддержку проекта!", payload, int(amount)); err != nil {
		a.tg.AnswerCallback(cb.ID, "Не удалось создать счет", true)
		log.Printf("send invoice failed: %v", err)
	}
}

func (a *App) plategaCreate(cb *CallbackQuery) {
	if !a.cfg.EnableDonateBlock || !a.cfg.PlategaEnabled || a.platega == nil {
		a.tg.AnswerCallback(cb.ID, "Platega отключена", true)
		return
	}
	amount := int(parseLastInt(cb.Data))
	if !containsInt(a.cfg.PlategaAmountsRUB, amount) {
		a.tg.AnswerCallback(cb.ID, "Недоступная сумма", true)
		return
	}
	payload := fmt.Sprintf("platega_donate:%d:%d", cb.From.ID, amount)
	payment, err := a.platega.CreatePayment(
		amount,
		fmt.Sprintf("Cloud bot support from Telegram %d", cb.From.ID),
		payload,
		a.cfg.PlategaReturnURL,
		a.cfg.PlategaFailedURL,
	)
	if err != nil {
		a.tg.AnswerCallback(cb.ID, "Не удалось создать платеж", true)
		_, _ = a.tg.SendMessage(cb.Message.Chat.ID, "Не удалось создать платеж Platega: <code>"+esc(err.Error())+"</code>", nil)
		return
	}
	transactionID := fmt.Sprint(payment["transactionId"])
	paymentURL := fmt.Sprint(payment["url"])
	status := strings.ToUpper(fmt.Sprint(payment["status"]))
	if status == "" || status == "<nil>" {
		status = "PENDING"
	}
	_ = a.db.CreatePayment(transactionID, cb.From.ID, "platega", amount, "RUB", status, &paymentURL, &payload)
	a.edit(
		cb,
		fmt.Sprintf("💳 <b>Platega</b>\n\nПлатеж создан. После оплаты нажмите «Проверить оплату».\nID: <code>%s</code>\nСумма: <b>%d RUB</b>", esc(transactionID), amount),
		plategaPaymentKeyboard(paymentURL, transactionID),
	)
}

func (a *App) plategaCheck(cb *CallbackQuery) {
	if a.platega == nil {
		a.tg.AnswerCallback(cb.ID, "Platega не настроена", true)
		return
	}
	transactionID := strings.TrimPrefix(cb.Data, "platega_check:")
	storedPayment, err := a.db.GetPayment(transactionID)
	if err != nil || storedPayment == nil {
		a.tg.AnswerCallback(cb.ID, "Платеж не найден", true)
		return
	}
	if storedPayment.TelegramID != cb.From.ID && !a.isAdmin(cb.From.ID) {
		a.tg.AnswerCallback(cb.ID, "Нет доступа", true)
		return
	}
	payment, err := a.platega.Transaction(transactionID)
	if err != nil {
		a.tg.AnswerCallback(cb.ID, "Не удалось проверить платеж", true)
		_, _ = a.tg.SendMessage(cb.Message.Chat.ID, "Не удалось проверить платеж Platega: <code>"+esc(err.Error())+"</code>", nil)
		return
	}
	status := strings.ToUpper(fmt.Sprint(payment["status"]))
	_ = a.db.UpdatePaymentStatus(transactionID, status)
	if status == "CONFIRMED" {
		until := time.Now().UTC().Add(time.Duration(a.cfg.PremiumDays) * 24 * time.Hour).Format(time.RFC3339)
		_ = a.db.SetSupporter(storedPayment.TelegramID, true, &until)
		_, _ = a.tg.SendMessage(cb.Message.Chat.ID, "⭐ Оплата подтверждена! Премиум-иконка активирована.", accountKeyboard(a.cfg, "ru"))
		a.tg.AnswerCallback(cb.ID, "Оплачено", false)
		return
	}
	if status == "CANCELED" || status == "CHARGEBACKED" || status == "FAILED" || status == "EXPIRED" {
		a.tg.AnswerCallback(cb.ID, "Платеж не активен. Статус: "+status, true)
		return
	}
	a.tg.AnswerCallback(cb.ID, "Платеж пока не подтвержден. Статус: "+status, true)
}

func (a *App) setLanguage(cb *CallbackQuery) {
	lang := strings.TrimPrefix(cb.Data, "lang:")
	if lang != "ru" && lang != "en" {
		return
	}
	_ = a.db.SetLanguage(cb.From.ID, lang)
	user, _ := a.db.GetUser(cb.From.ID)
	if user != nil {
		a.edit(cb, a.accountText(user), accountKeyboard(a.cfg, lang))
	}
}

func (a *App) applyUserPassword(msg *Message) {
	user, err := a.db.GetUser(msg.From.ID)
	if err != nil || user == nil || user.Status != "approved" || user.NCUserID == nil {
		a.states.Clear(msg.From.ID)
		_, _ = a.tg.SendMessage(msg.Chat.ID, "Доступ не активен.", nil)
		return
	}
	password := strings.TrimSpace(msg.Text)
	if len(password) < 8 || len(password) > 128 {
		_, _ = a.tg.SendMessage(msg.Chat.ID, "Пароль должен быть от 8 до 128 символов.", accountBackKeyboard())
		return
	}
	if err := a.nc.SetUserValue(*user.NCUserID, "password", password); err != nil {
		_, _ = a.tg.SendMessage(msg.Chat.ID, "⚠️ Не удалось сменить пароль: <code>"+esc(err.Error())+"</code>", accountBackKeyboard())
		return
	}
	_ = a.db.SetNextcloudPassword(user.TelegramID, password)
	a.states.Clear(msg.From.ID)
	_ = a.sendEventSticker(msg.Chat.ID, "password")
	_, _ = a.tg.SendMessage(msg.Chat.ID, "✅ Пароль сменен.\n\nЛогин: <code>"+esc(*user.NCUserID)+"</code>\nПароль: <code>"+esc(password)+"</code>", accountKeyboard(a.cfg, langOf(user)))
}

func (a *App) handleSuccessfulPayment(msg *Message) {
	payload := msg.SuccessfulPayment.InvoicePayload
	if !strings.HasPrefix(payload, "stars_donate:") {
		return
	}
	until := time.Now().UTC().Add(time.Duration(a.cfg.PremiumDays) * 24 * time.Hour).Format(time.RFC3339)
	_ = a.db.SetSupporter(msg.From.ID, true, &until)
	_ = a.db.CreatePayment(msg.SuccessfulPayment.TelegramPaymentChargeID, msg.From.ID, "telegram_stars", msg.SuccessfulPayment.TotalAmount, "XTR", "CONFIRMED", nil, &payload)
	_, _ = a.tg.SendMessage(msg.Chat.ID, "⭐ Спасибо за поддержку! Премиум-иконка активирована.", accountKeyboard(a.cfg, "ru"))
}

