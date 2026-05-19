package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

type ContentItem struct {
	Key         string
	Title       string
	DefaultText string
}

type ContentStore struct {
	path     string
	mu       sync.RWMutex
	messages map[string]string
	buttons  map[string]string
	photos   map[string]string
}

type contentStoreFile struct {
	Messages map[string]string `json:"messages"`
	Buttons  map[string]string `json:"buttons"`
	Photos   map[string]string `json:"photos"`
}

var contentMessages = []ContentItem{
	{Key: "account_home", Title: "Главное сообщение пользователя", DefaultText: "☁️✨ <b>Ваше облако</b> ✨\n<code>━━━━━━━━━━━━━━━━━━━━</code>\n\n{premium}🌐 Ссылка: <a href=\"{cloud_url}\">{cloud_url}</a>\n🆔 Логин: <code>{login}</code>\n🔐 Пароль: {password}\n💾 Квота: <b>{quota_gb} GB</b>\n\n{storage}\n\n📤 Отправьте файл в этот чат, и бот загрузит его в облако."},
	{Key: "access_sent", Title: "Заявка отправлена", DefaultText: "<b>Заявка отправлена ✨</b>\n\nАдминистратор проверит доступ. Я сообщу, когда аккаунт будет готов."},
	{Key: "approved", Title: "Доступ одобрен", DefaultText: "✅ <b>Доступ открыт</b>\n<code>━━━━━━━━━━━━━━━━━━━━</code>\n\n🆔 Логин: <code>{login}</code>\n🔐 Пароль: <code>{password}</code>\n💾 Квота: <b>{quota_gb} GB</b>\n\n📤 Файлы можно отправлять прямо сюда: бот загрузит их в облако.\nПароль всегда виден в /start, там же его можно сменить."},
	{Key: "rejected", Title: "Заявка отклонена", DefaultText: "Ваша заявка сейчас отклонена."},
	{Key: "support", Title: "Поддержка", DefaultText: "<b>💬 Поддержка</b>\n\n{support_contacts}"},
	{Key: "donate", Title: "Донат", DefaultText: "<b>💙 Поддержать проект</b>\n\nМожно поддержать проект через Telegram Stars, Platega, Pally, CryptoBot, Heleket или внешнюю ссылку.\nВыберите способ поддержки."},
	{Key: "premium_info", Title: "Описание премиума", DefaultText: "<b>⭐ Премиум</b>\n\nПремиум дает приоритет в очереди загрузок, отдельную иконку поддержавшего и скидку на докупку места."},
	{Key: "info", Title: "Инфо и соглашения", DefaultText: "<b>ℹ️ Информация</b>\n\nПользовательское соглашение, политика хранения файлов и контакты поддержки настраиваются администратором в этой панели."},
	{Key: "maintenance", Title: "Техработы", DefaultText: "<b>🛠️ Технические работы</b>\n\nСервис временно недоступен. Попробуйте позже."},
	{Key: "password_changed", Title: "Пароль сменен", DefaultText: "✅ Пароль сменен.\n\nЛогин: <code>{login}</code>\nПароль: <code>{password}</code>"},
	{Key: "upload_too_large", Title: "Файл больше лимита", DefaultText: "⚠️ Telegram не дает боту скачать этот файл: он больше <b>{limit_mb} MB</b>.\n\nЗагрузите большой файл напрямую через веб-интерфейс облака."},
}

var contentButtons = []ContentItem{
	{Key: "cloud_ru", Title: "Кнопка Войти в облако", DefaultText: "☁️ Войти в облако"},
	{Key: "change_password_ru", Title: "Кнопка Сменить пароль", DefaultText: "🔐 Сменить пароль"},
	{Key: "support_ru", Title: "Кнопка Поддержка", DefaultText: "💬 Поддержка"},
	{Key: "donate_ru", Title: "Кнопка Донат", DefaultText: "💙 Донат"},
	{Key: "buy_storage_ru", Title: "Кнопка Докупить место", DefaultText: "💾 Докупить место"},
	{Key: "promo_ru", Title: "Кнопка Промокод", DefaultText: "🎟 Промокод"},
	{Key: "info_ru", Title: "Кнопка Инфо", DefaultText: "ℹ️ Инфо"},
	{Key: "language_ru", Title: "Кнопка Язык", DefaultText: "🌐 Язык"},
	{Key: "cloud_en", Title: "Button Open cloud", DefaultText: "☁️ Open cloud"},
	{Key: "change_password_en", Title: "Button Change password", DefaultText: "🔐 Change password"},
	{Key: "support_en", Title: "Button Support", DefaultText: "💬 Support"},
	{Key: "donate_en", Title: "Button Donate", DefaultText: "💙 Donate"},
	{Key: "buy_storage_en", Title: "Button Buy storage", DefaultText: "💾 Buy storage"},
	{Key: "promo_en", Title: "Button Promo code", DefaultText: "🎟 Promo code"},
	{Key: "info_en", Title: "Button Info", DefaultText: "ℹ️ Info"},
	{Key: "language_en", Title: "Button Language", DefaultText: "🌐 Language"},
}

func NewContentStore(path string) *ContentStore {
	return &ContentStore{path: path, messages: map[string]string{}, buttons: map[string]string{}, photos: map[string]string{}}
}

func (s *ContentStore) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	raw, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	var payload contentStoreFile
	if err := json.Unmarshal(raw, &payload); err != nil {
		return err
	}
	if payload.Messages != nil {
		s.messages = payload.Messages
	}
	if payload.Buttons != nil {
		s.buttons = payload.Buttons
	}
	if payload.Photos != nil {
		s.photos = payload.Photos
	}
	return nil
}

func (s *ContentStore) Save() error {
	s.mu.RLock()
	payload := contentStoreFile{Messages: map[string]string{}, Buttons: map[string]string{}, Photos: map[string]string{}}
	for key, value := range s.messages {
		payload.Messages[key] = value
	}
	for key, value := range s.buttons {
		payload.Buttons[key] = value
	}
	for key, value := range s.photos {
		payload.Photos[key] = value
	}
	s.mu.RUnlock()
	raw, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(s.path, raw, 0o600)
}

func (s *ContentStore) Message(key string, vars map[string]string) string {
	return renderTemplate(s.messageRaw(key), vars)
}

func (s *ContentStore) Button(key string) string {
	s.mu.RLock()
	value := strings.TrimSpace(s.buttons[key])
	s.mu.RUnlock()
	if value != "" {
		return value
	}
	return contentDefault(contentButtons, key)
}

func (s *ContentStore) Photo(key string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return strings.TrimSpace(s.photos[key])
}

func (s *ContentStore) SetMessage(key, value string) error {
	if !contentKeyExists(contentMessages, key) {
		return errors.New("unknown message key")
	}
	s.mu.Lock()
	s.messages[key] = value
	s.mu.Unlock()
	return s.Save()
}

func (s *ContentStore) SetButton(key, value string) error {
	if !contentKeyExists(contentButtons, key) {
		return errors.New("unknown button key")
	}
	s.mu.Lock()
	s.buttons[key] = strings.TrimSpace(value)
	s.mu.Unlock()
	return s.Save()
}

func (s *ContentStore) SetPhoto(key, fileID string) error {
	if !contentKeyExists(contentMessages, key) {
		return errors.New("unknown message key")
	}
	s.mu.Lock()
	s.photos[key] = strings.TrimSpace(fileID)
	s.mu.Unlock()
	return s.Save()
}

func (s *ContentStore) ResetMessage(key string) error {
	s.mu.Lock()
	delete(s.messages, key)
	s.mu.Unlock()
	return s.Save()
}

func (s *ContentStore) ResetButton(key string) error {
	s.mu.Lock()
	delete(s.buttons, key)
	s.mu.Unlock()
	return s.Save()
}

func (s *ContentStore) ResetPhoto(key string) error {
	s.mu.Lock()
	delete(s.photos, key)
	s.mu.Unlock()
	return s.Save()
}

func (s *ContentStore) messageRaw(key string) string {
	s.mu.RLock()
	value := strings.TrimSpace(s.messages[key])
	s.mu.RUnlock()
	if value != "" {
		return value
	}
	return contentDefault(contentMessages, key)
}

func contentDefault(items []ContentItem, key string) string {
	for _, item := range items {
		if item.Key == key {
			return item.DefaultText
		}
	}
	return ""
}

func contentTitle(items []ContentItem, key string) string {
	for _, item := range items {
		if item.Key == key {
			return item.Title
		}
	}
	return key
}

func contentKeyExists(items []ContentItem, key string) bool {
	for _, item := range items {
		if item.Key == key {
			return true
		}
	}
	return false
}

func sortedContentKeys(items []ContentItem) []string {
	keys := make([]string, 0, len(items))
	for _, item := range items {
		keys = append(keys, item.Key)
	}
	sort.Strings(keys)
	return keys
}

func renderTemplate(text string, vars map[string]string) string {
	for key, value := range vars {
		text = strings.ReplaceAll(text, "{"+key+"}", value)
	}
	return text
}

func messageHTMLWithCustomEmoji(msg *Message) string {
	if msg == nil || len(msg.Entities) == 0 {
		return strings.TrimSpace(msg.Text)
	}
	entities := make([]MessageEntity, 0, len(msg.Entities))
	for _, entity := range msg.Entities {
		if entity.Type == "custom_emoji" && entity.CustomEmojiID != "" && entity.Length > 0 {
			entities = append(entities, entity)
		}
	}
	if len(entities) == 0 {
		return strings.TrimSpace(msg.Text)
	}
	sort.Slice(entities, func(i, j int) bool { return entities[i].Offset < entities[j].Offset })
	var out strings.Builder
	last := 0
	for _, entity := range entities {
		start := utf16OffsetToByteIndex(msg.Text, entity.Offset)
		end := utf16OffsetToByteIndex(msg.Text, entity.Offset+entity.Length)
		if start < last || start > len(msg.Text) || end < start || end > len(msg.Text) {
			continue
		}
		out.WriteString(msg.Text[last:start])
		fallback := msg.Text[start:end]
		if strings.TrimSpace(fallback) == "" {
			fallback = "✨"
		}
		out.WriteString(customEmojiHTML(entity.CustomEmojiID, fallback))
		last = end
	}
	out.WriteString(msg.Text[last:])
	return strings.TrimSpace(out.String())
}

func utf16OffsetToByteIndex(text string, target int) int {
	if target <= 0 {
		return 0
	}
	units := 0
	for index, r := range text {
		if units >= target {
			return index
		}
		step := 1
		if r > 0xFFFF {
			step = 2
		}
		if units+step > target {
			return index
		}
		units += step
	}
	return len(text)
}
