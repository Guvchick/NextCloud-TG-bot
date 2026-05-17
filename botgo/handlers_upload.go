package main

import (
	"fmt"
	"log"
	"os"
	"strconv"
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
		_, _ = a.tg.SendMessage(msg.Chat.ID, fmt.Sprintf("⚠️ Telegram не дает боту скачать этот файл: он больше <b>%d MB</b>.\n\nЗагрузите большой файл напрямую через веб-интерфейс облака.", a.cfg.TelegramMaxDownloadMB), nil)
		return
	}
	isSupporter := isPremium(user)
	priority := 10
	if isSupporter {
		priority = 0
	}
	a.uploadSeq++
	statusMsg, _ := a.tg.SendMessage(msg.Chat.ID, fmt.Sprintf("📥 <b>%s</b> (%s) добавлен в очередь.", esc(filename), formatBytes(size)), nil)
	job := UploadJob{
		TelegramID:      user.TelegramID,
		ChatID:          msg.Chat.ID,
		StatusMessageID: statusMsg.MessageID,
		FileID:          fileID,
		Filename:        filename,
		FileSize:        size,
		Lang:            langOf(user),
		IsSupporter:     isSupporter,
		Priority:        priority,
		Seq:             a.uploadSeq,
	}
	position := a.uploads.Put(job)
	text := fmt.Sprintf("📥 <b>%s</b> (%s) добавлен в очередь.\n\nМесто в очереди: <b>%d</b>.", esc(filename), formatBytes(size), position)
	if isSupporter {
		text += "\n\n⭐ У вас премиум-приоритет: загрузка пройдет раньше обычной очереди."
	}
	_ = a.tg.EditMessageText(msg.Chat.ID, statusMsg.MessageID, text, nil)
	log.Printf("upload queued: telegram_id=%d filename=%s size=%d priority=%d", user.TelegramID, filename, size, priority)
}

func (a *App) uploadWorker() {
	for {
		job := a.uploads.Get()
		a.processUpload(job)
	}
}

func (a *App) processUpload(job UploadJob) {
	user, err := a.db.GetUser(job.TelegramID)
	if err != nil || user == nil || user.Status != "approved" || user.IsDisabled == 1 || user.NCUserID == nil || user.NCPassword == nil {
		_ = a.tg.EditMessageText(job.ChatID, job.StatusMessageID, "Загрузка доступна только активным пользователям.", nil)
		return
	}
	_ = a.tg.EditMessageText(job.ChatID, job.StatusMessageID, fmt.Sprintf("📤 Загружаю <b>%s</b> (%s) в облако...", esc(job.Filename), formatBytes(job.FileSize)), nil)
	tmp, err := a.tg.DownloadFile(job.FileID)
	if err != nil {
		_ = a.sendEventSticker(job.ChatID, "error")
		_ = a.tg.EditMessageText(job.ChatID, job.StatusMessageID, fmt.Sprintf("⚠️ Telegram не дает боту скачать этот файл.\n\nЛимит для загрузки через бота: <b>%d MB</b>.\nЗагрузите большой файл напрямую через облако.", a.cfg.TelegramMaxDownloadMB), nil)
		log.Printf("telegram download failed: %v", err)
		return
	}
	defer os.Remove(tmp)
	remote, err := a.nc.UploadFile(*user.NCUserID, *user.NCPassword, job.Filename, tmp)
	if err != nil {
		_ = a.sendEventSticker(job.ChatID, "error")
		_ = a.tg.EditMessageText(job.ChatID, job.StatusMessageID, "⚠️ Не удалось загрузить файл в облако: <code>"+esc(err.Error())+"</code>", nil)
		log.Printf("nextcloud upload failed: telegram_id=%d filename=%s err=%v", job.TelegramID, job.Filename, err)
		return
	}
	_ = a.sendEventSticker(job.ChatID, "upload_ok")
	_ = a.tg.EditMessageText(job.ChatID, job.StatusMessageID, "✅ <b>Файл загружен</b>\n\nПуть: <code>"+esc(remote)+"</code>\n\n"+a.storageText(user), nil)
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
	return "☁️✨ <b>Ваше облако</b> ✨\n<code>━━━━━━━━━━━━━━━━━━━━</code>\n\n" +
		premium +
		`🌐 Ссылка: <a href="` + esc(a.cfg.NextcloudURL) + `">` + esc(a.cfg.NextcloudURL) + `</a>` + "\n" +
		"🆔 Логин: <code>" + esc(login) + "</code>\n" +
		"🔐 Пароль: " + password + "\n" +
		fmt.Sprintf("💾 Квота: <b>%d GB</b>\n\n", user.QuotaGB) +
		a.storageText(user) + "\n\n" +
		"📤 Отправьте файл в этот чат, и бот загрузит его в облако."
}

func (a *App) storageText(user *User) string {
	if user.NCUserID == nil || user.NCPassword == nil {
		return "☁️ Занято: <b>неизвестно</b>"
	}
	used, available, err := a.nc.GetQuota(*user.NCUserID, *user.NCPassword)
	if err != nil {
		log.Printf("quota failed: telegram_id=%d err=%v", user.TelegramID, err)
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
	checked := 0
	removed := 0
	for _, user := range users {
		if user.NCUserID == nil {
			continue
		}
		checked++
		exists, err := a.nc.UserExists(*user.NCUserID)
		if err != nil {
			return checked, removed, err
		}
		if !exists {
			_ = a.db.DeleteUser(user.TelegramID)
			removed++
			log.Printf("sync removed bot user: telegram_id=%d nc_user_id=%s", user.TelegramID, *user.NCUserID)
		}
	}
	return checked, removed, nil
}

