package main

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"container/heap"
	"crypto/rand"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"html"
	"io"
	"log"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const pageSize = 8

type Config struct {
	BotToken                     string
	AdminIDs                     map[int64]bool
	NextcloudURL                 string
	NextcloudInternalURL         string
	NextcloudAdminUser           string
	NextcloudAdminPassword       string
	DefaultQuotaGB               int
	DatabaseURL                  string
	DatabaseAPIToken             string
	RedisURL                     string
	BackupDir                    string
	LogDir                       string
	EnableSupportBlock           bool
	SupportTelegram              string
	SupportEmail                 string
	EnableDonateBlock            bool
	DonateURL                    string
	TelegramStarsEnabled         bool
	TelegramStarsAmounts         []int
	PlategaEnabled               bool
	PlategaURL                   string
	PlategaMerchantID            string
	PlategaSecret                string
	PlategaBaseURL               string
	PlategaAmountsRUB            []int
	PlategaReturnURL             string
	PlategaFailedURL             string
	TelegramMaxDownloadMB        int
	PremiumDays                  int
	BackupRetentionDays          int
	AutoBackupIntervalHours      int
	NextcloudSyncIntervalMinutes int
	StickerWelcome               string
	StickerApproved              string
	StickerUploadOK              string
	StickerError                 string
}

type App struct {
	cfg       Config
	tg        *Telegram
	db        *DB
	nc        *Nextcloud
	platega   *Platega
	states    *StateStore
	uploads   *UploadQueue
	uploadSeq int64
}

type User struct {
	TelegramID     int64   `json:"telegram_id"`
	Username       *string `json:"username"`
	FirstName      *string `json:"first_name"`
	LastName       *string `json:"last_name"`
	Status         string  `json:"status"`
	Language       string  `json:"language"`
	NCUserID       *string `json:"nc_user_id"`
	NCPassword     *string `json:"nc_password"`
	QuotaGB        int     `json:"quota_gb"`
	IsSupporter    int     `json:"is_supporter"`
	SupporterUntil *string `json:"supporter_until"`
	IsDisabled     int     `json:"is_disabled"`
	CreatedAt      string  `json:"created_at"`
	UpdatedAt      string  `json:"updated_at"`
	ApprovedAt     *string `json:"approved_at"`
}

type Payment struct {
	TransactionID string `json:"transaction_id"`
	TelegramID    int64  `json:"telegram_id"`
	Provider      string `json:"provider"`
	Amount        int    `json:"amount"`
	Currency      string `json:"currency"`
	Status        string `json:"status"`
}

type StateKind string

const (
	StateNone           StateKind = ""
	StateChangePassword StateKind = "change_password"
	StateQuotaCustom    StateKind = "quota_custom"
	StateAdminSearch    StateKind = "admin_search"
	StateBroadcast      StateKind = "broadcast"
	StateSticker        StateKind = "sticker"
)

type State struct {
	Kind     StateKind
	TargetID int64
	Event    string
}

type StateStore struct {
	mu     sync.Mutex
	values map[int64]State
	redis  *RedisClient
}

func NewStateStore(redis *RedisClient) *StateStore {
	return &StateStore{values: map[int64]State{}, redis: redis}
}

func (s *StateStore) Set(id int64, st State) {
	s.mu.Lock()
	s.values[id] = st
	s.mu.Unlock()
	if s.redis != nil {
		raw, _ := json.Marshal(st)
		if err := s.redis.SetEX("state:"+strconv.FormatInt(id, 10), string(raw), 6*time.Hour); err != nil {
			log.Printf("redis state set failed: %v", err)
		}
	}
}

func (s *StateStore) Get(id int64) (State, bool) {
	if s.redis != nil {
		raw, err := s.redis.Get("state:" + strconv.FormatInt(id, 10))
		if err == nil && raw != "" {
			var st State
			if json.Unmarshal([]byte(raw), &st) == nil {
				return st, true
			}
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.values[id]
	return st, ok
}

func (s *StateStore) Clear(id int64) {
	s.mu.Lock()
	delete(s.values, id)
	s.mu.Unlock()
	if s.redis != nil {
		if err := s.redis.Del("state:" + strconv.FormatInt(id, 10)); err != nil {
			log.Printf("redis state del failed: %v", err)
		}
	}
}

type RedisClient struct {
	addr     string
	password string
	db       string
}

func NewRedisClient(rawURL string) (*RedisClient, error) {
	if strings.TrimSpace(rawURL) == "" {
		return nil, nil
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	addr := parsed.Host
	if !strings.Contains(addr, ":") {
		addr += ":6379"
	}
	password, _ := parsed.User.Password()
	db := strings.TrimPrefix(parsed.Path, "/")
	return &RedisClient{addr: addr, password: password, db: db}, nil
}

func (r *RedisClient) SetEX(key, value string, ttl time.Duration) error {
	_, err := r.do("SETEX", key, strconv.Itoa(int(ttl.Seconds())), value)
	return err
}

func (r *RedisClient) Get(key string) (string, error) {
	value, err := r.do("GET", key)
	if err != nil {
		return "", err
	}
	if value == nil {
		return "", nil
	}
	return fmt.Sprint(value), nil
}

func (r *RedisClient) Del(key string) error {
	_, err := r.do("DEL", key)
	return err
}

func (r *RedisClient) do(args ...string) (any, error) {
	conn, err := net.DialTimeout("tcp", r.addr, 3*time.Second)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	reader := bufio.NewReader(conn)
	if r.password != "" {
		if _, err := writeRedisCommand(conn, "AUTH", r.password); err != nil {
			return nil, err
		}
		if _, err := readRedisReply(reader); err != nil {
			return nil, err
		}
	}
	if r.db != "" && r.db != "0" {
		if _, err := writeRedisCommand(conn, "SELECT", r.db); err != nil {
			return nil, err
		}
		if _, err := readRedisReply(reader); err != nil {
			return nil, err
		}
	}
	if _, err := writeRedisCommand(conn, args...); err != nil {
		return nil, err
	}
	return readRedisReply(reader)
}

func writeRedisCommand(w io.Writer, args ...string) (int, error) {
	var b strings.Builder
	b.WriteString("*")
	b.WriteString(strconv.Itoa(len(args)))
	b.WriteString("\r\n")
	for _, arg := range args {
		b.WriteString("$")
		b.WriteString(strconv.Itoa(len(arg)))
		b.WriteString("\r\n")
		b.WriteString(arg)
		b.WriteString("\r\n")
	}
	return io.WriteString(w, b.String())
}

func readRedisReply(r *bufio.Reader) (any, error) {
	prefix, err := r.ReadByte()
	if err != nil {
		return nil, err
	}
	line, err := r.ReadString('\n')
	if err != nil {
		return nil, err
	}
	line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
	switch prefix {
	case '+':
		return line, nil
	case '-':
		return nil, errors.New(line)
	case ':':
		return strconv.ParseInt(line, 10, 64)
	case '$':
		size, _ := strconv.Atoi(line)
		if size < 0 {
			return nil, nil
		}
		buf := make([]byte, size+2)
		if _, err := io.ReadFull(r, buf); err != nil {
			return nil, err
		}
		return string(buf[:size]), nil
	case '*':
		count, _ := strconv.Atoi(line)
		items := make([]any, 0, count)
		for i := 0; i < count; i++ {
			item, err := readRedisReply(r)
			if err != nil {
				return nil, err
			}
			items = append(items, item)
		}
		return items, nil
	default:
		return nil, fmt.Errorf("unknown redis reply prefix %q", prefix)
	}
}

type DB struct {
	baseURL string
	token   string
	client  *http.Client
}

type rpcResponse struct {
	OK    bool            `json:"ok"`
	Data  json.RawMessage `json:"data"`
	Error string          `json:"error"`
}

func (db *DB) rpc(method string, params map[string]any, out any) error {
	body, _ := json.Marshal(map[string]any{"method": method, "params": params})
	req, err := http.NewRequest(http.MethodPost, db.baseURL+"/rpc", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if db.token != "" {
		req.Header.Set("Authorization", "Bearer "+db.token)
	}
	resp, err := db.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	var payload rpcResponse
	if err := json.Unmarshal(raw, &payload); err != nil {
		return fmt.Errorf("DB returned non-JSON response (%d): %s", resp.StatusCode, string(raw[:min(len(raw), 300)]))
	}
	if resp.StatusCode >= 400 || !payload.OK {
		return errors.New(payload.Error)
	}
	if out != nil && len(payload.Data) > 0 && string(payload.Data) != "null" {
		return json.Unmarshal(payload.Data, out)
	}
	return nil
}

func (db *DB) Init() error {
	return db.rpc("init", map[string]any{}, nil)
}

func (db *DB) UpsertRequest(id int64, username, firstName, lastName *string) (*User, error) {
	var user User
	err := db.rpc("upsert_request", map[string]any{
		"telegram_id": id,
		"username":    username,
		"first_name":  firstName,
		"last_name":   lastName,
	}, &user)
	return &user, err
}

func (db *DB) GetUser(id int64) (*User, error) {
	var user *User
	err := db.rpc("get_user", map[string]any{"telegram_id": id}, &user)
	return user, err
}

func (db *DB) ApproveUser(id int64, ncUserID, password string, quota int) error {
	return db.rpc("approve_user", map[string]any{
		"telegram_id": id, "nc_user_id": ncUserID, "nc_password": password, "quota_gb": quota,
	}, nil)
}

func (db *DB) SetNextcloudPassword(id int64, password string) error {
	return db.rpc("set_nextcloud_password", map[string]any{"telegram_id": id, "nc_password": password}, nil)
}

func (db *DB) RejectUser(id int64) error {
	return db.rpc("reject_user", map[string]any{"telegram_id": id}, nil)
}

func (db *DB) SetLanguage(id int64, language string) error {
	return db.rpc("set_language", map[string]any{"telegram_id": id, "language": language}, nil)
}

func (db *DB) SetQuota(id int64, quota int) error {
	return db.rpc("set_quota", map[string]any{"telegram_id": id, "quota_gb": quota}, nil)
}

func (db *DB) SetDisabled(id int64, disabled bool) error {
	return db.rpc("set_disabled", map[string]any{"telegram_id": id, "is_disabled": disabled}, nil)
}

func (db *DB) DeleteUser(id int64) error {
	return db.rpc("delete_user", map[string]any{"telegram_id": id}, nil)
}

func (db *DB) SetSupporter(id int64, enabled bool, until *string) error {
	return db.rpc("set_supporter", map[string]any{"telegram_id": id, "is_supporter": enabled, "supporter_until": until}, nil)
}

func (db *DB) ExpireSupporters() (int, error) {
	var n int
	err := db.rpc("expire_supporters", map[string]any{}, &n)
	return n, err
}

func (db *DB) ListUsers(status string, limit, offset int) ([]User, error) {
	var users []User
	params := map[string]any{"limit": limit, "offset": offset}
	if status != "" {
		params["status"] = status
	}
	err := db.rpc("list_users", params, &users)
	return users, err
}

func (db *DB) SearchUsers(query string, limit int) ([]User, error) {
	var users []User
	err := db.rpc("search_users", map[string]any{"query": query, "limit": limit}, &users)
	return users, err
}

func (db *DB) CountUsers(status string) (int, error) {
	var n int
	params := map[string]any{}
	if status != "" {
		params["status"] = status
	}
	err := db.rpc("count_users", params, &n)
	return n, err
}

func (db *DB) ApprovedTelegramIDs() ([]int64, error) {
	var ids []int64
	err := db.rpc("approved_telegram_ids", map[string]any{}, &ids)
	return ids, err
}

func (db *DB) RestoreUsers(users []User) error {
	return db.rpc("restore_users", map[string]any{"users": users}, nil)
}

func (db *DB) GetSetting(key string) (string, error) {
	var value *string
	err := db.rpc("get_setting", map[string]any{"key": key}, &value)
	if value == nil {
		return "", err
	}
	return *value, err
}

func (db *DB) SetSetting(key, value string) error {
	return db.rpc("set_setting", map[string]any{"key": key, "value": value}, nil)
}

func (db *DB) ListSettings(prefix string) (map[string]string, error) {
	var settings map[string]string
	err := db.rpc("list_settings", map[string]any{"prefix": prefix}, &settings)
	if settings == nil {
		settings = map[string]string{}
	}
	return settings, err
}

func (db *DB) CreatePayment(transactionID string, telegramID int64, provider string, amount int, currency, status string, paymentURL *string, payload *string) error {
	return db.rpc("create_payment", map[string]any{
		"transaction_id": transactionID,
		"telegram_id":    telegramID,
		"provider":       provider,
		"amount":         amount,
		"currency":       currency,
		"status":         status,
		"payment_url":    paymentURL,
		"payload":        payload,
	}, nil)
}

func (db *DB) GetPayment(transactionID string) (*Payment, error) {
	var payment *Payment
	err := db.rpc("get_payment", map[string]any{"transaction_id": transactionID}, &payment)
	return payment, err
}

func (db *DB) UpdatePaymentStatus(transactionID, status string) error {
	return db.rpc("update_payment_status", map[string]any{"transaction_id": transactionID, "status": status}, nil)
}

type Telegram struct {
	token   string
	apiURL  string
	fileURL string
	client  *http.Client
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
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	var envelope tgResponse
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return fmt.Errorf("Telegram returned non-JSON response (%d): %s", resp.StatusCode, string(raw[:min(len(raw), 300)]))
	}
	if !envelope.OK {
		return errors.New(envelope.Description)
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

func (tg *Telegram) DownloadFile(fileID string) (string, error) {
	file, err := tg.GetFile(fileID)
	if err != nil {
		return "", err
	}
	resp, err := tg.client.Get(tg.fileURL + file.FilePath)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", fmt.Errorf("Telegram file download HTTP %d: %s", resp.StatusCode, string(raw))
	}
	tmp, err := os.CreateTemp("", "tg-nextcloud-*")
	if err != nil {
		return "", err
	}
	defer tmp.Close()
	if _, err := io.Copy(tmp, resp.Body); err != nil {
		_ = os.Remove(tmp.Name())
		return "", err
	}
	return tmp.Name(), nil
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
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	var envelope tgResponse
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return err
	}
	if !envelope.OK {
		return errors.New(envelope.Description)
	}
	return nil
}

type Nextcloud struct {
	baseURL  string
	username string
	password string
	client   *http.Client
}

type Platega struct {
	merchantID string
	secret     string
	baseURL    string
	client     *http.Client
}

func (p *Platega) request(method, path string, body any) (map[string]any, error) {
	var reader io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, strings.TrimRight(p.baseURL, "/")+path, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-MerchantId", p.merchantID)
	req.Header.Set("X-Secret", p.secret)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "telegram-nextcloud-bot-go/1.0")
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("Platega returned non-JSON response (%d): %s", resp.StatusCode, string(raw[:min(len(raw), 300)]))
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("Platega HTTP %d: %s", resp.StatusCode, string(raw[:min(len(raw), 300)]))
	}
	return payload, nil
}

func (p *Platega) CreatePayment(amount int, description, payload, returnURL, failedURL string) (map[string]any, error) {
	body := map[string]any{
		"paymentDetails": map[string]any{"amount": amount, "currency": "RUB"},
		"description":    description,
		"payload":        payload,
	}
	if returnURL != "" {
		body["return"] = returnURL
	}
	if failedURL != "" {
		body["failedUrl"] = failedURL
	}
	data, err := p.request(http.MethodPost, "/v2/transaction/process", body)
	if err != nil {
		return nil, err
	}
	transactionID := fmt.Sprint(data["transactionId"])
	paymentURL := fmt.Sprint(data["url"])
	if transactionID == "" || transactionID == "<nil>" || paymentURL == "" || paymentURL == "<nil>" {
		return nil, errors.New("Platega response does not contain transactionId or url")
	}
	return data, nil
}

func (p *Platega) Transaction(transactionID string) (map[string]any, error) {
	return p.request(http.MethodGet, "/transaction/"+url.PathEscape(transactionID), nil)
}

func (nc *Nextcloud) ocs(method, path string, data url.Values) (map[string]any, error) {
	endpoint := nc.baseURL + path
	var body io.Reader
	if data != nil {
		body = strings.NewReader(data.Encode())
	}
	req, err := http.NewRequest(method, endpoint, body)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(nc.username, nc.password)
	req.Header.Set("OCS-APIRequest", "true")
	req.Header.Set("Accept", "application/json")
	if data != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	resp, err := nc.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("Nextcloud returned non-JSON response (%d): %s", resp.StatusCode, string(raw[:min(len(raw), 300)]))
	}
	ocs, _ := payload["ocs"].(map[string]any)
	meta, _ := ocs["meta"].(map[string]any)
	status := strings.ToLower(fmt.Sprint(meta["status"]))
	statusCode := intFromAny(meta["statuscode"])
	message := fmt.Sprint(meta["message"])
	if resp.StatusCode >= 400 {
		if statusCode == 403 && strings.Contains(message, "Password confirmation is required") {
			return nil, errors.New("Nextcloud требует подтверждение пароля. Укажите app password администратора в NEXTCLOUD_ADMIN_PASSWORD и проверьте Provisioning API")
		}
		return nil, fmt.Errorf("Nextcloud HTTP %d: %s", resp.StatusCode, string(raw[:min(len(raw), 300)]))
	}
	if status != "ok" && !containsInt([]int{100, 200, 201, 202, 204}, statusCode) {
		return nil, fmt.Errorf("Nextcloud OCS %d: %s", statusCode, message)
	}
	dataMap, _ := ocs["data"].(map[string]any)
	if dataMap == nil {
		dataMap = map[string]any{}
	}
	return dataMap, nil
}

func (nc *Nextcloud) UserExists(userID string) (bool, error) {
	_, err := nc.ocs(http.MethodGet, "/ocs/v2.php/cloud/users/"+url.PathEscape(userID), nil)
	if err == nil {
		return true, nil
	}
	if strings.Contains(err.Error(), "OCS 101") || strings.Contains(err.Error(), "OCS 404") || strings.Contains(err.Error(), "HTTP 404") {
		return false, nil
	}
	return false, err
}

func (nc *Nextcloud) CreateUser(userID, password string) error {
	data := url.Values{"userid": {userID}, "password": {password}}
	_, err := nc.ocs(http.MethodPost, "/ocs/v2.php/cloud/users", data)
	return err
}

func (nc *Nextcloud) SetUserValue(userID, key, value string) error {
	data := url.Values{"key": {key}, "value": {value}}
	_, err := nc.ocs(http.MethodPut, "/ocs/v2.php/cloud/users/"+url.PathEscape(userID), data)
	return err
}

func (nc *Nextcloud) EnsureUser(userID, password string, quota int) error {
	exists, err := nc.UserExists(userID)
	if err != nil {
		return err
	}
	if exists {
		if err := nc.SetUserValue(userID, "password", password); err != nil {
			return err
		}
	} else if err := nc.CreateUser(userID, password); err != nil {
		return err
	}
	if err := nc.SetQuota(userID, quota); err != nil {
		return err
	}
	return nc.EnableUser(userID)
}

func (nc *Nextcloud) SetQuota(userID string, quota int) error {
	return nc.SetUserValue(userID, "quota", fmt.Sprintf("%d GB", quota))
}

func (nc *Nextcloud) EnableUser(userID string) error {
	_, err := nc.ocs(http.MethodPut, "/ocs/v1.php/cloud/users/"+url.PathEscape(userID)+"/enable", nil)
	return err
}

func (nc *Nextcloud) DisableUser(userID string) error {
	_, err := nc.ocs(http.MethodPut, "/ocs/v1.php/cloud/users/"+url.PathEscape(userID)+"/disable", nil)
	return err
}

func (nc *Nextcloud) DeleteUser(userID string) error {
	_, err := nc.ocs(http.MethodDelete, "/ocs/v2.php/cloud/users/"+url.PathEscape(userID), nil)
	return err
}

func (nc *Nextcloud) CheckConnection() error {
	_, err := nc.ocs(http.MethodGet, "/ocs/v2.php/cloud/capabilities", nil)
	return err
}

func (nc *Nextcloud) dav(method, userID, password, remotePath string, body io.Reader, headers map[string]string) (int, []byte, error) {
	parts := []string{}
	for _, part := range strings.Split(strings.Trim(remotePath, "/"), "/") {
		if part != "" {
			parts = append(parts, url.PathEscape(part))
		}
	}
	endpoint := nc.baseURL + "/remote.php/dav/files/" + url.PathEscape(userID) + "/"
	if len(parts) > 0 {
		endpoint += strings.Join(parts, "/")
	}
	req, err := http.NewRequest(method, endpoint, body)
	if err != nil {
		return 0, nil, err
	}
	req.SetBasicAuth(userID, password)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := nc.client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	return resp.StatusCode, raw, nil
}

func (nc *Nextcloud) GetQuota(userID, password string) (int64, int64, error) {
	body := strings.NewReader(`<?xml version="1.0"?><d:propfind xmlns:d="DAV:"><d:prop><d:quota-used-bytes/><d:quota-available-bytes/></d:prop></d:propfind>`)
	status, raw, err := nc.dav("PROPFIND", userID, password, "", body, map[string]string{"Depth": "0", "Content-Type": "application/xml", "Accept": "application/xml"})
	if err != nil {
		return 0, 0, err
	}
	if status != 207 && status != 200 {
		return 0, 0, fmt.Errorf("Nextcloud WebDAV quota HTTP %d: %s", status, string(raw[:min(len(raw), 300)]))
	}
	decoder := xml.NewDecoder(bytes.NewReader(raw))
	var used, available int64 = -1, -1
	for {
		tok, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return 0, 0, err
		}
		start, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		if start.Name.Local == "quota-used-bytes" || start.Name.Local == "quota-available-bytes" {
			var text string
			if err := decoder.DecodeElement(&text, &start); err != nil {
				return 0, 0, err
			}
			value, _ := strconv.ParseInt(strings.TrimSpace(text), 10, 64)
			if start.Name.Local == "quota-used-bytes" {
				used = value
			} else {
				available = value
			}
		}
	}
	return used, available, nil
}

func (nc *Nextcloud) UploadFile(userID, password, filename, localPath string) (string, error) {
	file, err := os.Open(localPath)
	if err != nil {
		return "", err
	}
	defer file.Close()
	remotePath := cleanFilename(filename)
	status, raw, err := nc.dav(http.MethodPut, userID, password, remotePath, file, nil)
	if err != nil {
		return "", err
	}
	if status != 200 && status != 201 && status != 204 {
		if status == 403 {
			return "", fmt.Errorf("Nextcloud WebDAV upload HTTP 403: облако запретило создание файла. Проверьте квоту, доступ пользователя и правила File Access Control. Ответ: %s", string(raw[:min(len(raw), 300)]))
		}
		return "", fmt.Errorf("Nextcloud WebDAV upload HTTP %d: %s", status, string(raw[:min(len(raw), 300)]))
	}
	return remotePath, nil
}

type UploadJob struct {
	TelegramID      int64
	ChatID          int64
	StatusMessageID int
	FileID          string
	Filename        string
	FileSize        int64
	Lang            string
	IsSupporter     bool
	Priority        int
	Seq             int64
}

type UploadHeap []UploadJob

func (h UploadHeap) Len() int { return len(h) }
func (h UploadHeap) Less(i, j int) bool {
	if h[i].Priority == h[j].Priority {
		return h[i].Seq < h[j].Seq
	}
	return h[i].Priority < h[j].Priority
}
func (h UploadHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *UploadHeap) Push(x any)   { *h = append(*h, x.(UploadJob)) }
func (h *UploadHeap) Pop() any {
	old := *h
	item := old[len(old)-1]
	*h = old[:len(old)-1]
	return item
}

type UploadQueue struct {
	mu     sync.Mutex
	cond   *sync.Cond
	values UploadHeap
}

func NewUploadQueue() *UploadQueue {
	q := &UploadQueue{}
	q.cond = sync.NewCond(&q.mu)
	heap.Init(&q.values)
	return q
}

func (q *UploadQueue) Put(job UploadJob) int {
	q.mu.Lock()
	defer q.mu.Unlock()
	heap.Push(&q.values, job)
	size := q.values.Len()
	q.cond.Signal()
	return size
}

func (q *UploadQueue) Get() UploadJob {
	q.mu.Lock()
	defer q.mu.Unlock()
	for q.values.Len() == 0 {
		q.cond.Wait()
	}
	return heap.Pop(&q.values).(UploadJob)
}

func main() {
	loadDotEnv(".env")
	cfg := loadConfig()
	configureLogging(cfg)
	redis, err := NewRedisClient(cfg.RedisURL)
	if err != nil {
		log.Fatalf("redis config: %v", err)
	}

	app := &App{
		cfg: cfg,
		tg: &Telegram{
			token:   cfg.BotToken,
			apiURL:  "https://api.telegram.org/bot" + cfg.BotToken + "/",
			fileURL: "https://api.telegram.org/file/bot" + cfg.BotToken + "/",
			client:  &http.Client{Timeout: 90 * time.Second},
		},
		db: &DB{
			baseURL: strings.TrimRight(cfg.DatabaseURL, "/"),
			token:   cfg.DatabaseAPIToken,
			client:  &http.Client{Timeout: 30 * time.Second},
		},
		nc: &Nextcloud{
			baseURL:  strings.TrimRight(cfg.NextcloudInternalURL, "/"),
			username: cfg.NextcloudAdminUser,
			password: cfg.NextcloudAdminPassword,
			client:   &http.Client{Timeout: 90 * time.Second},
		},
		states:  NewStateStore(redis),
		uploads: NewUploadQueue(),
	}
	if cfg.PlategaEnabled && cfg.PlategaMerchantID != "" && cfg.PlategaSecret != "" {
		app.platega = &Platega{
			merchantID: cfg.PlategaMerchantID,
			secret:     cfg.PlategaSecret,
			baseURL:    cfg.PlategaBaseURL,
			client:     &http.Client{Timeout: 30 * time.Second},
		}
	}

	if err := app.db.Init(); err != nil {
		log.Fatalf("init db: %v", err)
	}
	log.Printf("Go Telegram bot started. public_nextcloud=%s internal_nextcloud=%s db=%s", cfg.NextcloudURL, cfg.NextcloudInternalURL, cfg.DatabaseURL)

	go app.uploadWorker()
	go app.autoBackupLoop()
	go app.nextcloudSyncLoop()
	go app.premiumExpirationLoop()
	app.poll()
}

func (a *App) poll() {
	offset := 0
	for {
		updates, err := a.tg.GetUpdates(offset)
		if err != nil {
			log.Printf("getUpdates failed: %v", err)
			time.Sleep(3 * time.Second)
			continue
		}
		for _, update := range updates {
			if update.UpdateID >= offset {
				offset = update.UpdateID + 1
			}
			a.handleUpdate(update)
		}
	}
}

func (a *App) handleUpdate(update Update) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("panic while handling update %d: %v", update.UpdateID, r)
		}
	}()
	if update.PreCheckoutQuery != nil {
		a.tg.AnswerPreCheckout(update.PreCheckoutQuery.ID, strings.HasPrefix(update.PreCheckoutQuery.InvoicePayload, "stars_donate:"))
		return
	}
	if update.CallbackQuery != nil {
		a.handleCallback(update.CallbackQuery)
		return
	}
	if update.Message != nil {
		a.handleMessage(update.Message)
	}
}

func (a *App) handleMessage(msg *Message) {
	if msg.From == nil {
		return
	}
	userID := msg.From.ID
	if msg.SuccessfulPayment != nil {
		a.handleSuccessfulPayment(msg)
		return
	}
	if st, ok := a.states.Get(userID); ok {
		a.handleStateMessage(msg, st)
		return
	}
	if strings.HasPrefix(msg.Text, "/") {
		a.handleCommand(msg)
		return
	}
	if a.hasUpload(msg) {
		a.handleUpload(msg)
	}
}

func (a *App) handleCommand(msg *Message) {
	parts := strings.Fields(msg.Text)
	command := strings.Split(strings.TrimPrefix(parts[0], "/"), "@")[0]
	switch command {
	case "start":
		a.start(msg)
	case "admin":
		if a.isAdmin(msg.From.ID) {
			_, _ = a.tg.SendMessage(msg.Chat.ID, a.adminSummary(), adminKeyboard())
		}
	case "health":
		if a.isAdmin(msg.From.ID) {
			a.health(msg.Chat.ID)
		}
	case "sync":
		if a.isAdmin(msg.From.ID) {
			checked, removed, err := a.syncNextcloudUsers()
			if err != nil {
				_, _ = a.tg.SendMessage(msg.Chat.ID, "⚠️ Синхронизация не удалась: <code>"+esc(err.Error())+"</code>", nil)
				return
			}
			_, _ = a.tg.SendMessage(msg.Chat.ID, fmt.Sprintf("🔄 <b>Синхронизация завершена</b>\n\nПроверено: <b>%d</b>\nУдалено: <b>%d</b>", checked, removed), adminKeyboard())
		}
	case "search":
		if a.isAdmin(msg.From.ID) {
			query := strings.TrimSpace(strings.TrimPrefix(msg.Text, parts[0]))
			a.renderSearch(msg.Chat.ID, query)
		}
	case "broadcast":
		if a.isAdmin(msg.From.ID) {
			text := strings.TrimSpace(strings.TrimPrefix(msg.Text, parts[0]))
			a.broadcastText(msg.Chat.ID, text)
		}
	case "setsticker":
		if a.isAdmin(msg.From.ID) {
			a.setStickerStart(msg, parts)
		}
	case "stickers":
		if a.isAdmin(msg.From.ID) {
			_, _ = a.tg.SendMessage(msg.Chat.ID, a.stickersText(), adminKeyboard())
		}
	}
}

func (a *App) start(msg *Message) {
	if a.isAdmin(msg.From.ID) {
		_, _ = a.tg.SendMessage(msg.Chat.ID, a.adminSummary(), adminKeyboard())
		return
	}
	user, err := a.db.UpsertRequest(msg.From.ID, ptrOrNil(msg.From.Username), ptrOrNil(msg.From.FirstName), ptrOrNil(msg.From.LastName))
	if err != nil {
		_, _ = a.tg.SendMessage(msg.Chat.ID, "⚠️ Ошибка базы: <code>"+esc(err.Error())+"</code>", nil)
		return
	}
	if user.Status == "approved" {
		if user.NCUserID != nil {
			exists, err := a.nc.UserExists(*user.NCUserID)
			if err == nil && !exists {
				_ = a.db.DeleteUser(user.TelegramID)
				_, _ = a.tg.SendMessage(msg.Chat.ID, "Аккаунт не найден в облаке, запись бота очищена. Отправьте /start еще раз.", nil)
				return
			}
		}
		_ = a.sendEventSticker(msg.Chat.ID, "welcome")
		_, _ = a.tg.SendMessage(msg.Chat.ID, a.accountText(user), accountKeyboard(a.cfg, langOf(user)))
		return
	}
	if user.Status == "rejected" {
		_, _ = a.tg.SendMessage(msg.Chat.ID, "Ваша заявка на beta-тест сейчас отклонена.", nil)
		return
	}
	_, _ = a.tg.SendMessage(msg.Chat.ID, "<b>Заявка отправлена ✨</b>\n\nАдминистратор проверит доступ к beta-тесту. Я сообщу, когда аккаунт будет готов.", nil)
	for adminID := range a.cfg.AdminIDs {
		text := "<b>Новая заявка на beta-тест</b>\n<code>━━━━━━━━━━━━━━━━━━━━</code>\n\n" +
			"Пользователь: " + displayName(user) + "\n" +
			fmt.Sprintf("Telegram ID: <code>%d</code>", user.TelegramID)
		_, _ = a.tg.SendMessage(adminID, text, requestReviewKeyboard(user.TelegramID))
	}
	log.Printf("beta request: telegram_id=%d username=%s", msg.From.ID, msg.From.Username)
}

func (a *App) handleCallback(cb *CallbackQuery) {
	data := cb.Data
	if !a.isAdmin(cb.From.ID) &&
		!strings.HasPrefix(data, "account:") &&
		!strings.HasPrefix(data, "lang:") &&
		!strings.HasPrefix(data, "donate:") &&
		!strings.HasPrefix(data, "stars:") &&
		!strings.HasPrefix(data, "platega:") &&
		!strings.HasPrefix(data, "platega_check:") {
		a.tg.AnswerCallback(cb.ID, "Нет доступа", true)
		return
	}
	switch {
	case data == "admin":
		a.edit(cb, a.adminSummary(), adminKeyboard())
	case data == "admin:search":
		a.states.Set(cb.From.ID, State{Kind: StateAdminSearch})
		_, _ = a.tg.SendMessage(cb.Message.Chat.ID, "🔎 Отправьте Telegram ID или тег пользователя. Например: <code>8799317819</code> или <code>@username</code>.", backAdminKeyboard())
	case data == "stickers":
		a.edit(cb, a.stickersText(), adminKeyboard())
	case strings.HasPrefix(data, "users:"):
		a.usersList(cb)
	case strings.HasPrefix(data, "user:"):
		a.userDetails(cb)
	case strings.HasPrefix(data, "approve:"):
		a.approveUser(cb)
	case strings.HasPrefix(data, "reject:"):
		a.rejectUser(cb)
	case strings.HasPrefix(data, "quotaadd:"):
		a.quotaAdd(cb)
	case strings.HasPrefix(data, "quotacustom:"):
		id := parseLastInt(data)
		a.states.Set(cb.From.ID, State{Kind: StateQuotaCustom, TargetID: id})
		_, _ = a.tg.SendMessage(cb.Message.Chat.ID, "Введите, сколько GB добавить пользователю.", backAdminKeyboard())
	case strings.HasPrefix(data, "resetpass:"):
		a.resetPassword(cb)
	case strings.HasPrefix(data, "supporter:"):
		a.setSupporter(cb)
	case strings.HasPrefix(data, "disable:"):
		a.setEnabled(cb, false)
	case strings.HasPrefix(data, "enable:"):
		a.setEnabled(cb, true)
	case strings.HasPrefix(data, "deleteask:"):
		id := parseLastInt(data)
		a.edit(cb, fmt.Sprintf("<b>Удаление пользователя</b>\n\nБудет удален аккаунт облака и запись в базе бота.\nПользователь: <code>%d</code>", id), deleteConfirmKeyboard(id))
	case strings.HasPrefix(data, "deleteyes:"):
		a.deleteUser(cb)
	case data == "account:home":
		a.accountHome(cb)
	case data == "account:support":
		a.accountSupport(cb)
	case data == "account:donate":
		a.accountDonate(cb)
	case data == "account:language":
		a.edit(cb, "<b>🌐 Выберите язык</b>", languageKeyboard())
	case data == "account:change_password":
		a.states.Set(cb.From.ID, State{Kind: StateChangePassword})
		_, _ = a.tg.SendMessage(cb.Message.Chat.ID, "🔐 Отправьте новый пароль для облака.\n\nМинимум 8 символов. После смены бот обновит сохраненный пароль для загрузок.", accountBackKeyboard())
	case strings.HasPrefix(data, "lang:"):
		a.setLanguage(cb)
	case strings.HasPrefix(data, "donate:"):
		a.donateCallback(cb)
	case strings.HasPrefix(data, "stars:"):
		a.starsDonate(cb)
	case strings.HasPrefix(data, "platega:"):
		a.plategaCreate(cb)
	case strings.HasPrefix(data, "platega_check:"):
		a.plategaCheck(cb)
	case data == "sync":
		checked, removed, err := a.syncNextcloudUsers()
		if err != nil {
			a.tg.AnswerCallback(cb.ID, "Ошибка", true)
			_, _ = a.tg.SendMessage(cb.Message.Chat.ID, "⚠️ Синхронизация не удалась: <code>"+esc(err.Error())+"</code>", nil)
			return
		}
		a.edit(cb, fmt.Sprintf("🔄 <b>Синхронизация завершена</b>\n\nПроверено: <b>%d</b>\nУдалено из БД бота: <b>%d</b>", checked, removed), adminKeyboard())
	case strings.HasPrefix(data, "backup"):
		a.backupCallback(cb)
	case strings.HasPrefix(data, "restore:"):
		a.restoreBackupCallback(cb)
	case data == "broadcast":
		a.states.Set(cb.From.ID, State{Kind: StateBroadcast})
		_, _ = a.tg.SendMessage(cb.Message.Chat.ID, "📣 Отправьте любое сообщение для рассылки: текст, фото, документ, видео или другой тип сообщения.", backAdminKeyboard())
	default:
		a.tg.AnswerCallback(cb.ID, "Неизвестное действие", true)
		return
	}
	a.tg.AnswerCallback(cb.ID, "", false)
}

func (a *App) handleStateMessage(msg *Message, st State) {
	switch st.Kind {
	case StateAdminSearch:
		a.states.Clear(msg.From.ID)
		a.renderSearch(msg.Chat.ID, msg.Text)
	case StateBroadcast:
		a.states.Clear(msg.From.ID)
		a.broadcastMessage(msg)
	case StateSticker:
		a.saveSticker(msg, st)
	case StateQuotaCustom:
		amount, err := strconv.Atoi(strings.TrimSpace(msg.Text))
		if err != nil || amount <= 0 {
			_, _ = a.tg.SendMessage(msg.Chat.ID, "Введите целое число GB больше нуля.", backAdminKeyboard())
			return
		}
		a.states.Clear(msg.From.ID)
		a.addQuotaToUser(msg.Chat.ID, st.TargetID, amount)
	case StateChangePassword:
		a.applyUserPassword(msg)
	}
}

func (a *App) adminSummary() string {
	total, _ := a.db.CountUsers("")
	requested, _ := a.db.CountUsers("requested")
	approved, _ := a.db.CountUsers("approved")
	rejected, _ := a.db.CountUsers("rejected")
	return fmt.Sprintf("<b>🛠️ Админ-панель облака</b>\n<code>━━━━━━━━━━━━━━━━━━━━</code>\n\n👥 Всего пользователей: <b>%d</b>\n📝 Заявок: <b>%d</b>\n✅ Одобрено: <b>%d</b>\n❌ Отклонено: <b>%d</b>", total, requested, approved, rejected)
}

func (a *App) health(chatID int64) {
	status := "✅ Nextcloud API доступен"
	if err := a.nc.CheckConnection(); err != nil {
		status = "⚠️ Nextcloud API недоступен: <code>" + esc(err.Error()) + "</code>"
	}
	_, _ = a.tg.SendMessage(chatID, "<b>Проверка Nextcloud</b>\n\nПубличный URL: <code>"+esc(a.cfg.NextcloudURL)+"</code>\nВнутренний URL: <code>"+esc(a.cfg.NextcloudInternalURL)+"</code>\n\n"+status, nil)
}

func (a *App) renderSearch(chatID int64, query string) {
	query = strings.TrimSpace(query)
	if query == "" {
		_, _ = a.tg.SendMessage(chatID, "Введите Telegram ID или тег для поиска.", backAdminKeyboard())
		return
	}
	users, err := a.db.SearchUsers(query, 10)
	if err != nil {
		_, _ = a.tg.SendMessage(chatID, "⚠️ Поиск не удался: <code>"+esc(err.Error())+"</code>", adminKeyboard())
		return
	}
	text := fmt.Sprintf("🔎 <b>Поиск</b>: <code>%s</code>\n\n", esc(query))
	if len(users) == 0 {
		text += "Ничего не найдено."
	} else {
		text += "Выберите пользователя."
	}
	_, _ = a.tg.SendMessage(chatID, text, usersKeyboard(users, "all", 0, false))
}

func (a *App) setStickerStart(msg *Message, parts []string) {
	allowed := map[string]bool{
		"welcome": true, "approved": true, "upload_ok": true, "error": true,
		"support": true, "donate": true, "language": true, "password": true,
	}
	if len(parts) != 2 || !allowed[parts[1]] {
		_, _ = a.tg.SendMessage(msg.Chat.ID, "Используйте: <code>/setsticker welcome|approved|upload_ok|error|support|donate|language|password</code>", adminKeyboard())
		return
	}
	a.states.Set(msg.From.ID, State{Kind: StateSticker, Event: parts[1]})
	_, _ = a.tg.SendMessage(msg.Chat.ID, "Отправьте стикер для события <code>"+esc(parts[1])+"</code>.", backAdminKeyboard())
}

func (a *App) saveSticker(msg *Message, st State) {
	if msg.Sticker == nil || msg.Sticker.FileID == "" {
		_, _ = a.tg.SendMessage(msg.Chat.ID, "Нужно отправить именно стикер.", backAdminKeyboard())
		return
	}
	if err := a.db.SetSetting("sticker_"+st.Event, msg.Sticker.FileID); err != nil {
		_, _ = a.tg.SendMessage(msg.Chat.ID, "Не удалось сохранить стикер: <code>"+esc(err.Error())+"</code>", adminKeyboard())
		return
	}
	a.states.Clear(msg.From.ID)
	_, _ = a.tg.SendMessage(msg.Chat.ID, "✨ Стикер для <code>"+esc(st.Event)+"</code> сохранен. Если Telegram его отклонит, бот оставит базовый маркер "+eventMark(st.Event)+".", adminKeyboard())
}

func (a *App) stickersText() string {
	settings, _ := a.db.ListSettings("sticker_")
	line := func(event string, envValue string) string {
		mode := "базовый"
		if settings["sticker_"+event] != "" || envValue != "" {
			mode = "кастомный"
		}
		return event + ": <b>" + mode + "</b> " + eventMark(event)
	}
	return "<b>✨ Стикеры</b>\n\n" +
		"Если кастомный стикер не задан или Telegram его отклонит, бот оставит базовый маркер в тексте.\n\n" +
		line("welcome", a.cfg.StickerWelcome) + "\n" +
		line("approved", a.cfg.StickerApproved) + "\n" +
		line("upload_ok", a.cfg.StickerUploadOK) + "\n" +
		line("error", a.cfg.StickerError) + "\n" +
		line("support", "") + "\n" +
		line("donate", "") + "\n" +
		line("language", "") + "\n" +
		line("password", "") + "\n\n" +
		"Команды настройки:\n" +
		"<code>/setsticker welcome</code>\n" +
		"<code>/setsticker approved</code>\n" +
		"<code>/setsticker upload_ok</code>\n" +
		"<code>/setsticker error</code>\n" +
		"<code>/setsticker support</code>\n" +
		"<code>/setsticker donate</code>\n" +
		"<code>/setsticker language</code>\n" +
		"<code>/setsticker password</code>"
}

func (a *App) usersList(cb *CallbackQuery) {
	parts := strings.Split(cb.Data, ":")
	if len(parts) != 3 {
		return
	}
	status := parts[1]
	page, _ := strconv.Atoi(parts[2])
	queryStatus := status
	if status == "all" {
		queryStatus = ""
	}
	users, err := a.db.ListUsers(queryStatus, pageSize+1, page*pageSize)
	if err != nil {
		a.tg.AnswerCallback(cb.ID, "Ошибка базы", true)
		return
	}
	hasNext := len(users) > pageSize
	if hasNext {
		users = users[:pageSize]
	}
	title := "Все пользователи"
	if status != "all" {
		title = "Пользователи: " + status
	}
	text := "<b>" + esc(title) + "</b>\n\n"
	if len(users) == 0 {
		text += "Пока пусто."
	} else {
		text += "Выберите пользователя."
	}
	a.edit(cb, text, usersKeyboard(users, status, page, hasNext))
}

func (a *App) userDetails(cb *CallbackQuery) {
	parts := strings.Split(cb.Data, ":")
	if len(parts) < 4 {
		return
	}
	id, _ := strconv.ParseInt(parts[1], 10, 64)
	backStatus := parts[2]
	backPage, _ := strconv.Atoi(parts[3])
	text, markup, err := a.userDetailsView(id, backStatus, backPage)
	if err != nil {
		a.tg.AnswerCallback(cb.ID, err.Error(), true)
		return
	}
	a.edit(cb, text, markup)
}

func (a *App) userDetailsView(id int64, backStatus string, backPage int) (string, *InlineKeyboardMarkup, error) {
	user, err := a.db.GetUser(id)
	if err != nil || user == nil {
		return "", nil, errors.New("Пользователь не найден")
	}
	storage := "☁️ Занято: <b>нет данных</b>"
	if user.Status == "approved" && user.NCUserID != nil && user.NCPassword != nil {
		storage = a.storageText(user)
	}
	premium := "нет"
	if isPremium(user) {
		premium = "⭐ до " + premiumUntilText(user)
	}
	text := "<b>👤 Пользователь</b>\n\n" +
		"Имя: " + displayName(user) + "\n" +
		fmt.Sprintf("Telegram ID: <code>%d</code>\n", user.TelegramID) +
		"Cloud ID: <code>" + esc(strPtr(user.NCUserID, "-")) + "</code>\n" +
		"Статус: <b>" + esc(user.Status) + "</b>\n" +
		"Премиум: <b>" + esc(premium) + "</b>\n" +
		fmt.Sprintf("Квота: <b>%d GB</b>\n", user.QuotaGB) +
		storage + "\n" +
		"Доступ: <b>" + mapBool(user.IsDisabled == 1, "отключен", "активен") + "</b>"
	return text, userKeyboard(user, backStatus, backPage), nil
}

func (a *App) approveUser(cb *CallbackQuery) {
	id := parseLastInt(cb.Data)
	user, err := a.db.GetUser(id)
	if err != nil || user == nil {
		a.tg.AnswerCallback(cb.ID, "Пользователь не найден", true)
		return
	}
	ncUserID := strconv.FormatInt(id, 10)
	password := generatePassword(18)
	if err := a.nc.EnsureUser(ncUserID, password, a.cfg.DefaultQuotaGB); err != nil {
		a.tg.AnswerCallback(cb.ID, "Ошибка Nextcloud", true)
		_, _ = a.tg.SendMessage(cb.Message.Chat.ID, "Не удалось выдать доступ: <code>"+esc(err.Error())+"</code>", nil)
		return
	}
	if err := a.db.ApproveUser(id, ncUserID, password, a.cfg.DefaultQuotaGB); err != nil {
		a.tg.AnswerCallback(cb.ID, "Ошибка базы", true)
		return
	}
	approved, _ := a.db.GetUser(id)
	_ = a.sendEventSticker(id, "approved")
	_, _ = a.tg.SendMessage(id,
		"✅ <b>Ваша заявка одобрена</b>\n<code>━━━━━━━━━━━━━━━━━━━━</code>\n\n"+
			"🆔 Логин: <code>"+esc(ncUserID)+"</code>\n"+
			"🔐 Пароль: <code>"+esc(password)+"</code>\n"+
			fmt.Sprintf("💾 Квота: <b>%d GB</b>\n\n", a.cfg.DefaultQuotaGB)+
			"📤 Файлы можно отправлять прямо сюда: бот загрузит их в облако.\nПароль всегда виден в /start, там же его можно сменить.",
		accountKeyboard(a.cfg, langOf(approved)),
	)
	a.edit(cb, fmt.Sprintf("✅ Доступ выдан пользователю <code>%d</code>: %d GB.", id, a.cfg.DefaultQuotaGB), adminKeyboard())
	log.Printf("user approved: telegram_id=%d nc_user_id=%s", id, ncUserID)
}

func (a *App) rejectUser(cb *CallbackQuery) {
	id := parseLastInt(cb.Data)
	user, _ := a.db.GetUser(id)
	_ = a.db.RejectUser(id)
	_, _ = a.tg.SendMessage(id, "Ваша заявка на beta-тест сейчас отклонена.", nil)
	a.edit(cb, fmt.Sprintf("❌ Заявка пользователя <code>%d</code> отклонена.", id), adminKeyboard())
	log.Printf("user rejected: telegram_id=%d user=%v", id, user != nil)
}

func (a *App) quotaAdd(cb *CallbackQuery) {
	parts := strings.Split(cb.Data, ":")
	if len(parts) != 3 {
		return
	}
	id, _ := strconv.ParseInt(parts[1], 10, 64)
	amount, _ := strconv.Atoi(parts[2])
	a.addQuotaToUser(cb.Message.Chat.ID, id, amount)
	text, markup, err := a.userDetailsView(id, "all", 0)
	if err == nil {
		a.edit(cb, text, markup)
	}
}

func (a *App) addQuotaToUser(chatID int64, id int64, amount int) {
	user, err := a.db.GetUser(id)
	if err != nil || user == nil || user.NCUserID == nil {
		_, _ = a.tg.SendMessage(chatID, "Пользователь не найден или еще не одобрен.", adminKeyboard())
		return
	}
	newQuota := user.QuotaGB + amount
	if err := a.nc.SetQuota(*user.NCUserID, newQuota); err != nil {
		_, _ = a.tg.SendMessage(chatID, "Не удалось изменить квоту: <code>"+esc(err.Error())+"</code>", adminKeyboard())
		return
	}
	_ = a.db.SetQuota(id, newQuota)
	_, _ = a.tg.SendMessage(chatID, fmt.Sprintf("💾 Квота пользователя <code>%d</code> теперь <b>%d GB</b>.", id, newQuota), adminKeyboard())
}

func (a *App) resetPassword(cb *CallbackQuery) {
	id := parseLastInt(cb.Data)
	user, err := a.db.GetUser(id)
	if err != nil || user == nil || user.NCUserID == nil {
		a.tg.AnswerCallback(cb.ID, "Nextcloud-пользователь еще не создан", true)
		return
	}
	password := generatePassword(18)
	if err := a.nc.SetUserValue(*user.NCUserID, "password", password); err != nil {
		a.tg.AnswerCallback(cb.ID, "Ошибка Nextcloud", true)
		_, _ = a.tg.SendMessage(cb.Message.Chat.ID, "Не удалось сбросить пароль: <code>"+esc(err.Error())+"</code>", nil)
		return
	}
	_ = a.db.SetNextcloudPassword(id, password)
	_, _ = a.tg.SendMessage(id, "🔐 Администратор сбросил пароль для вашего облака.\n\nЛогин: <code>"+esc(*user.NCUserID)+"</code>\nНовый пароль: <code>"+esc(password)+"</code>", accountKeyboard(a.cfg, langOf(user)))
	_, _ = a.tg.SendMessage(cb.Message.Chat.ID, fmt.Sprintf("🔐 Пароль пользователя <code>%d</code> сброшен.", id), adminKeyboard())
}

func (a *App) setSupporter(cb *CallbackQuery) {
	parts := strings.Split(cb.Data, ":")
	if len(parts) != 3 {
		return
	}
	id, _ := strconv.ParseInt(parts[1], 10, 64)
	enabled := parts[2] == "1"
	var until *string
	if enabled {
		value := time.Now().UTC().Add(time.Duration(a.cfg.PremiumDays) * 24 * time.Hour).Format(time.RFC3339)
		until = &value
	}
	_ = a.db.SetSupporter(id, enabled, until)
	text, markup, err := a.userDetailsView(id, "all", 0)
	if err == nil {
		a.edit(cb, text, markup)
	}
}

func (a *App) setEnabled(cb *CallbackQuery, enabled bool) {
	id := parseLastInt(cb.Data)
	user, err := a.db.GetUser(id)
	if err != nil || user == nil || user.NCUserID == nil {
		a.tg.AnswerCallback(cb.ID, "Nextcloud-пользователь еще не создан", true)
		return
	}
	if enabled {
		err = a.nc.EnableUser(*user.NCUserID)
	} else {
		err = a.nc.DisableUser(*user.NCUserID)
	}
	if err != nil {
		a.tg.AnswerCallback(cb.ID, "Ошибка Nextcloud", true)
		_, _ = a.tg.SendMessage(cb.Message.Chat.ID, "Не удалось изменить доступ: <code>"+esc(err.Error())+"</code>", nil)
		return
	}
	_ = a.db.SetDisabled(id, !enabled)
	text, markup, err := a.userDetailsView(id, "all", 0)
	if err == nil {
		a.edit(cb, text, markup)
	}
}

func (a *App) deleteUser(cb *CallbackQuery) {
	id := parseLastInt(cb.Data)
	user, err := a.db.GetUser(id)
	if err != nil || user == nil {
		a.tg.AnswerCallback(cb.ID, "Пользователь уже удален", true)
		return
	}
	if user.NCUserID != nil {
		if err := a.nc.DeleteUser(*user.NCUserID); err != nil {
			a.tg.AnswerCallback(cb.ID, "Ошибка Nextcloud", true)
			_, _ = a.tg.SendMessage(cb.Message.Chat.ID, "Не удалось удалить аккаунт облака: <code>"+esc(err.Error())+"</code>", nil)
			return
		}
	}
	_ = a.db.DeleteUser(id)
	_, _ = a.tg.SendMessage(id, "Ваш beta-доступ к облаку был удален администратором.", nil)
	a.edit(cb, fmt.Sprintf("🗑️ Пользователь <code>%d</code> удален.", id), adminKeyboard())
}

func (a *App) accountHome(cb *CallbackQuery) {
	user, err := a.db.GetUser(cb.From.ID)
	if err != nil || user == nil || user.Status != "approved" || user.IsDisabled == 1 {
		a.tg.AnswerCallback(cb.ID, "Доступ не активен", true)
		return
	}
	a.edit(cb, a.accountText(user), accountKeyboard(a.cfg, langOf(user)))
}

func (a *App) accountSupport(cb *CallbackQuery) {
	if !a.cfg.EnableSupportBlock {
		a.tg.AnswerCallback(cb.ID, "Раздел поддержки отключен", true)
		return
	}
	lines := []string{"<b>💬 Поддержка</b>", ""}
	if a.cfg.SupportTelegram != "" {
		tg := strings.TrimSpace(a.cfg.SupportTelegram)
		if strings.HasPrefix(tg, "http://") || strings.HasPrefix(tg, "https://") {
			lines = append(lines, `Telegram: <a href="`+esc(tg)+`">`+esc(tg)+`</a>`)
		} else {
			username := strings.TrimPrefix(tg, "@")
			lines = append(lines, `Telegram: <a href="https://t.me/`+esc(username)+`">@`+esc(username)+`</a>`)
		}
	}
	if a.cfg.SupportEmail != "" {
		lines = append(lines, `Email: <a href="mailto:`+esc(a.cfg.SupportEmail)+`">`+esc(a.cfg.SupportEmail)+`</a>`)
	}
	if len(lines) == 2 {
		lines = append(lines, "Контакты поддержки пока не настроены.")
	}
	_ = a.sendEventSticker(cb.Message.Chat.ID, "support")
	a.edit(cb, strings.Join(lines, "\n"), accountBackKeyboard())
}

func (a *App) accountDonate(cb *CallbackQuery) {
	if !a.cfg.EnableDonateBlock {
		a.tg.AnswerCallback(cb.ID, "Раздел доната отключен", true)
		return
	}
	text := "<b>💙 Поддержать проект</b>\n\nМожно поддержать проект через Telegram Stars или внешнюю ссылку.\nВыберите способ поддержки."
	_ = a.sendEventSticker(cb.Message.Chat.ID, "donate")
	a.edit(cb, text, donateKeyboard(a.cfg))
}

func (a *App) donateCallback(cb *CallbackQuery) {
	if cb.Data == "donate:stars" {
		a.edit(cb, "⭐ <b>Telegram Stars</b>\n\nВыберите сумму в звездах.", starsKeyboard(a.cfg))
		return
	}
	if cb.Data == "donate:platega" {
		if a.platega == nil && a.cfg.PlategaURL == "" {
			a.tg.AnswerCallback(cb.ID, "Platega не настроена", true)
			return
		}
		a.edit(cb, "💳 <b>Platega</b>\n\nВыберите сумму в рублях. Бот создаст платежную ссылку Platega.", plategaKeyboard(a.cfg, a.platega != nil))
		return
	}
	a.tg.AnswerCallback(cb.ID, "Недоступно", true)
}

func (a *App) starsDonate(cb *CallbackQuery) {
	if !a.cfg.EnableDonateBlock || !a.cfg.TelegramStarsEnabled {
		a.tg.AnswerCallback(cb.ID, "Донат отключен", true)
		return
	}
	amount := parseLastInt(cb.Data)
	if !containsInt(a.cfg.TelegramStarsAmounts, int(amount)) {
		a.tg.AnswerCallback(cb.ID, "Недоступная сумма", true)
		return
	}
	payload := fmt.Sprintf("stars_donate:%d:%d", cb.From.ID, amount)
	if err := a.tg.SendInvoice(cb.Message.Chat.ID, "Поддержка проекта", "Спасибо за поддержку проекта!", payload, int(amount)); err != nil {
		a.tg.AnswerCallback(cb.ID, "Не удалось создать счет", true)
		log.Printf("send invoice failed: %v", err)
	}
}

func (a *App) plategaCreate(cb *CallbackQuery) {
	if !a.cfg.EnableDonateBlock || !a.cfg.PlategaEnabled || a.platega == nil {
		a.tg.AnswerCallback(cb.ID, "Platega отключена", true)
		return
	}
	amount := int(parseLastInt(cb.Data))
	if !containsInt(a.cfg.PlategaAmountsRUB, amount) {
		a.tg.AnswerCallback(cb.ID, "Недоступная сумма", true)
		return
	}
	payload := fmt.Sprintf("platega_donate:%d:%d", cb.From.ID, amount)
	payment, err := a.platega.CreatePayment(
		amount,
		fmt.Sprintf("Cloud bot support from Telegram %d", cb.From.ID),
		payload,
		a.cfg.PlategaReturnURL,
		a.cfg.PlategaFailedURL,
	)
	if err != nil {
		a.tg.AnswerCallback(cb.ID, "Не удалось создать платеж", true)
		_, _ = a.tg.SendMessage(cb.Message.Chat.ID, "Не удалось создать платеж Platega: <code>"+esc(err.Error())+"</code>", nil)
		return
	}
	transactionID := fmt.Sprint(payment["transactionId"])
	paymentURL := fmt.Sprint(payment["url"])
	status := strings.ToUpper(fmt.Sprint(payment["status"]))
	if status == "" || status == "<nil>" {
		status = "PENDING"
	}
	_ = a.db.CreatePayment(transactionID, cb.From.ID, "platega", amount, "RUB", status, &paymentURL, &payload)
	a.edit(
		cb,
		fmt.Sprintf("💳 <b>Platega</b>\n\nПлатеж создан. После оплаты нажмите «Проверить оплату».\nID: <code>%s</code>\nСумма: <b>%d RUB</b>", esc(transactionID), amount),
		plategaPaymentKeyboard(paymentURL, transactionID),
	)
}

func (a *App) plategaCheck(cb *CallbackQuery) {
	if a.platega == nil {
		a.tg.AnswerCallback(cb.ID, "Platega не настроена", true)
		return
	}
	transactionID := strings.TrimPrefix(cb.Data, "platega_check:")
	storedPayment, err := a.db.GetPayment(transactionID)
	if err != nil || storedPayment == nil {
		a.tg.AnswerCallback(cb.ID, "Платеж не найден", true)
		return
	}
	if storedPayment.TelegramID != cb.From.ID && !a.isAdmin(cb.From.ID) {
		a.tg.AnswerCallback(cb.ID, "Нет доступа", true)
		return
	}
	payment, err := a.platega.Transaction(transactionID)
	if err != nil {
		a.tg.AnswerCallback(cb.ID, "Не удалось проверить платеж", true)
		_, _ = a.tg.SendMessage(cb.Message.Chat.ID, "Не удалось проверить платеж Platega: <code>"+esc(err.Error())+"</code>", nil)
		return
	}
	status := strings.ToUpper(fmt.Sprint(payment["status"]))
	_ = a.db.UpdatePaymentStatus(transactionID, status)
	if status == "CONFIRMED" {
		until := time.Now().UTC().Add(time.Duration(a.cfg.PremiumDays) * 24 * time.Hour).Format(time.RFC3339)
		_ = a.db.SetSupporter(storedPayment.TelegramID, true, &until)
		_, _ = a.tg.SendMessage(cb.Message.Chat.ID, "⭐ Оплата подтверждена! Премиум-иконка активирована.", accountKeyboard(a.cfg, "ru"))
		a.tg.AnswerCallback(cb.ID, "Оплачено", false)
		return
	}
	if status == "CANCELED" || status == "CHARGEBACKED" || status == "FAILED" || status == "EXPIRED" {
		a.tg.AnswerCallback(cb.ID, "Платеж не активен. Статус: "+status, true)
		return
	}
	a.tg.AnswerCallback(cb.ID, "Платеж пока не подтвержден. Статус: "+status, true)
}

func (a *App) setLanguage(cb *CallbackQuery) {
	lang := strings.TrimPrefix(cb.Data, "lang:")
	if lang != "ru" && lang != "en" {
		return
	}
	_ = a.db.SetLanguage(cb.From.ID, lang)
	user, _ := a.db.GetUser(cb.From.ID)
	if user != nil {
		a.edit(cb, a.accountText(user), accountKeyboard(a.cfg, lang))
	}
}

func (a *App) applyUserPassword(msg *Message) {
	user, err := a.db.GetUser(msg.From.ID)
	if err != nil || user == nil || user.Status != "approved" || user.NCUserID == nil {
		a.states.Clear(msg.From.ID)
		_, _ = a.tg.SendMessage(msg.Chat.ID, "Доступ не активен.", nil)
		return
	}
	password := strings.TrimSpace(msg.Text)
	if len(password) < 8 || len(password) > 128 {
		_, _ = a.tg.SendMessage(msg.Chat.ID, "Пароль должен быть от 8 до 128 символов.", accountBackKeyboard())
		return
	}
	if err := a.nc.SetUserValue(*user.NCUserID, "password", password); err != nil {
		_, _ = a.tg.SendMessage(msg.Chat.ID, "⚠️ Не удалось сменить пароль: <code>"+esc(err.Error())+"</code>", accountBackKeyboard())
		return
	}
	_ = a.db.SetNextcloudPassword(user.TelegramID, password)
	a.states.Clear(msg.From.ID)
	_ = a.sendEventSticker(msg.Chat.ID, "password")
	_, _ = a.tg.SendMessage(msg.Chat.ID, "✅ Пароль сменен.\n\nЛогин: <code>"+esc(*user.NCUserID)+"</code>\nПароль: <code>"+esc(password)+"</code>", accountKeyboard(a.cfg, langOf(user)))
}

func (a *App) handleSuccessfulPayment(msg *Message) {
	payload := msg.SuccessfulPayment.InvoicePayload
	if !strings.HasPrefix(payload, "stars_donate:") {
		return
	}
	until := time.Now().UTC().Add(time.Duration(a.cfg.PremiumDays) * 24 * time.Hour).Format(time.RFC3339)
	_ = a.db.SetSupporter(msg.From.ID, true, &until)
	_ = a.db.CreatePayment(msg.SuccessfulPayment.TelegramPaymentChargeID, msg.From.ID, "telegram_stars", msg.SuccessfulPayment.TotalAmount, "XTR", "CONFIRMED", nil, &payload)
	_, _ = a.tg.SendMessage(msg.Chat.ID, "⭐ Спасибо за поддержку! Премиум-иконка активирована.", accountKeyboard(a.cfg, "ru"))
}

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
		adminKeyboard(),
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

func adminKeyboard() *InlineKeyboardMarkup {
	return keyboard([][]InlineKeyboardButton{
		{{Text: "👥 Пользователи", CallbackData: "users:all:0"}, {Text: "🔎 Поиск", CallbackData: "admin:search"}},
		{{Text: "📝 Заявки", CallbackData: "users:requested:0"}, {Text: "🗄️ Бекапы", CallbackData: "backup"}},
		{{Text: "📣 Рассылка", CallbackData: "broadcast"}, {Text: "🔄 Синхронизация", CallbackData: "sync"}},
		{{Text: "✨ Стикеры", CallbackData: "stickers"}},
	})
}

func backAdminKeyboard() *InlineKeyboardMarkup {
	return keyboard([][]InlineKeyboardButton{{{Text: "🛠️ В админку", CallbackData: "admin"}}})
}

func requestReviewKeyboard(id int64) *InlineKeyboardMarkup {
	return keyboard([][]InlineKeyboardButton{{{Text: "✅ Одобрить", CallbackData: fmt.Sprintf("approve:%d", id)}, {Text: "❌ Отклонить", CallbackData: fmt.Sprintf("reject:%d", id)}}})
}

func accountKeyboard(cfg Config, lang string) *InlineKeyboardMarkup {
	changePassword := "🔐 Сменить пароль"
	support := "💬 Поддержка"
	donate := "💙 Донат"
	language := "🌐 Язык"
	cloud := "☁️ Войти в облако"
	if lang == "en" {
		changePassword = "🔐 Change password"
		support = "💬 Support"
		donate = "💙 Donate"
		language = "🌐 Language"
		cloud = "☁️ Open cloud"
	}
	rows := [][]InlineKeyboardButton{{{Text: cloud, URL: cfg.NextcloudURL}}, {{Text: changePassword, CallbackData: "account:change_password"}}}
	if cfg.EnableSupportBlock {
		rows = append(rows, []InlineKeyboardButton{{Text: support, CallbackData: "account:support"}})
	}
	if cfg.EnableDonateBlock {
		rows = append(rows, []InlineKeyboardButton{{Text: donate, CallbackData: "account:donate"}})
	}
	rows = append(rows, []InlineKeyboardButton{{Text: language, CallbackData: "account:language"}})
	return keyboard(rows)
}

func accountBackKeyboard() *InlineKeyboardMarkup {
	return keyboard([][]InlineKeyboardButton{{{Text: "⬅️ Назад", CallbackData: "account:home"}}})
}

func languageKeyboard() *InlineKeyboardMarkup {
	return keyboard([][]InlineKeyboardButton{{{Text: "🇷🇺 Русский", CallbackData: "lang:ru"}, {Text: "🇬🇧 English", CallbackData: "lang:en"}}, {{Text: "⬅️ Назад", CallbackData: "account:home"}}})
}

func donateKeyboard(cfg Config) *InlineKeyboardMarkup {
	rows := [][]InlineKeyboardButton{}
	if cfg.TelegramStarsEnabled && len(cfg.TelegramStarsAmounts) > 0 {
		rows = append(rows, []InlineKeyboardButton{{Text: "⭐ Telegram Stars", CallbackData: "donate:stars"}})
	}
	if cfg.PlategaVisible() {
		rows = append(rows, []InlineKeyboardButton{{Text: "💳 Platega", CallbackData: "donate:platega"}})
	}
	if cfg.DonateURL != "" {
		rows = append(rows, []InlineKeyboardButton{{Text: "💙 Донат", URL: cfg.DonateURL}})
	}
	rows = append(rows, []InlineKeyboardButton{{Text: "⬅️ Назад", CallbackData: "account:home"}})
	return keyboard(rows)
}

func (cfg Config) PlategaVisible() bool {
	return cfg.PlategaEnabled && (cfg.PlategaPaymentURL() != "" || (cfg.PlategaMerchantID != "" && cfg.PlategaSecret != ""))
}

func (cfg Config) PlategaPaymentURL() string {
	if cfg.PlategaEnabled {
		return cfg.PlategaURL
	}
	return ""
}

func starsKeyboard(cfg Config) *InlineKeyboardMarkup {
	rows := [][]InlineKeyboardButton{}
	row := []InlineKeyboardButton{}
	for _, amount := range cfg.TelegramStarsAmounts {
		row = append(row, InlineKeyboardButton{Text: fmt.Sprintf("⭐ %d", amount), CallbackData: fmt.Sprintf("stars:%d", amount)})
		if len(row) == 3 {
			rows = append(rows, row)
			row = []InlineKeyboardButton{}
		}
	}
	if len(row) > 0 {
		rows = append(rows, row)
	}
	rows = append(rows, []InlineKeyboardButton{{Text: "⬅️ Назад", CallbackData: "account:donate"}})
	return keyboard(rows)
}

func plategaKeyboard(cfg Config, apiEnabled bool) *InlineKeyboardMarkup {
	rows := [][]InlineKeyboardButton{}
	if apiEnabled {
		row := []InlineKeyboardButton{}
		for _, amount := range cfg.PlategaAmountsRUB {
			row = append(row, InlineKeyboardButton{Text: fmt.Sprintf("💳 %d RUB", amount), CallbackData: fmt.Sprintf("platega:%d", amount)})
			if len(row) == 2 {
				rows = append(rows, row)
				row = []InlineKeyboardButton{}
			}
		}
		if len(row) > 0 {
			rows = append(rows, row)
		}
	}
	if cfg.PlategaPaymentURL() != "" {
		rows = append(rows, []InlineKeyboardButton{{Text: "💳 Оплатить", URL: cfg.PlategaPaymentURL()}})
	}
	rows = append(rows, []InlineKeyboardButton{{Text: "⬅️ Назад", CallbackData: "account:donate"}})
	return keyboard(rows)
}

func plategaPaymentKeyboard(paymentURL, transactionID string) *InlineKeyboardMarkup {
	return keyboard([][]InlineKeyboardButton{
		{{Text: "💳 Оплатить", URL: paymentURL}},
		{{Text: "🔎 Проверить оплату", CallbackData: "platega_check:" + transactionID}},
		{{Text: "⬅️ Назад", CallbackData: "donate:platega"}},
	})
}

func usersKeyboard(users []User, status string, page int, hasNext bool) *InlineKeyboardMarkup {
	rows := [][]InlineKeyboardButton{}
	for _, user := range users {
		name := strPtr(user.Username, strPtr(user.FirstName, strconv.FormatInt(user.TelegramID, 10)))
		label := fmt.Sprintf("%s | %s | %dGB", name, user.Status, user.QuotaGB)
		if len([]rune(label)) > 60 {
			label = string([]rune(label)[:60])
		}
		rows = append(rows, []InlineKeyboardButton{{Text: label, CallbackData: fmt.Sprintf("user:%d:%s:%d", user.TelegramID, status, page)}})
	}
	nav := []InlineKeyboardButton{}
	if page > 0 {
		nav = append(nav, InlineKeyboardButton{Text: "⬅️ Назад", CallbackData: fmt.Sprintf("users:%s:%d", status, page-1)})
	}
	if hasNext {
		nav = append(nav, InlineKeyboardButton{Text: "➡️ Вперед", CallbackData: fmt.Sprintf("users:%s:%d", status, page+1)})
	}
	if len(nav) > 0 {
		rows = append(rows, nav)
	}
	rows = append(rows, []InlineKeyboardButton{{Text: "🛠️ В админку", CallbackData: "admin"}})
	return keyboard(rows)
}

func userKeyboard(user *User, backStatus string, backPage int) *InlineKeyboardMarkup {
	rows := [][]InlineKeyboardButton{}
	if user.Status == "requested" || user.Status == "rejected" {
		rows = append(rows, []InlineKeyboardButton{{Text: "✅ Одобрить", CallbackData: fmt.Sprintf("approve:%d", user.TelegramID)}})
	}
	if user.Status == "requested" {
		rows = append(rows, []InlineKeyboardButton{{Text: "❌ Отклонить", CallbackData: fmt.Sprintf("reject:%d", user.TelegramID)}})
	}
	if user.Status == "approved" {
		rows = append(rows, []InlineKeyboardButton{
			{Text: "➕ 1GB", CallbackData: fmt.Sprintf("quotaadd:%d:1", user.TelegramID)},
			{Text: "➕ 5GB", CallbackData: fmt.Sprintf("quotaadd:%d:5", user.TelegramID)},
			{Text: "➕ 10GB", CallbackData: fmt.Sprintf("quotaadd:%d:10", user.TelegramID)},
		})
		rows = append(rows, []InlineKeyboardButton{{Text: "⚙️ Другое", CallbackData: fmt.Sprintf("quotacustom:%d", user.TelegramID)}, {Text: "🔐 Сбросить пароль", CallbackData: fmt.Sprintf("resetpass:%d", user.TelegramID)}})
		if isPremium(user) {
			rows = append(rows, []InlineKeyboardButton{{Text: "⭐ Убрать премиум", CallbackData: fmt.Sprintf("supporter:%d:0", user.TelegramID)}})
		} else {
			rows = append(rows, []InlineKeyboardButton{{Text: "⭐ Сделать премиум", CallbackData: fmt.Sprintf("supporter:%d:1", user.TelegramID)}})
		}
		if user.IsDisabled == 1 {
			rows = append(rows, []InlineKeyboardButton{{Text: "🟢 Включить", CallbackData: fmt.Sprintf("enable:%d", user.TelegramID)}})
		} else {
			rows = append(rows, []InlineKeyboardButton{{Text: "🔴 Отключить", CallbackData: fmt.Sprintf("disable:%d", user.TelegramID)}})
		}
		rows = append(rows, []InlineKeyboardButton{{Text: "🗑️ Удалить", CallbackData: fmt.Sprintf("deleteask:%d", user.TelegramID)}})
	}
	rows = append(rows, []InlineKeyboardButton{{Text: "⬅️ Назад", CallbackData: fmt.Sprintf("users:%s:%d", backStatus, backPage)}})
	return keyboard(rows)
}

func deleteConfirmKeyboard(id int64) *InlineKeyboardMarkup {
	return keyboard([][]InlineKeyboardButton{{{Text: "🗑️ Да, удалить", CallbackData: fmt.Sprintf("deleteyes:%d", id)}}, {{Text: "⬅️ Отмена", CallbackData: fmt.Sprintf("user:%d:all:0", id)}}})
}

func backupKeyboard() *InlineKeyboardMarkup {
	return keyboard([][]InlineKeyboardButton{
		{{Text: "🗄️ Создать PostgreSQL", CallbackData: "backup:db"}, {Text: "📦 Создать JSON", CallbackData: "backup:json"}},
		{{Text: "📋 Список", CallbackData: "backup:list"}, {Text: "♻️ Восстановить", CallbackData: "backup:restore"}},
		{{Text: "🛠️ В админку", CallbackData: "admin"}},
	})
}

func restoreBackupKeyboard(files []string) *InlineKeyboardMarkup {
	rows := [][]InlineKeyboardButton{}
	for index, file := range files {
		label := filepath.Base(file)
		if len([]rune(label)) > 60 {
			label = string([]rune(label)[:60])
		}
		rows = append(rows, []InlineKeyboardButton{{Text: label, CallbackData: fmt.Sprintf("restore:%d", index)}})
	}
	rows = append(rows, []InlineKeyboardButton{{Text: "⬅️ Отмена", CallbackData: "backup"}})
	return keyboard(rows)
}

func keyboard(rows [][]InlineKeyboardButton) *InlineKeyboardMarkup {
	return &InlineKeyboardMarkup{InlineKeyboard: rows}
}

func loadConfig() Config {
	nextcloudURL := requiredEnv("NEXTCLOUD_URL")
	nextcloudInternal := strings.TrimRight(env("NEXTCLOUD_INTERNAL_URL", ""), "/")
	if nextcloudInternal == "" {
		nextcloudInternal = nextcloudURL
	}
	return Config{
		BotToken:                     requiredEnv("BOT_TOKEN"),
		AdminIDs:                     parseAdminIDs(requiredEnv("ADMIN_IDS")),
		NextcloudURL:                 strings.TrimRight(nextcloudURL, "/"),
		NextcloudInternalURL:         nextcloudInternal,
		NextcloudAdminUser:           requiredEnv("NEXTCLOUD_ADMIN_USER"),
		NextcloudAdminPassword:       requiredEnv("NEXTCLOUD_ADMIN_PASSWORD"),
		DefaultQuotaGB:               envInt("DEFAULT_QUOTA_GB", 10),
		DatabaseURL:                  strings.TrimRight(env("DATABASE_URL", "http://bot-db:8080"), "/"),
		DatabaseAPIToken:             env("DATABASE_API_TOKEN", ""),
		RedisURL:                     env("REDIS_URL", "redis://redis:6379/0"),
		BackupDir:                    env("BACKUP_DIR", "backups"),
		LogDir:                       env("LOG_DIR", "logs"),
		EnableSupportBlock:           envBool("ENABLE_SUPPORT_BLOCK", true),
		SupportTelegram:              env("SUPPORT_TELEGRAM", ""),
		SupportEmail:                 env("SUPPORT_EMAIL", ""),
		EnableDonateBlock:            envBool("ENABLE_DONATE_BLOCK", true),
		DonateURL:                    env("DONATE_URL", ""),
		TelegramStarsEnabled:         envBool("TELEGRAM_STARS_ENABLED", true),
		TelegramStarsAmounts:         envIntList("TELEGRAM_STARS_AMOUNTS", []int{50, 100, 250}),
		PlategaEnabled:               envBool("PLATEGA_ENABLED", true),
		PlategaURL:                   env("PLATEGA_URL", ""),
		PlategaMerchantID:            env("PLATEGA_MERCHANT_ID", ""),
		PlategaSecret:                env("PLATEGA_SECRET", ""),
		PlategaBaseURL:               strings.TrimRight(env("PLATEGA_BASE_URL", "https://app.platega.io"), "/"),
		PlategaAmountsRUB:            envIntList("PLATEGA_AMOUNTS_RUB", []int{100, 300, 500}),
		PlategaReturnURL:             env("PLATEGA_RETURN_URL", ""),
		PlategaFailedURL:             env("PLATEGA_FAILED_URL", ""),
		TelegramMaxDownloadMB:        envInt("TELEGRAM_MAX_DOWNLOAD_MB", 20),
		PremiumDays:                  envInt("PREMIUM_DAYS", 30),
		BackupRetentionDays:          envInt("BACKUP_RETENTION_DAYS", 7),
		AutoBackupIntervalHours:      envInt("AUTO_BACKUP_INTERVAL_HOURS", 24),
		NextcloudSyncIntervalMinutes: envInt("NEXTCLOUD_SYNC_INTERVAL_MINUTES", 60),
		StickerWelcome:               env("STICKER_WELCOME", ""),
		StickerApproved:              env("STICKER_APPROVED", ""),
		StickerUploadOK:              env("STICKER_UPLOAD_OK", ""),
		StickerError:                 env("STICKER_ERROR", ""),
	}
}

func configureLogging(cfg Config) {
	_ = os.MkdirAll(cfg.LogDir, 0o755)
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	file, err := os.OpenFile(filepath.Join(cfg.LogDir, "bot-go.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err == nil {
		log.SetOutput(io.MultiWriter(os.Stdout, file))
	}
}

func loadDotEnv(path string) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || !strings.Contains(line, "=") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		key := strings.TrimSpace(parts[0])
		value := strings.Trim(strings.TrimSpace(parts[1]), `"'`)
		if os.Getenv(key) == "" {
			_ = os.Setenv(key, value)
		}
	}
}

func requiredEnv(name string) string {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		log.Fatalf("missing required environment variable: %s", name)
	}
	return value
}

func env(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func envInt(name string, fallback int) int {
	value, err := strconv.Atoi(strings.TrimSpace(os.Getenv(name)))
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

func envBool(name string, fallback bool) bool {
	raw := strings.ToLower(strings.TrimSpace(os.Getenv(name)))
	if raw == "" {
		return fallback
	}
	return raw == "1" || raw == "true" || raw == "yes" || raw == "y" || raw == "on" || raw == "да"
}

func envIntList(name string, fallback []int) []int {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	var values []int
	for _, part := range strings.Split(raw, ",") {
		value, err := strconv.Atoi(strings.TrimSpace(part))
		if err == nil && value > 0 {
			values = append(values, value)
		}
	}
	if len(values) == 0 {
		return fallback
	}
	return values
}

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

func createDatabaseBackup(cfg Config, db *DB) (string, error) {
	users, err := db.ListUsers("", 100000, 0)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(cfg.BackupDir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(cfg.BackupDir, "bot-"+time.Now().UTC().Format("20060102-150405")+".postgres.json.gz")
	out, err := os.Create(path)
	if err != nil {
		return "", err
	}
	defer out.Close()
	gz := gzip.NewWriter(out)
	payload := map[string]any{"generated_at": time.Now().UTC().Format(time.RFC3339), "storage": "postgres", "users": users}
	encoder := json.NewEncoder(gz)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(payload); err != nil {
		_ = gz.Close()
		return "", err
	}
	if err := gz.Close(); err != nil {
		return "", err
	}
	return path, nil
}

func createPublicJSONBackup(cfg Config, db *DB) (string, error) {
	users, err := db.ListUsers("", 100000, 0)
	if err != nil {
		return "", err
	}
	for i := range users {
		users[i].NCPassword = nil
	}
	if err := os.MkdirAll(cfg.BackupDir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(cfg.BackupDir, "users-public-"+time.Now().UTC().Format("20060102-150405")+".json.gz")
	out, err := os.Create(path)
	if err != nil {
		return "", err
	}
	defer out.Close()
	gz := gzip.NewWriter(out)
	payload := map[string]any{"generated_at": time.Now().UTC().Format(time.RFC3339), "users": users}
	encoder := json.NewEncoder(gz)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(payload); err != nil {
		_ = gz.Close()
		return "", err
	}
	if err := gz.Close(); err != nil {
		return "", err
	}
	return path, nil
}

func restoreDatabaseBackup(backupPath string, db *DB) error {
	if _, err := os.Stat(backupPath); err != nil {
		return err
	}
	in, err := os.Open(backupPath)
	if err != nil {
		return err
	}
	defer in.Close()
	gz, err := gzip.NewReader(in)
	if err != nil {
		return err
	}
	defer gz.Close()
	var payload struct {
		Users []User `json:"users"`
	}
	if err := json.NewDecoder(gz).Decode(&payload); err != nil {
		return err
	}
	return db.RestoreUsers(payload.Users)
}

func listBackups(cfg Config) []string {
	files, _ := filepath.Glob(filepath.Join(cfg.BackupDir, "*.postgres.json.gz"))
	sortByModTime(files)
	if len(files) > 10 {
		files = files[:10]
	}
	return files
}

func pruneBackups(cfg Config) error {
	files, _ := filepath.Glob(filepath.Join(cfg.BackupDir, "*.postgres.json.gz"))
	cutoff := time.Now().Add(-time.Duration(cfg.BackupRetentionDays) * 24 * time.Hour)
	for _, file := range files {
		info, err := os.Stat(file)
		if err == nil && info.ModTime().Before(cutoff) {
			_ = os.Remove(file)
		}
	}
	return nil
}

func sortByModTime(files []string) {
	for i := 0; i < len(files); i++ {
		for j := i + 1; j < len(files); j++ {
			ii, _ := os.Stat(files[i])
			jj, _ := os.Stat(files[j])
			if ii != nil && jj != nil && jj.ModTime().After(ii.ModTime()) {
				files[i], files[j] = files[j], files[i]
			}
		}
	}
}
