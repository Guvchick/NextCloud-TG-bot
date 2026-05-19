package main

import (
	"fmt"
	"log"
	"net/http"
	"runtime/debug"
	"strings"
	"time"
)

func (a *App) notifyAdmins(text string) {
	for adminID := range a.cfg.AdminIDs {
		if _, err := a.tg.SendMessage(adminID, text, nil); err != nil {
			log.Printf("admin notification failed: admin_id=%d err=%v", adminID, err)
		}
	}
}

func (a *App) notifyStartup() {
	if !a.cfg.NotifyAdminsOnStart {
		return
	}
	text := fmt.Sprintf(
		"🟢 <b>Бот запущен</b>\n<code>━━━━━━━━━━━━━━━━━━━━</code>\n\n"+
			"Время: <code>%s</code>\n"+
			"Облако: <code>%s</code>\n"+
			"Telegram API: <code>%s</code>\n"+
			"Local Bot API: <b>%s</b>\n"+
			"Workers: <b>%d</b>\n"+
			"Webhook: <code>%s</code>",
		time.Now().Format("2006-01-02 15:04:05"),
		esc(a.cfg.NextcloudURL),
		esc(a.cfg.TelegramAPIBaseURL),
		mapBool(a.cfg.TelegramLocalMode, "да", "нет"),
		a.cfg.UploadWorkers,
		esc(a.cfg.WebhookListenAddr),
	)
	a.notifyAdmins(text)
}

func (a *App) notifyCrash(scope string, recovered any, stack []byte) {
	if !a.cfg.NotifyAdminsOnCrash {
		return
	}
	stackText := strings.TrimSpace(string(stack))
	if len([]rune(stackText)) > 2500 {
		stackText = string([]rune(stackText)[:2500]) + "\n..."
	}
	text := fmt.Sprintf(
		"🚨 <b>Краш в боте</b>\n<code>━━━━━━━━━━━━━━━━━━━━</code>\n\n"+
			"Место: <code>%s</code>\n"+
			"Ошибка: <code>%s</code>\n\n"+
			"<b>Stack:</b>\n<code>%s</code>",
		esc(scope),
		esc(fmt.Sprint(recovered)),
		esc(stackText),
	)
	a.notifyAdmins(text)
}

func (a *App) safeGo(name string, fn func()) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				stack := debug.Stack()
				log.Printf("panic in goroutine %s: %v\n%s", name, r, string(stack))
				a.notifyCrash("goroutine: "+name, r, stack)
			}
		}()
		fn()
	}()
}

func (a *App) recoverHTTP(name string, handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				stack := debug.Stack()
				log.Printf("panic in webhook %s: %v\n%s", name, recovered, string(stack))
				a.notifyCrash("webhook: "+name, recovered, stack)
				w.WriteHeader(http.StatusInternalServerError)
			}
		}()
		handler(w, r)
	}
}
