package main

import (
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	StickerKindSticker     = "sticker"
	StickerKindCustomEmoji = "custom_emoji"
)

type StickerValue struct {
	Kind      string `json:"kind"`
	ID        string `json:"id"`
	UpdatedAt string `json:"updated_at"`
}

type stickerStoreFile struct {
	Events map[string]StickerValue `json:"events"`
}

type StickerStore struct {
	path   string
	mu     sync.RWMutex
	events map[string]StickerValue
}

var stickerEvents = []string{
	"welcome",
	"approved",
	"upload_ok",
	"error",
	"support",
	"donate",
	"language",
	"password",
	"premium",
	"backup",
	"sync",
}

func NewStickerStore(path string) *StickerStore {
	return &StickerStore{path: path, events: map[string]StickerValue{}}
}

func (s *StickerStore) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	raw, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	var payload stickerStoreFile
	if err := json.Unmarshal(raw, &payload); err != nil {
		return err
	}
	if payload.Events == nil {
		payload.Events = map[string]StickerValue{}
	}
	s.events = payload.Events
	return nil
}

func (s *StickerStore) Save() error {
	s.mu.RLock()
	payload := stickerStoreFile{Events: map[string]StickerValue{}}
	for event, value := range s.events {
		payload.Events[event] = value
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

func (s *StickerStore) Get(event string) (StickerValue, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	value, ok := s.events[event]
	return value, ok && value.ID != ""
}

func (s *StickerStore) Set(event, kind, id string) error {
	event = strings.TrimSpace(event)
	kind = strings.TrimSpace(kind)
	id = strings.TrimSpace(id)
	if event == "" || id == "" {
		return errors.New("empty sticker event or id")
	}
	if kind != StickerKindSticker && kind != StickerKindCustomEmoji {
		return errors.New("unsupported sticker kind")
	}
	s.mu.Lock()
	s.events[event] = StickerValue{Kind: kind, ID: id, UpdatedAt: time.Now().UTC().Format(time.RFC3339)}
	s.mu.Unlock()
	return s.Save()
}

func (s *StickerStore) Clear(event string) error {
	s.mu.Lock()
	delete(s.events, event)
	s.mu.Unlock()
	return s.Save()
}

func stickerEventAllowed(event string) bool {
	for _, item := range stickerEvents {
		if item == event {
			return true
		}
	}
	return false
}

func (a *App) sendEventSticker(chatID int64, event string) error {
	value, ok := a.stickers.Get(event)
	if !ok {
		return nil
	}
	var err error
	switch value.Kind {
	case StickerKindSticker:
		err = a.tg.SendSticker(chatID, value.ID)
	case StickerKindCustomEmoji:
		_, err = a.tg.SendMessage(chatID, customEmojiHTML(value.ID, eventMark(event)), nil)
	default:
		return nil
	}
	if err != nil {
		log.Printf("failed to send decoration: chat_id=%d event=%s kind=%s err=%v", chatID, event, value.Kind, err)
		return err
	}
	return nil
}

func firstCustomEmojiID(msg *Message) string {
	for _, entity := range msg.Entities {
		if entity.Type == "custom_emoji" && strings.TrimSpace(entity.CustomEmojiID) != "" {
			return entity.CustomEmojiID
		}
	}
	return ""
}

func customEmojiHTML(id, fallback string) string {
	if strings.TrimSpace(fallback) == "" {
		fallback = "✨"
	}
	return `<tg-emoji emoji-id="` + esc(id) + `">` + esc(fallback) + `</tg-emoji>`
}
