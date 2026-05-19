package main

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

type UploadBatchItem struct {
	Seq      int64
	Filename string
	Size     int64
	Status   string
	Position int
	Remote   string
	Error    string
}

type UploadBatch struct {
	TelegramID int64
	ChatID     int64
	MessageID  int
	CreatedAt  time.Time
	UpdatedAt  time.Time
	Items      map[int64]*UploadBatchItem
}

type UploadBatchManager struct {
	mu      sync.Mutex
	batches map[int64]*UploadBatch
}

func NewUploadBatchManager() *UploadBatchManager {
	return &UploadBatchManager{batches: map[int64]*UploadBatch{}}
}

func (m *UploadBatchManager) getOrCreate(a *App, telegramID, chatID int64) *UploadBatch {
	m.mu.Lock()
	now := time.Now()
	if batch := m.batches[telegramID]; batch != nil && now.Sub(batch.UpdatedAt) < 3*time.Minute && !batch.allDoneLocked() {
		m.mu.Unlock()
		return batch
	}
	m.mu.Unlock()
	msg, _ := a.tg.SendMessage(chatID, "📦 <b>Готовлю очередь загрузки...</b>", nil)
	batch := &UploadBatch{
		TelegramID: telegramID,
		ChatID:     chatID,
		CreatedAt:  now,
		UpdatedAt:  now,
		Items:      map[int64]*UploadBatchItem{},
	}
	if msg != nil {
		batch.MessageID = msg.MessageID
	}
	m.mu.Lock()
	if existing := m.batches[telegramID]; existing != nil && now.Sub(existing.UpdatedAt) < 3*time.Minute && !existing.allDoneLocked() {
		m.mu.Unlock()
		return existing
	}
	m.batches[telegramID] = batch
	m.mu.Unlock()
	return batch
}

func (m *UploadBatchManager) add(a *App, job UploadJob, position int) int {
	batch := m.getOrCreate(a, job.TelegramID, job.ChatID)
	m.mu.Lock()
	batch.Items[job.Seq] = &UploadBatchItem{
		Seq:      job.Seq,
		Filename: job.Filename,
		Size:     job.FileSize,
		Status:   "queued",
		Position: position,
	}
	batch.UpdatedAt = time.Now()
	messageID := batch.MessageID
	text := batch.renderLocked("")
	m.mu.Unlock()
	if messageID > 0 {
		_ = a.tg.EditMessageText(batch.ChatID, messageID, text, nil)
	}
	return messageID
}

func (m *UploadBatchManager) updatePosition(a *App, job UploadJob, position int) {
	m.mu.Lock()
	batch := m.batches[job.TelegramID]
	if batch == nil {
		m.mu.Unlock()
		return
	}
	if item := batch.Items[job.Seq]; item != nil {
		item.Position = position
	}
	batch.UpdatedAt = time.Now()
	messageID := batch.MessageID
	text := batch.renderLocked("")
	m.mu.Unlock()
	if messageID > 0 {
		_ = a.tg.EditMessageText(batch.ChatID, messageID, text, nil)
	}
}

func (m *UploadBatchManager) set(a *App, job UploadJob, status, remote, errorText string, footer string) {
	m.mu.Lock()
	batch := m.batches[job.TelegramID]
	if batch == nil {
		m.mu.Unlock()
		return
	}
	item := batch.Items[job.Seq]
	if item != nil {
		item.Status = status
		item.Remote = remote
		item.Error = errorText
	}
	batch.UpdatedAt = time.Now()
	messageID := batch.MessageID
	text := batch.renderLocked(footer)
	m.mu.Unlock()
	if messageID > 0 {
		_ = a.tg.EditMessageText(batch.ChatID, messageID, text, nil)
	}
}

func (m *UploadBatchManager) allDone(telegramID int64) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	batch := m.batches[telegramID]
	return batch != nil && batch.allDoneLocked()
}

func (m *UploadBatchManager) hasFailures(telegramID int64) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	batch := m.batches[telegramID]
	if batch == nil {
		return false
	}
	for _, item := range batch.Items {
		if item.Status == "failed" {
			return true
		}
	}
	return false
}

func (b *UploadBatch) allDoneLocked() bool {
	if len(b.Items) == 0 {
		return false
	}
	for _, item := range b.Items {
		if item.Status != "done" && item.Status != "failed" {
			return false
		}
	}
	return true
}

func (b *UploadBatch) renderLocked(footer string) string {
	items := make([]*UploadBatchItem, 0, len(b.Items))
	for _, item := range b.Items {
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Seq < items[j].Seq })
	total := len(items)
	done := 0
	failed := 0
	var lines []string
	for index, item := range items {
		icon := "⏳"
		status := "в очереди"
		switch item.Status {
		case "downloading":
			icon = "📥"
			status = "получаю из Telegram"
		case "uploading":
			icon = "📤"
			status = "доставляю в облако"
		case "done":
			icon = "✅"
			status = "доставлено"
			done++
		case "failed":
			icon = "⚠️"
			status = "ошибка"
			failed++
		}
		name := item.Filename
		if len([]rune(name)) > 42 {
			name = string([]rune(name)[:42]) + "..."
		}
		line := fmt.Sprintf("%d. %s <b>%s</b> (%s) — %s", index+1, icon, esc(name), formatBytes(item.Size), status)
		if item.Status == "queued" && item.Position > 0 {
			line += fmt.Sprintf(" #%d", item.Position)
		}
		if item.Status == "failed" && item.Error != "" {
			errText := item.Error
			if len([]rune(errText)) > 80 {
				errText = string([]rune(errText)[:80]) + "..."
			}
			line += "\n   <code>" + esc(errText) + "</code>"
		}
		lines = append(lines, line)
	}
	titleIcon := "📦"
	if total > 0 && done+failed == total {
		if failed > 0 {
			titleIcon = "⚠️"
		} else {
			titleIcon = "✅"
		}
	}
	text := fmt.Sprintf("%s <b>Загрузка файлов</b>\n<code>━━━━━━━━━━━━━━━━━━━━</code>\n\n", titleIcon)
	if total == 0 {
		text += "Очередь пустая."
	} else {
		text += strings.Join(lines, "\n")
		text += fmt.Sprintf("\n\nИтого: <b>%d</b> · доставлено: <b>%d</b> · ошибок: <b>%d</b>", total, done, failed)
	}
	if strings.TrimSpace(footer) != "" {
		text += "\n\n" + footer
	}
	return text
}
