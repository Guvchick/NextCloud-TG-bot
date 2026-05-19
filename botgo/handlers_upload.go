package main

import (
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
)

func (a *App) hasUpload(msg *Message) bool {
	return msg.Document != nil || len(msg.Photo) > 0 || msg.Video != nil || msg.Audio != nil || msg.Voice != nil || msg.VideoNote != nil || msg.Animation != nil
}

func (a *App) uploadTarget(msg *Message) (string, string, int64, bool) {
	if msg.Document != nil {
		return msg.Document.FileID, cleanFilename(firstNonEmpty(msg.Document.FileName, fmt.Sprintf("document_%d.bin", msg.MessageID))), msg.Document.FileSize, true
	}
	if len(msg.Photo) > 0 {
		photo := msg.Photo[len(msg.Photo)-1]
		return photo.FileID, fmt.Sprintf("photo_%d.jpg", msg.MessageID), photo.FileSize, true
	}
	if msg.Video != nil {
		return msg.Video.FileID, cleanFilename(firstNonEmpty(msg.Video.FileName, fmt.Sprintf("video_%d.mp4", msg.MessageID))), msg.Video.FileSize, true
	}
	if msg.Audio != nil {
		return msg.Audio.FileID, cleanFilename(firstNonEmpty(msg.Audio.FileName, fmt.Sprintf("audio_%d.mp3", msg.MessageID))), msg.Audio.FileSize, true
	}
	if msg.Voice != nil {
		return msg.Voice.FileID, fmt.Sprintf("voice_%d.ogg", msg.MessageID), msg.Voice.FileSize, true
	}
	if msg.VideoNote != nil {
		return msg.VideoNote.FileID, fmt.Sprintf("video_note_%d.mp4", msg.MessageID), msg.VideoNote.FileSize, true
	}
	if msg.Animation != nil {
		return msg.Animation.FileID, cleanFilename(firstNonEmpty(msg.Animation.FileName, fmt.Sprintf("animation_%d.mp4", msg.MessageID))), msg.Animation.FileSize, true
	}
	return "", "", 0, false
}

func (a *App) handleUpload(msg *Message) {
	user, err := a.db.GetUser(msg.From.ID)
	if err != nil || user == nil || user.Status != "approved" || user.IsDisabled == 1 {
		_, _ = a.tg.SendMessage(msg.Chat.ID, "Загрузка доступна только одобренным активным пользователям.", nil)
		return
	}
	if user.NCUserID == nil || user.NCPassword == nil {
		_, _ = a.tg.SendMessage(msg.Chat.ID, "Для этого аккаунта нет сохраненного WebDAV-пароля. Попросите администратора сбросить пароль в панели.", nil)
		return
	}
	fileID, filename, size, ok := a.uploadTarget(msg)
	if !ok {
		_, _ = a.tg.SendMessage(msg.Chat.ID, "Не удалось определить файл для загрузки.", nil)
		return
	}
	limit := int64(a.cfg.TelegramMaxDownloadMB) * 1024 * 1024
	if size > 0 && size > limit {
		_ = a.sendEventSticker(msg.Chat.ID, "error")
		_, _ = a.tg.SendMessage(msg.Chat.ID, a.content.Message("upload_too_large", map[string]string{"limit_mb": strconv.Itoa(a.cfg.TelegramMaxDownloadMB)}), nil)
		return
	}
	isSupporter := isPremium(user)
	priority := 10
	if isSupporter {
		priority = 0
	}
	a.uploadSeq++
	job := UploadJob{
		TelegramID:      user.TelegramID,
		ChatID:          msg.Chat.ID,
		FileID:          fileID,
		Filename:        filename,
		FileSize:        size,
		Lang:            langOf(user),
		IsSupporter:     isSupporter,
		Priority:        priority,
		Seq:             a.uploadSeq,
	}
	a.batches.add(a, job, 0)
	position := a.uploads.Put(job)
	a.batches.updatePosition(a, job, position)
	log.Printf("upload queued: telegram_id=%d filename=%s size=%d priority=%d", user.TelegramID, filename, size, priority)
}

func (a *App) uploadWorker(workerID int) {
	for {
		job := a.uploads.Get()
		log.Printf("upload worker %d picked job: telegram_id=%d filename=%s", workerID, job.TelegramID, job.Filename)
		a.processUpload(job)
	}
}

func (a *App) processUpload(job UploadJob) {
	user, err := a.db.GetUser(job.TelegramID)
	if err != nil || user == nil || user.Status != "approved" || user.IsDisabled == 1 || user.NCUserID == nil || user.NCPassword == nil {
		a.batches.set(a, job, "failed", "", "Загрузка доступна только активным пользователям.", "")
		return
	}
	a.batches.set(a, job, "downloading", "", "", "")
	tmp, cleanup, err := a.tg.DownloadFile(job.FileID)
	if err != nil {
		_ = a.sendEventSticker(job.ChatID, "error")
		userMessage := a.telegramDownloadErrorText(err)
		a.batches.set(a, job, "failed", "", userMessage, "")
		log.Printf("upload failed at telegram download: telegram_id=%d file=%s size=%d local_mode=%v api=%s err=%s", job.TelegramID, job.Filename, job.FileSize, a.cfg.TelegramLocalMode, a.cfg.TelegramAPIBaseURL, err.Error())
		return
	}
	if cleanup {
		defer os.Remove(tmp)
	}
	a.batches.set(a, job, "uploading", "", "", "")
	remote, err := a.nc.UploadFile(*user.NCUserID, *user.NCPassword, job.Filename, tmp)
	if err != nil {
		_ = a.sendEventSticker(job.ChatID, "error")
		a.batches.set(a, job, "failed", "", err.Error(), "")
		log.Printf("nextcloud upload failed: telegram_id=%d filename=%s err=%v", job.TelegramID, job.Filename, err)
		return
	}
	a.invalidateUserQuota(user)
	a.batches.set(a, job, "done", remote, "", "")
	if a.batches.allDone(job.TelegramID) {
		footer := a.storageTextFresh(user)
		if a.batches.hasFailures(job.TelegramID) {
			_ = a.sendEventSticker(job.ChatID, "error")
		} else {
			_ = a.sendEventSticker(job.ChatID, "upload_ok")
		}
		a.batches.set(a, job, "done", remote, "", footer)
	}
	log.Printf("upload completed: telegram_id=%d remote=%s size=%d", job.TelegramID, remote, job.FileSize)
}

func (a *App) accountText(user *User) string {
	password := "не сохранен"
	if user.NCPassword != nil && *user.NCPassword != "" {
		password = "<code>" + esc(*user.NCPassword) + "</code>"
	} else {
		password = "<b>не сохранен</b>"
	}
	premium := ""
	if isPremium(user) {
		premium = "⭐ <b>Премиум-поддержка</b> до <b>" + esc(premiumUntilText(user)) + "</b>\n"
	}
	login := strPtr(user.NCUserID, strconv.FormatInt(user.TelegramID, 10))
	return a.content.Message("account_home", map[string]string{
		"premium":   premium,
		"cloud_url": esc(a.cfg.NextcloudURL),
		"login":     esc(login),
		"password":  password,
		"quota_gb":  strconv.Itoa(user.QuotaGB),
		"storage":   a.storageText(user),
	})
}

func (a *App) telegramDownloadErrorText(err error) string {
	text := err.Error()
	if a.cfg.TelegramLocalMode {
		if strings.Contains(text, "lookup") || strings.Contains(text, "connection refused") || strings.Contains(text, "no such host") {
			return "Локальный Telegram Bot API недоступен. Проверьте, что сервис <code>telegram-bot-api</code> запущен в docker compose и включен профиль <code>telegram-local</code>."
		}
		if strings.Contains(text, "not readable") || strings.Contains(text, "no such file") {
			return "Локальный Bot API вернул путь к файлу, но бот его не видит. Проверьте общий volume и <code>TELEGRAM_LOCAL_PATH_PREFIX</code>/<code>TELEGRAM_BOT_PATH_PREFIX</code>."
		}
		return "Локальный Telegram Bot API не смог отдать файл: " + text
	}
	return fmt.Sprintf("Telegram не дает боту скачать файл. Лимит через публичный Bot API: %d MB. Для больших файлов включите локальный Bot API.", a.cfg.TelegramMaxDownloadMB)
}

func (a *App) storageText(user *User) string {
	if user.NCUserID == nil || user.NCPassword == nil {
		return "☁️ Занято: <b>неизвестно</b>"
	}
	return a.storageTextWithMode(user, false)
}

func (a *App) storageTextFresh(user *User) string {
	return a.storageTextWithMode(user, true)
}

func (a *App) storageTextWithMode(user *User, fresh bool) string {
	if user.NCUserID == nil || user.NCPassword == nil {
		return "☁️ Занято: <b>неизвестно</b>"
	}
	used, available, err := a.userQuota(user, fresh)
	if err != nil {
		return "☁️ Занято: <b>не удалось обновить</b>"
	}
	if used == 0 && available >= 0 {
		return fmt.Sprintf("☁️ Занято: <b>0 B</b>\n🟢 Доступно: <b>%s</b>\n📊 <code>%s</code> 0.0%%", formatBytes(available), usageBar(used, available))
	}
	if available >= 0 {
		total := used + available
		percent := float64(used) / float64(total) * 100
		return fmt.Sprintf("☁️ Занято: <b>%s</b> / <b>%s</b>\n📊 <code>%s</code> %.1f%%", formatBytes(used), formatBytes(total), usageBar(used, available), percent)
	}
	return fmt.Sprintf("☁️ Занято: <b>%s</b>, 🟢 свободно: <b>%s</b>", formatBytes(used), formatBytes(available))
}

func (a *App) syncNextcloudUsers() (int, int, error) {
	users, err := a.db.ListUsers("approved", 100000, 0)
	if err != nil {
		return 0, 0, err
	}
	jobs := make(chan User)
	var wg sync.WaitGroup
	var mu sync.Mutex
	checked := 0
	removed := 0
	var firstErr error
	workers := 5
	if len(users) < workers {
		workers = len(users)
	}
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for user := range jobs {
				if user.NCUserID == nil {
					continue
				}
				exists, err := a.nc.UserExists(*user.NCUserID)
				mu.Lock()
				checked++
				if err != nil && firstErr == nil {
					firstErr = err
				}
				mu.Unlock()
				if err != nil {
					continue
				}
				if !exists {
					_ = a.db.DeleteUser(user.TelegramID)
					mu.Lock()
					removed++
					mu.Unlock()
					log.Printf("sync removed bot user: telegram_id=%d nc_user_id=%s", user.TelegramID, *user.NCUserID)
				}
			}
		}()
	}
	for _, user := range users {
		jobs <- user
	}
	close(jobs)
	wg.Wait()
	return checked, removed, firstErr
}
