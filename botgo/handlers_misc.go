package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func (a *App) broadcastText(chatID int64, text string) {
	if strings.TrimSpace(text) == "" {
		_, _ = a.tg.SendMessage(chatID, "📣 Используйте: <code>/broadcast текст рассылки</code>", adminKeyboard())
		return
	}
	ids, err := a.db.ApprovedTelegramIDs()
	if err != nil {
		_, _ = a.tg.SendMessage(chatID, "Не удалось получить пользователей: <code>"+esc(err.Error())+"</code>", adminKeyboard())
		return
	}
	sent := 0
	failed := 0
	for _, id := range ids {
		if _, err := a.tg.SendMessage(id, text, nil); err != nil {
			failed++
			log.Printf("broadcast failed: telegram_id=%d err=%v", id, err)
		} else {
			sent++
			time.Sleep(50 * time.Millisecond)
		}
	}
	_, _ = a.tg.SendMessage(chatID, fmt.Sprintf("📣 Рассылка завершена.\n\nОтправлено: <b>%d</b>\nОшибок: <b>%d</b>", sent, failed), adminKeyboard())
}

func (a *App) broadcastMessage(msg *Message) {
	ids, err := a.db.ApprovedTelegramIDs()
	if err != nil {
		_, _ = a.tg.SendMessage(msg.Chat.ID, "Не удалось получить пользователей: <code>"+esc(err.Error())+"</code>", adminKeyboard())
		return
	}
	sent := 0
	failed := 0
	for _, id := range ids {
		if err := a.tg.CopyMessage(id, msg.Chat.ID, msg.MessageID); err != nil {
			failed++
			log.Printf("broadcast copy failed: telegram_id=%d err=%v", id, err)
		} else {
			sent++
			time.Sleep(50 * time.Millisecond)
		}
	}
	_, _ = a.tg.SendMessage(msg.Chat.ID, fmt.Sprintf("📣 Рассылка завершена.\n\nОтправлено: <b>%d</b>\nОшибок: <b>%d</b>", sent, failed), adminKeyboard())
}

func (a *App) backupCallback(cb *CallbackQuery) {
	if cb.Data == "backup" {
		a.edit(cb, "🗄️ <b>Бекапы</b>\n\nВсе бекапы сжимаются в .gz, хранятся на сервере и автоматически чистятся по retention.", backupKeyboard())
		return
	}
	if cb.Data == "backup:db" {
		path, err := createDatabaseBackup(a.cfg, a.db)
		if err != nil {
			a.tg.AnswerCallback(cb.ID, "Ошибка бекапа", true)
			_, _ = a.tg.SendMessage(cb.Message.Chat.ID, "Не удалось создать бекап: <code>"+esc(err.Error())+"</code>", nil)
			return
		}
		_ = pruneBackups(a.cfg)
		_ = a.tg.SendDocument(cb.Message.Chat.ID, path, "Сжатый PostgreSQL-бекап базы бота")
		return
	}
	if cb.Data == "backup:json" {
		path, err := createPublicJSONBackup(a.cfg, a.db)
		if err != nil {
			a.tg.AnswerCallback(cb.ID, "Ошибка бекапа", true)
			_, _ = a.tg.SendMessage(cb.Message.Chat.ID, "Не удалось создать JSON-бекап: <code>"+esc(err.Error())+"</code>", nil)
			return
		}
		_ = pruneBackups(a.cfg)
		_ = a.tg.SendDocument(cb.Message.Chat.ID, path, "Сжатый JSON-бекап пользователей")
		return
	}
	if cb.Data == "backup:list" {
		files := listBackups(a.cfg)
		text := "🗄️ <b>Последние PostgreSQL-бекапы</b>\n\n"
		if len(files) == 0 {
			text += "Бекапов пока нет."
		}
		for i, file := range files {
			info, _ := os.Stat(file)
			text += fmt.Sprintf("%d. <code>%s</code> (%s)\n", i+1, esc(filepath.Base(file)), formatBytes(info.Size()))
		}
		a.edit(cb, text, backupKeyboard())
		return
	}
	if cb.Data == "backup:restore" {
		files := listBackups(a.cfg)
		if len(files) == 0 {
			a.edit(cb, "Нет PostgreSQL-бекапов для восстановления.", backupKeyboard())
			return
		}
		a.edit(cb, "♻️ <b>Восстановление бекапа</b>\n\nВыберите PostgreSQL-бекап. Перед восстановлением будет создан свежий safety-бекап.", restoreBackupKeyboard(files))
		return
	}
}

func (a *App) restoreBackupCallback(cb *CallbackQuery) {
	indexRaw := strings.TrimPrefix(cb.Data, "restore:")
	index, err := strconv.Atoi(indexRaw)
	files := listBackups(a.cfg)
	if err != nil || index < 0 || index >= len(files) {
		a.tg.AnswerCallback(cb.ID, "Бекап не найден", true)
		return
	}
	safety, safetyErr := createDatabaseBackup(a.cfg, a.db)
	if safetyErr != nil {
		a.tg.AnswerCallback(cb.ID, "Не удалось создать safety-бекап", true)
		_, _ = a.tg.SendMessage(cb.Message.Chat.ID, "Не удалось создать safety-бекап: <code>"+esc(safetyErr.Error())+"</code>", nil)
		return
	}
	if err := restoreDatabaseBackup(files[index], a.db); err != nil {
		a.tg.AnswerCallback(cb.ID, "Не удалось восстановить", true)
		_, _ = a.tg.SendMessage(cb.Message.Chat.ID, "Не удалось восстановить базу: <code>"+esc(err.Error())+"</code>", nil)
		return
	}
	a.edit(
		cb,
		"♻️ База восстановлена из <code>"+esc(filepath.Base(files[index]))+"</code>.\n\nSafety-бекап: <code>"+esc(filepath.Base(safety))+"</code>",
		maintenanceKeyboard(),
	)
}

func (a *App) autoBackupLoop() {
	ticker := time.NewTicker(time.Duration(a.cfg.AutoBackupIntervalHours) * time.Hour)
	defer ticker.Stop()
	for {
		path, err := createDatabaseBackup(a.cfg, a.db)
		if err != nil {
			log.Printf("auto backup failed: %v", err)
		} else {
			log.Printf("auto backup created: %s", path)
			_ = pruneBackups(a.cfg)
		}
		<-ticker.C
	}
}

func (a *App) nextcloudSyncLoop() {
	ticker := time.NewTicker(time.Duration(a.cfg.NextcloudSyncIntervalMinutes) * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		checked, removed, err := a.syncNextcloudUsers()
		if err != nil {
			log.Printf("automatic sync failed: %v", err)
		} else {
			log.Printf("automatic sync completed: checked=%d removed=%d", checked, removed)
		}
	}
}

func (a *App) premiumExpirationLoop() {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for range ticker.C {
		n, err := a.db.ExpireSupporters()
		if err != nil {
			log.Printf("premium expiration failed: %v", err)
		} else if n > 0 {
			log.Printf("expired premium supporters: %d", n)
		}
	}
}

func (a *App) edit(cb *CallbackQuery, text string, markup *InlineKeyboardMarkup) {
	if cb.Message == nil {
		return
	}
	if err := a.tg.EditMessageText(cb.Message.Chat.ID, cb.Message.MessageID, text, markup); err != nil {
		log.Printf("edit message failed: %v", err)
	}
}

func (a *App) isAdmin(id int64) bool {
	return a.cfg.AdminIDs[id]
}
