package main

import (
	"crypto/rand"
	"fmt"
	"html"
	"log"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

func parseAdminIDs(raw string) map[int64]bool {
	ids := map[int64]bool{}
	for _, part := range strings.Split(raw, ",") {
		id, err := strconv.ParseInt(strings.TrimSpace(part), 10, 64)
		if err == nil && id > 0 {
			ids[id] = true
		}
	}
	if len(ids) == 0 {
		log.Fatal("ADMIN_IDS must contain at least one Telegram user id")
	}
	return ids
}

func ptrOrNil(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return &value
}

func strPtr(value *string, fallback string) string {
	if value == nil || *value == "" {
		return fallback
	}
	return *value
}

func esc(value string) string {
	return html.EscapeString(value)
}

func displayName(user *User) string {
	if user == nil {
		return "-"
	}
	if user.Username != nil && *user.Username != "" {
		return "@" + esc(*user.Username)
	}
	name := strings.TrimSpace(strPtr(user.FirstName, "") + " " + strPtr(user.LastName, ""))
	if name == "" {
		return strconv.FormatInt(user.TelegramID, 10)
	}
	return esc(name)
}

func langOf(user *User) string {
	if user != nil && user.Language == "en" {
		return "en"
	}
	return "ru"
}

func generatePassword(length int) string {
	const alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-"
	buf := make([]byte, length)
	random := make([]byte, length)
	if _, err := rand.Read(random); err != nil {
		panic(err)
	}
	for i := range buf {
		buf[i] = alphabet[int(random[i])%len(alphabet)]
	}
	return string(buf)
}

var cleanNameRE = regexp.MustCompile(`[^A-Za-z0-9А-Яа-я._ -]+`)
var cleanSpaceRE = regexp.MustCompile(`\s+`)

func cleanFilename(filename string) string {
	filename = filepath.Base(strings.TrimSpace(filename))
	filename = cleanNameRE.ReplaceAllString(filename, "_")
	filename = cleanSpaceRE.ReplaceAllString(filename, " ")
	filename = strings.Trim(filename, " .")
	runes := []rune(filename)
	if len(runes) > 120 {
		filename = string(runes[:120])
	}
	if filename == "" {
		return "upload.bin"
	}
	return filename
}

func formatBytes(value int64) string {
	if value < 0 {
		return "неизвестно"
	}
	units := []string{"B", "KB", "MB", "GB", "TB"}
	size := float64(value)
	for _, unit := range units {
		if size < 1024 || unit == units[len(units)-1] {
			if unit == "B" {
				return fmt.Sprintf("%d B", value)
			}
			return fmt.Sprintf("%.1f %s", size, unit)
		}
		size /= 1024
	}
	return fmt.Sprintf("%d B", value)
}

func usageBar(used, available int64) string {
	width := 12
	total := used + available
	if total <= 0 {
		return "[" + strings.Repeat("-", width) + "]"
	}
	filled := int(float64(used)/float64(total)*float64(width) + 0.5)
	if filled < 0 {
		filled = 0
	}
	if filled > width {
		filled = width
	}
	return "[" + strings.Repeat("#", filled) + strings.Repeat("-", width-filled) + "]"
}

func isPremium(user *User) bool {
	if user == nil || user.IsSupporter != 1 || user.SupporterUntil == nil {
		return false
	}
	t, err := time.Parse(time.RFC3339, *user.SupporterUntil)
	return err == nil && t.After(time.Now().UTC())
}

func premiumUntilText(user *User) string {
	if user == nil || user.SupporterUntil == nil {
		return "-"
	}
	t, err := time.Parse(time.RFC3339, *user.SupporterUntil)
	if err != nil {
		return *user.SupporterUntil
	}
	return t.Format("2006-01-02")
}

func firstNonEmpty(value, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return value
	}
	return fallback
}

func parseLastInt(data string) int64 {
	parts := strings.Split(data, ":")
	id, _ := strconv.ParseInt(parts[len(parts)-1], 10, 64)
	return id
}

func intFromAny(value any) int {
	switch v := value.(type) {
	case float64:
		return int(v)
	case int:
		return v
	case string:
		n, _ := strconv.Atoi(v)
		return n
	default:
		return 0
	}
}

func containsInt(values []int, target int) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func mapBool(condition bool, yes, no string) string {
	if condition {
		return yes
	}
	return no
}

func eventMark(event string) string {
	switch event {
	case "welcome":
		return "☁️"
	case "approved":
		return "✅"
	case "upload_ok":
		return "📦"
	case "error":
		return "⚠️"
	case "support":
		return "💬"
	case "donate":
		return "💙"
	case "language":
		return "🌐"
	case "password":
		return "🔐"
	case "premium":
		return "⭐"
	case "backup":
		return "🗄️"
	case "sync":
		return "🔄"
	default:
		return "✨"
	}
}

func (a *App) stickerFromConfig(event string) string {
	switch event {
	case "welcome":
		return a.cfg.StickerWelcome
	case "approved":
		return a.cfg.StickerApproved
	case "upload_ok":
		return a.cfg.StickerUploadOK
	case "error":
		return a.cfg.StickerError
	default:
		return ""
	}
}

func (a *App) sendEventSticker(chatID int64, event string) error {
	stickerID, _ := a.db.GetSetting("sticker_" + event)
	if stickerID == "" {
		stickerID = a.stickerFromConfig(event)
	}
	if stickerID == "" {
		return nil
	}
	if err := a.tg.SendSticker(chatID, stickerID); err != nil {
		log.Printf("failed to send sticker: chat_id=%d event=%s err=%v", chatID, event, err)
		return err
	}
	return nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
