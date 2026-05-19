package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func (a *App) startWebhookServer() {
	if strings.TrimSpace(a.cfg.WebhookListenAddr) == "" {
		return
	}
	mux := http.NewServeMux()
	mux.HandleFunc(a.cfg.PlategaWebhookPath, a.plategaWebhook)
	mux.HandleFunc(a.cfg.PallyCallbackPath, a.pallyWebhook)
	mux.HandleFunc(a.cfg.CryptoBotWebhookPath, a.cryptoBotWebhook)
	mux.HandleFunc(a.cfg.HeleketWebhookPath, a.heleketWebhook)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	log.Printf("webhook server listening: addr=%s platega_path=%s pally_path=%s cryptobot_path=%s heleket_path=%s callback_url=%s", a.cfg.WebhookListenAddr, a.cfg.PlategaWebhookPath, a.cfg.PallyCallbackPath, a.cfg.CryptoBotWebhookPath, a.cfg.HeleketWebhookPath, a.cfg.PlategaCallbackURL)
	if err := http.ListenAndServe(a.cfg.WebhookListenAddr, mux); err != nil {
		log.Printf("webhook server stopped: %v", err)
	}
}

func (a *App) plategaWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	merchantID := r.Header.Get("X-MerchantId")
	if merchantID == "" {
		merchantID = r.Header.Get("X-Merchant-Id")
	}
	secret := r.Header.Get("X-Secret")
	if secret == "" {
		secret = r.Header.Get("X-Api-Key")
	}
	if a.cfg.PlategaMerchantID != "" && merchantID != a.cfg.PlategaMerchantID {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if a.cfg.PlategaSecret != "" && secret != a.cfg.PlategaSecret {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	defer r.Body.Close()
	var payload map[string]any
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	transactionID := plategaString(payload, "id", "transactionId", "transaction_id")
	status := strings.ToUpper(plategaString(payload, "status", "paymentStatus", "payment_status"))
	amount := plategaInt(payload, "amount")
	currency := plategaString(payload, "currency")
	if transactionID == "" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if status == "" {
		status = "UNKNOWN"
	}
	existing, _ := a.db.GetPayment(transactionID)
	if existing != nil && existing.Status == "FULFILLED" {
		log.Printf("platega webhook duplicate ignored: transaction_id=%s status=%s", transactionID, status)
		w.WriteHeader(http.StatusOK)
		return
	}
	_ = a.db.UpdatePaymentStatus(transactionID, status)
	if status == "CONFIRMED" {
		if err := a.fulfillPayment(transactionID); err != nil {
			log.Printf("platega webhook fulfillment failed: transaction_id=%s err=%v", transactionID, err)
		}
	}
	log.Printf("platega webhook received: transaction_id=%s status=%s amount=%d currency=%s", transactionID, status, amount, currency)
	w.WriteHeader(http.StatusOK)
}

func (a *App) pallyWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if a.pally == nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	if err := r.ParseForm(); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if !a.pally.VerifyPostback(r.PostForm) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	transactionID := strings.TrimSpace(r.PostForm.Get("TrsId"))
	if transactionID == "" {
		transactionID = strings.TrimSpace(r.PostForm.Get("InvId"))
	}
	status := strings.ToUpper(strings.TrimSpace(r.PostForm.Get("Status")))
	if status == "SUCCESS" {
		status = "CONFIRMED"
	}
	_ = a.db.UpdatePaymentStatus(transactionID, status)
	if status == "CONFIRMED" {
		if err := a.fulfillPayment(transactionID); err != nil {
			log.Printf("pally webhook fulfillment failed: transaction_id=%s err=%v", transactionID, err)
		}
	}
	log.Printf("pally webhook received: transaction_id=%s status=%s amount=%s", transactionID, status, r.PostForm.Get("OutSum"))
	w.WriteHeader(http.StatusOK)
}

func (a *App) cryptoBotWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if a.cryptoBot == nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	defer r.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(r.Body, 2<<20))
	if !a.cryptoBot.VerifyWebhook(raw, r.Header.Get("crypto-pay-api-signature")) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	var update map[string]any
	if err := json.Unmarshal(raw, &update); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if fmt.Sprint(update["update_type"]) != "invoice_paid" {
		w.WriteHeader(http.StatusOK)
		return
	}
	payload, _ := update["payload"].(map[string]any)
	transactionID := firstMapString(payload, "invoice_id")
	if transactionID == "" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	_ = a.db.UpdatePaymentStatus(transactionID, "CONFIRMED")
	if err := a.fulfillPayment(transactionID); err != nil {
		log.Printf("cryptobot webhook fulfillment failed: transaction_id=%s err=%v", transactionID, err)
	}
	log.Printf("cryptobot webhook received: transaction_id=%s status=CONFIRMED", transactionID)
	w.WriteHeader(http.StatusOK)
}

func (a *App) heleketWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if a.heleket == nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	defer r.Body.Close()
	var payload map[string]any
	if err := json.NewDecoder(io.LimitReader(r.Body, 2<<20)).Decode(&payload); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if !a.heleket.VerifyWebhook(payload) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	transactionID := firstMapString(payload, "order_id", "uuid")
	status := strings.ToLower(firstMapString(payload, "status"))
	if transactionID == "" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	dbStatus := strings.ToUpper(status)
	if status == "paid" || status == "paid_over" {
		dbStatus = "CONFIRMED"
	}
	_ = a.db.UpdatePaymentStatus(transactionID, dbStatus)
	if dbStatus == "CONFIRMED" {
		if err := a.fulfillPayment(transactionID); err != nil {
			log.Printf("heleket webhook fulfillment failed: transaction_id=%s err=%v", transactionID, err)
		}
	}
	log.Printf("heleket webhook received: transaction_id=%s status=%s", transactionID, dbStatus)
	w.WriteHeader(http.StatusOK)
}

func plategaString(payload map[string]any, keys ...string) string {
	for _, key := range keys {
		value, ok := payload[key]
		if !ok || value == nil {
			continue
		}
		text := strings.TrimSpace(fmt.Sprint(value))
		if text != "" && text != "<nil>" {
			return text
		}
	}
	return ""
}

func plategaInt(payload map[string]any, keys ...string) int {
	raw := plategaString(payload, keys...)
	if raw == "" {
		return 0
	}
	value, _ := strconv.Atoi(strings.Split(raw, ".")[0])
	return value
}

func (a *App) fulfillPayment(transactionID string) error {
	payment, err := a.db.GetPayment(transactionID)
	if err != nil || payment == nil {
		return err
	}
	if payment.Status == "FULFILLED" {
		return nil
	}
	if payment.Payload == nil {
		return nil
	}
	parts := strings.Split(*payment.Payload, ":")
	if len(parts) < 2 {
		return nil
	}
	user, err := a.db.GetUser(payment.TelegramID)
	if err != nil || user == nil {
		return err
	}
	switch parts[0] {
	case "platega_donate", "pally_donate", "cryptobot_donate", "heleket_donate", "donate":
		until := time.Now().UTC().Add(time.Duration(a.cfg.PremiumDays) * 24 * time.Hour).Format(time.RFC3339)
		_ = a.db.SetSupporter(payment.TelegramID, true, &until)
		_, _ = a.tg.SendMessage(payment.TelegramID, "⭐ Оплата подтверждена! Премиум-иконка активирована.", a.accountKeyboard(langOf(user)))
	case "storage":
		gb := a.storagePackGB()
		if len(parts) >= 3 {
			if parsed, err := strconv.Atoi(parts[2]); err == nil && parsed > 0 {
				gb = parsed
			}
		}
		newQuota := user.QuotaGB + gb
		if user.NCUserID != nil {
			if err := a.nc.SetQuota(*user.NCUserID, newQuota); err != nil {
				return err
			}
		}
		_ = a.db.SetQuota(payment.TelegramID, newQuota)
		_, _ = a.tg.SendMessage(payment.TelegramID, "✅ Оплата подтверждена. Добавлено место: <b>"+strconv.Itoa(gb)+" GB</b>.", a.accountKeyboard(langOf(user)))
	}
	_ = a.db.UpdatePaymentStatus(transactionID, "FULFILLED")
	return nil
}
