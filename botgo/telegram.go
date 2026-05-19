package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type Telegram struct {
	token           string
	apiURL          string
	fileURL         string
	localMode       bool
	localPathPrefix string
	botPathPrefix   string
	client          *http.Client
}

type tgResponse struct {
	OK          bool            `json:"ok"`
	Result      json.RawMessage `json:"result"`
	Description string          `json:"description"`
}

type Update struct {
	UpdateID         int               `json:"update_id"`
	Message          *Message          `json:"message"`
	CallbackQuery    *CallbackQuery    `json:"callback_query"`
	PreCheckoutQuery *PreCheckoutQuery `json:"pre_checkout_query"`
}

type Message struct {
	MessageID         int                `json:"message_id"`
	From              *TGUser            `json:"from"`
	Chat              Chat               `json:"chat"`
	Text              string             `json:"text"`
	Document          *TGFileInfo        `json:"document"`
	Photo             []TGPhoto          `json:"photo"`
	Video             *TGFileInfo        `json:"video"`
	Audio             *TGFileInfo        `json:"audio"`
	Voice             *TGFileInfo        `json:"voice"`
	VideoNote         *TGFileInfo        `json:"video_note"`
	Animation         *TGFileInfo        `json:"animation"`
	Sticker           *TGSticker         `json:"sticker"`
	Entities          []MessageEntity    `json:"entities"`
	SuccessfulPayment *SuccessfulPayment `json:"successful_payment"`
}

type TGUser struct {
	ID        int64  `json:"id"`
	Username  string `json:"username"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
}

type Chat struct {
	ID int64 `json:"id"`
}

type TGFileInfo struct {
	FileID   string `json:"file_id"`
	FileName string `json:"file_name"`
	FileSize int64  `json:"file_size"`
}

type TGPhoto struct {
	FileID   string `json:"file_id"`
	FileSize int64  `json:"file_size"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
}

type TGSticker struct {
	FileID string `json:"file_id"`
	Emoji  string `json:"emoji"`
}

type MessageEntity struct {
	Type          string `json:"type"`
	Offset        int    `json:"offset"`
	Length        int    `json:"length"`
	CustomEmojiID string `json:"custom_emoji_id"`
}

type CallbackQuery struct {
	ID      string   `json:"id"`
	From    TGUser   `json:"from"`
	Message *Message `json:"message"`
	Data    string   `json:"data"`
}

type PreCheckoutQuery struct {
	ID              string `json:"id"`
	From            TGUser `json:"from"`
	InvoicePayload  string `json:"invoice_payload"`
	TotalAmount     int    `json:"total_amount"`
	Currency        string `json:"currency"`
}

type SuccessfulPayment struct {
	Currency                string `json:"currency"`
	TotalAmount             int    `json:"total_amount"`
	InvoicePayload          string `json:"invoice_payload"`
	TelegramPaymentChargeID  string `json:"telegram_payment_charge_id"`
	ProviderPaymentChargeID  string `json:"provider_payment_charge_id"`
}

type BotFile struct {
	FileID   string `json:"file_id"`
	FilePath string `json:"file_path"`
	FileSize int64  `json:"file_size"`
}

type InlineKeyboardMarkup struct {
	InlineKeyboard [][]InlineKeyboardButton `json:"inline_keyboard"`
}

type InlineKeyboardButton struct {
	Text         string `json:"text"`
	CallbackData string `json:"callback_data,omitempty"`
	URL          string `json:"url,omitempty"`
}

type LabeledPrice struct {
	Label  string `json:"label"`
	Amount int    `json:"amount"`
}

func (tg *Telegram) call(method string, payload any, out any) error {
	body, _ := json.Marshal(payload)
	req, err := http.NewRequest(http.MethodPost, tg.apiURL+method, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := tg.client.Do(req)
	if err != nil {
		return fmt.Errorf("Telegram %s request failed: %s", method, tg.cleanError(err))
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	var envelope tgResponse
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return fmt.Errorf("Telegram returned non-JSON response (%d): %s", resp.StatusCode, string(raw[:min(len(raw), 300)]))
	}
	if !envelope.OK {
		return errors.New(tg.cleanText(envelope.Description))
	}
	if out != nil && len(envelope.Result) > 0 {
		return json.Unmarshal(envelope.Result, out)
	}
	return nil
}

func (tg *Telegram) GetUpdates(offset int) ([]Update, error) {
	var updates []Update
	err := tg.call("getUpdates", map[string]any{
		"offset":  offset,
		"timeout": 50,
		"allowed_updates": []string{
			"message", "callback_query", "pre_checkout_query",
		},
	}, &updates)
	return updates, err
}

func (tg *Telegram) SendMessage(chatID int64, text string, markup *InlineKeyboardMarkup) (*Message, error) {
	payload := map[string]any{"chat_id": chatID, "text": text, "parse_mode": "HTML", "disable_web_page_preview": true}
	if markup != nil {
		payload["reply_markup"] = markup
	}
	var msg Message
	err := tg.call("sendMessage", payload, &msg)
	return &msg, err
}

func (tg *Telegram) SendPhoto(chatID int64, photoID, caption string, markup *InlineKeyboardMarkup) (*Message, error) {
	payload := map[string]any{"chat_id": chatID, "photo": photoID, "parse_mode": "HTML"}
	if caption != "" {
		payload["caption"] = caption
	}
	if markup != nil {
		payload["reply_markup"] = markup
	}
	var msg Message
	err := tg.call("sendPhoto", payload, &msg)
	return &msg, err
}

func (tg *Telegram) SendSticker(chatID int64, stickerID string) error {
	if strings.TrimSpace(stickerID) == "" {
		return nil
	}
	return tg.call("sendSticker", map[string]any{"chat_id": chatID, "sticker": stickerID}, nil)
}

func (tg *Telegram) EditMessageText(chatID int64, messageID int, text string, markup *InlineKeyboardMarkup) error {
	payload := map[string]any{"chat_id": chatID, "message_id": messageID, "text": text, "parse_mode": "HTML", "disable_web_page_preview": true}
	if markup != nil {
		payload["reply_markup"] = markup
	}
	err := tg.call("editMessageText", payload, nil)
	if err != nil && strings.Contains(err.Error(), "message is not modified") {
		return nil
	}
	return err
}

func (tg *Telegram) AnswerCallback(id, text string, alert bool) {
	payload := map[string]any{"callback_query_id": id, "show_alert": alert}
	if text != "" {
		payload["text"] = text
	}
	if err := tg.call("answerCallbackQuery", payload, nil); err != nil {
		log.Printf("answer callback failed: %v", err)
	}
}

func (tg *Telegram) AnswerPreCheckout(id string, ok bool) {
	if err := tg.call("answerPreCheckoutQuery", map[string]any{"pre_checkout_query_id": id, "ok": ok}, nil); err != nil {
		log.Printf("answer pre-checkout failed: %v", err)
	}
}

func (tg *Telegram) SendInvoice(chatID int64, title, description, payload string, amount int) error {
	return tg.call("sendInvoice", map[string]any{
		"chat_id":        chatID,
		"title":          title,
		"description":    description,
		"payload":        payload,
		"provider_token": "",
		"currency":       "XTR",
		"prices":         []LabeledPrice{{Label: fmt.Sprintf("%d Stars", amount), Amount: amount}},
	}, nil)
}

func (tg *Telegram) GetFile(fileID string) (*BotFile, error) {
	var file BotFile
	err := tg.call("getFile", map[string]any{"file_id": fileID}, &file)
	return &file, err
}

func (tg *Telegram) DownloadFile(fileID string) (string, bool, error) {
	file, err := tg.GetFile(fileID)
	if err != nil {
		return "", false, err
	}
	if tg.localMode && filepath.IsAbs(file.FilePath) {
		path := tg.mapLocalFilePath(file.FilePath)
		if _, err := os.Stat(path); err != nil {
			return "", false, fmt.Errorf("Telegram local file is not readable at %s: %w. Mount Telegram Bot API data into the bot container or set TELEGRAM_LOCAL_PATH_PREFIX/TELEGRAM_BOT_PATH_PREFIX", path, err)
		}
		return path, false, nil
	}
	resp, err := tg.client.Get(tg.fileURL + file.FilePath)
	if err != nil {
		return "", false, fmt.Errorf("Telegram file download failed: %s", tg.cleanError(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", false, fmt.Errorf("Telegram file download HTTP %d: %s", resp.StatusCode, string(raw))
	}
	tmp, err := os.CreateTemp("", "tg-nextcloud-*")
	if err != nil {
		return "", false, err
	}
	defer tmp.Close()
	if _, err := io.Copy(tmp, resp.Body); err != nil {
		_ = os.Remove(tmp.Name())
		return "", false, err
	}
	return tmp.Name(), true, nil
}

func (tg *Telegram) mapLocalFilePath(path string) string {
	if tg.localPathPrefix != "" && tg.botPathPrefix != "" && strings.HasPrefix(path, tg.localPathPrefix) {
		return tg.botPathPrefix + strings.TrimPrefix(path, tg.localPathPrefix)
	}
	return path
}

func (tg *Telegram) CopyMessage(chatID, fromChatID int64, messageID int) error {
	return tg.call("copyMessage", map[string]any{"chat_id": chatID, "from_chat_id": fromChatID, "message_id": messageID}, nil)
}

func (tg *Telegram) SendDocument(chatID int64, path string, caption string) error {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	_ = writer.WriteField("chat_id", strconv.FormatInt(chatID, 10))
	if caption != "" {
		_ = writer.WriteField("caption", caption)
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	part, err := writer.CreateFormFile("document", filepath.Base(path))
	if err != nil {
		return err
	}
	if _, err := io.Copy(part, file); err != nil {
		return err
	}
	_ = writer.Close()
	req, err := http.NewRequest(http.MethodPost, tg.apiURL+"sendDocument", &body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	resp, err := tg.client.Do(req)
	if err != nil {
		return fmt.Errorf("Telegram sendDocument failed: %s", tg.cleanError(err))
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	var envelope tgResponse
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return err
	}
	if !envelope.OK {
		return errors.New(tg.cleanText(envelope.Description))
	}
	return nil
}

func (tg *Telegram) cleanError(err error) string {
	if err == nil {
		return ""
	}
	return tg.cleanText(err.Error())
}

func (tg *Telegram) cleanText(text string) string {
	if tg.token != "" {
		text = strings.ReplaceAll(text, tg.token, "<bot-token>")
	}
	return text
}
