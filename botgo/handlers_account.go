package main

import (
	"errors"
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
	a.edit(cb, a.accountText(user), a.accountKeyboard(langOf(user)))
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
	contacts := strings.Join(lines[2:], "\n")
	if a.content.Photo("support") != "" {
		_, _ = a.sendContent(cb.Message.Chat.ID, "support", map[string]string{"support_contacts": contacts}, accountBackKeyboard())
		return
	}
	a.edit(cb, a.content.Message("support", map[string]string{"support_contacts": contacts}), accountBackKeyboard())
}

func (a *App) accountDonate(cb *CallbackQuery) {
	if !a.cfg.EnableDonateBlock {
		a.tg.AnswerCallback(cb.ID, "Раздел доната отключен", true)
		return
	}
	_ = a.sendEventSticker(cb.Message.Chat.ID, "donate")
	text := a.content.Message("donate", nil) + "\n\n" + a.content.Message("premium_info", nil)
	if a.content.Photo("donate") != "" {
		_, _ = a.sendContent(cb.Message.Chat.ID, "donate", nil, donateKeyboard(a.cfg))
		_, _ = a.tg.SendMessage(cb.Message.Chat.ID, a.content.Message("premium_info", nil), nil)
		return
	}
	a.edit(cb, text, donateKeyboard(a.cfg))
}

func (a *App) accountBuyStorage(cb *CallbackQuery) {
	user, err := a.db.GetUser(cb.From.ID)
	if err != nil || user == nil || user.Status != "approved" {
		a.tg.AnswerCallback(cb.ID, "Доступ не активен", true)
		return
	}
	price := a.discountedStoragePrice(user)
	text := fmt.Sprintf("💾 <b>Докупить место</b>\n\nПакет: <b>%d GB</b>\nЦена: <b>%d RUB</b>", a.storagePackGB(), price)
	if isPremium(user) && a.premiumDiscount() > 0 {
		text += fmt.Sprintf("\n⭐ Ваша премиум-скидка: <b>%d%%</b>", a.premiumDiscount())
	}
	a.edit(cb, text, buyStorageKeyboard(a.cfg))
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
	if cb.Data == "donate:pally" {
		if a.pally == nil {
			a.tg.AnswerCallback(cb.ID, "Pally не настроен", true)
			return
		}
		a.edit(cb, "💳 <b>Pally</b>\n\nВыберите сумму в рублях.", paymentAmountKeyboard("pally", a.cfg.PlategaAmountsRUB, "account:donate"))
		return
	}
	if cb.Data == "donate:cryptobot" {
		if a.cryptoBot == nil {
			a.tg.AnswerCallback(cb.ID, "CryptoBot не настроен", true)
			return
		}
		a.edit(cb, "🪙 <b>CryptoBot</b>\n\nВыберите сумму в рублях.", paymentAmountKeyboard("cryptobot", a.cfg.PlategaAmountsRUB, "account:donate"))
		return
	}
	if cb.Data == "donate:heleket" {
		if a.heleket == nil {
			a.tg.AnswerCallback(cb.ID, "Heleket не настроен", true)
			return
		}
		a.edit(cb, "₿ <b>Heleket</b>\n\nВыберите сумму в рублях.", paymentAmountKeyboard("heleket", a.cfg.PlategaAmountsRUB, "account:donate"))
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
		a.cfg.PlategaCallbackURL,
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

func (a *App) storageBuyCallback(cb *CallbackQuery) {
	if !a.cfg.EnableDonateBlock {
		a.tg.AnswerCallback(cb.ID, "Оплата отключена", true)
		return
	}
	user, err := a.db.GetUser(cb.From.ID)
	if err != nil || user == nil || user.Status != "approved" {
		a.tg.AnswerCallback(cb.ID, "Доступ не активен", true)
		return
	}
	gb := a.storagePackGB()
	price := a.discountedStoragePrice(user)
	payload := fmt.Sprintf("storage:%d:%d:%d", cb.From.ID, gb, price)
	provider := strings.TrimPrefix(cb.Data, "storage_buy:")
	payment, err := a.createExternalPayment(provider, price, fmt.Sprintf("Buy %d GB for Telegram %d", gb, cb.From.ID), payload)
	if err != nil {
		a.tg.AnswerCallback(cb.ID, "Не удалось создать платеж", true)
		_, _ = a.tg.SendMessage(cb.Message.Chat.ID, "Не удалось создать платеж: <code>"+esc(err.Error())+"</code>", nil)
		return
	}
	transactionID := fmt.Sprint(payment["transactionId"])
	paymentURL := fmt.Sprint(payment["url"])
	status := strings.ToUpper(fmt.Sprint(payment["status"]))
	if status == "" || status == "<nil>" {
		status = "PENDING"
	}
	_ = a.db.CreatePayment(transactionID, cb.From.ID, provider, price, "RUB", status, &paymentURL, &payload)
	a.edit(cb, fmt.Sprintf("💾 <b>Покупка места</b>\n\nПакет: <b>%d GB</b>\nСумма: <b>%d RUB</b>\nID: <code>%s</code>", gb, price, esc(transactionID)), paymentLinkKeyboard(paymentURL, transactionID, "account:buy_storage"))
}

func (a *App) externalDonateCreate(cb *CallbackQuery, provider string) {
	if !a.cfg.EnableDonateBlock {
		a.tg.AnswerCallback(cb.ID, "Донат отключен", true)
		return
	}
	amount := int(parseLastInt(cb.Data))
	if !containsInt(a.cfg.PlategaAmountsRUB, amount) {
		a.tg.AnswerCallback(cb.ID, "Недоступная сумма", true)
		return
	}
	payload := fmt.Sprintf("%s_donate:%d:%d", provider, cb.From.ID, amount)
	payment, err := a.createExternalPayment(provider, amount, fmt.Sprintf("Cloud bot support from Telegram %d", cb.From.ID), payload)
	if err != nil {
		a.tg.AnswerCallback(cb.ID, "Не удалось создать платеж", true)
		_, _ = a.tg.SendMessage(cb.Message.Chat.ID, "Не удалось создать платеж: <code>"+esc(err.Error())+"</code>", nil)
		return
	}
	transactionID := fmt.Sprint(payment["transactionId"])
	paymentURL := fmt.Sprint(payment["url"])
	status := strings.ToUpper(fmt.Sprint(payment["status"]))
	if status == "" || status == "<nil>" {
		status = "PENDING"
	}
	_ = a.db.CreatePayment(transactionID, cb.From.ID, provider, amount, "RUB", status, &paymentURL, &payload)
	a.edit(cb, fmt.Sprintf("💳 <b>%s</b>\n\nПлатеж создан.\nID: <code>%s</code>\nСумма: <b>%d RUB</b>", esc(provider), esc(transactionID), amount), paymentLinkKeyboard(paymentURL, transactionID, "donate:"+provider))
}

func (a *App) createExternalPayment(provider string, amount int, description, payload string) (map[string]any, error) {
	switch provider {
	case "platega":
		if a.platega == nil {
			return nil, errors.New("Platega is not configured")
		}
		return a.platega.CreatePayment(amount, description, payload, a.cfg.PlategaReturnURL, a.cfg.PlategaFailedURL, a.cfg.PlategaCallbackURL)
	case "pally":
		if a.pally == nil {
			return nil, errors.New("Pally is not configured")
		}
		return a.pally.CreateBill(amount, description, payload)
	case "cryptobot":
		if a.cryptoBot == nil {
			return nil, errors.New("CryptoBot is not configured")
		}
		return a.cryptoBot.CreateInvoice(amount, description, payload, a.cfg.PlategaReturnURL)
	case "heleket":
		if a.heleket == nil {
			return nil, errors.New("Heleket is not configured")
		}
		return a.heleket.CreatePayment(amount, a.cfg.HeleketCurrency, a.cfg.HeleketToCurrency, a.publicWebhookURL(a.cfg.HeleketWebhookPath), a.cfg.PlategaReturnURL, payload)
	default:
		return nil, errors.New("unknown payment provider")
	}
}

func (a *App) externalPaymentCheck(cb *CallbackQuery) {
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
	if storedPayment.Status == "FULFILLED" {
		a.tg.AnswerCallback(cb.ID, "Уже выдано", false)
		return
	}
	status, err := a.checkExternalPayment(storedPayment)
	if err != nil {
		a.tg.AnswerCallback(cb.ID, "Не удалось проверить платеж", true)
		_, _ = a.tg.SendMessage(cb.Message.Chat.ID, "Не удалось проверить платеж: <code>"+esc(err.Error())+"</code>", nil)
		return
	}
	_ = a.db.UpdatePaymentStatus(transactionID, status)
	if status == "CONFIRMED" {
		if err := a.fulfillPayment(transactionID); err != nil {
			a.tg.AnswerCallback(cb.ID, "Платеж подтвержден, но выдача не удалась", true)
			_, _ = a.tg.SendMessage(cb.Message.Chat.ID, "⚠️ Платеж подтвержден, но выдача не удалась: <code>"+esc(err.Error())+"</code>", nil)
			return
		}
		a.tg.AnswerCallback(cb.ID, "Оплачено", false)
		return
	}
	if status == "CANCELED" || status == "CHARGEBACKED" || status == "FAILED" || status == "EXPIRED" {
		a.tg.AnswerCallback(cb.ID, "Платеж не активен. Статус: "+status, true)
		return
	}
	a.tg.AnswerCallback(cb.ID, "Платеж пока не подтвержден. Статус: "+status, true)
}

func (a *App) checkExternalPayment(payment *Payment) (string, error) {
	switch payment.Provider {
	case "platega":
		if a.platega == nil {
			return "", errors.New("Platega is not configured")
		}
		data, err := a.platega.Transaction(payment.TransactionID)
		if err != nil {
			return "", err
		}
		return strings.ToUpper(fmt.Sprint(data["status"])), nil
	case "cryptobot":
		if a.cryptoBot == nil {
			return "", errors.New("CryptoBot is not configured")
		}
		data, err := a.cryptoBot.Invoice(payment.TransactionID)
		if err != nil {
			return "", err
		}
		if strings.ToLower(firstMapString(data, "status")) == "paid" {
			return "CONFIRMED", nil
		}
		return strings.ToUpper(firstMapString(data, "status")), nil
	case "heleket":
		if a.heleket == nil {
			return "", errors.New("Heleket is not configured")
		}
		data, err := a.heleket.PaymentInfo(payment.TransactionID)
		if err != nil {
			return "", err
		}
		status := strings.ToLower(firstMapString(data, "status"))
		if status == "paid" || status == "paid_over" {
			return "CONFIRMED", nil
		}
		return strings.ToUpper(status), nil
	case "pally":
		return payment.Status, nil
	default:
		return "", errors.New("unknown payment provider")
	}
}

func (a *App) setLanguage(cb *CallbackQuery) {
	lang := strings.TrimPrefix(cb.Data, "lang:")
	if lang != "ru" && lang != "en" {
		return
	}
	_ = a.db.SetLanguage(cb.From.ID, lang)
	user, _ := a.db.GetUser(cb.From.ID)
	if user != nil {
		a.edit(cb, a.accountText(user), a.accountKeyboard(lang))
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
	_, _ = a.tg.SendMessage(msg.Chat.ID, a.content.Message("password_changed", map[string]string{"login": esc(*user.NCUserID), "password": esc(password)}), a.accountKeyboard(langOf(user)))
}

func (a *App) handleSuccessfulPayment(msg *Message) {
	payload := msg.SuccessfulPayment.InvoicePayload
	if !strings.HasPrefix(payload, "stars_donate:") {
		return
	}
	until := time.Now().UTC().Add(time.Duration(a.cfg.PremiumDays) * 24 * time.Hour).Format(time.RFC3339)
	_ = a.db.SetSupporter(msg.From.ID, true, &until)
	_ = a.db.CreatePayment(msg.SuccessfulPayment.TelegramPaymentChargeID, msg.From.ID, "telegram_stars", msg.SuccessfulPayment.TotalAmount, "XTR", "CONFIRMED", nil, &payload)
	_, _ = a.tg.SendMessage(msg.Chat.ID, "⭐ Спасибо за поддержку! Премиум-иконка активирована.", a.accountKeyboard("ru"))
}
