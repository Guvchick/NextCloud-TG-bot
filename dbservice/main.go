package main

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type Server struct {
	db       *sql.DB
	token    string
	aead     cipher.AEAD
	premium  int
}

type RPCRequest struct {
	Method string         `json:"method"`
	Params map[string]any `json:"params"`
}

type RPCResponse struct {
	OK    bool `json:"ok"`
	Data  any  `json:"data,omitempty"`
	Error string `json:"error,omitempty"`
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
	TransactionID string  `json:"transaction_id"`
	TelegramID    int64   `json:"telegram_id"`
	Provider      string  `json:"provider"`
	Amount        int     `json:"amount"`
	Currency      string  `json:"currency"`
	Status        string  `json:"status"`
	PaymentURL    *string `json:"payment_url"`
	Payload       *string `json:"payload"`
	CreatedAt     string  `json:"created_at"`
	UpdatedAt     string  `json:"updated_at"`
}

func main() {
	dbPath := env("DATABASE_PATH", "/app/data/bot.sqlite3")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o700); err != nil {
		log.Fatalf("mkdir data: %v", err)
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		log.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	srv := &Server{
		db:      db,
		token:   os.Getenv("DATABASE_API_TOKEN"),
		premium: envInt("PREMIUM_DAYS", 30),
	}
	if secret := os.Getenv("DATABASE_SECRET_KEY"); secret != "" {
		key := sha256.Sum256([]byte(secret))
		block, err := aes.NewCipher(key[:])
		if err != nil {
			log.Fatalf("aes: %v", err)
		}
		aead, err := cipher.NewGCM(block)
		if err != nil {
			log.Fatalf("gcm: %v", err)
		}
		srv.aead = aead
	}
	if err := srv.initSchema(context.Background()); err != nil {
		log.Fatalf("init schema: %v", err)
	}
	if err := chmodDB(dbPath); err != nil {
		log.Printf("chmod db warning: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, RPCResponse{OK: true, Data: "ok"})
	})
	mux.HandleFunc("/rpc", srv.handleRPC)

	addr := env("DB_LISTEN_ADDR", ":8080")
	log.Printf("Go DB service listening on %s, db=%s", addr, dbPath)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
	}
}

func (s *Server) handleRPC(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, 405, RPCResponse{OK: false, Error: "method not allowed"})
		return
	}
	if s.token != "" && r.Header.Get("Authorization") != "Bearer "+s.token {
		writeJSON(w, 401, RPCResponse{OK: false, Error: "unauthorized"})
		return
	}
	defer r.Body.Close()
	var req RPCRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		writeJSON(w, 400, RPCResponse{OK: false, Error: err.Error()})
		return
	}
	data, err := s.dispatch(r.Context(), req.Method, req.Params)
	if err != nil {
		writeJSON(w, 400, RPCResponse{OK: false, Error: err.Error()})
		return
	}
	writeJSON(w, 200, RPCResponse{OK: true, Data: data})
}

func (s *Server) dispatch(ctx context.Context, method string, p map[string]any) (any, error) {
	switch method {
	case "init":
		return nil, s.initSchema(ctx)
	case "upsert_request":
		return s.upsertRequest(ctx, int64Param(p, "telegram_id"), strPtrParam(p, "username"), strPtrParam(p, "first_name"), strPtrParam(p, "last_name"))
	case "set_language":
		return nil, s.exec(ctx, "UPDATE users SET language = ?, updated_at = ? WHERE telegram_id = ?", strParam(p, "language"), now(), int64Param(p, "telegram_id"))
	case "get_user":
		return s.getUser(ctx, int64Param(p, "telegram_id"))
	case "approve_user":
		return nil, s.approveUser(ctx, int64Param(p, "telegram_id"), strParam(p, "nc_user_id"), strParam(p, "nc_password"), intParam(p, "quota_gb"))
	case "set_nextcloud_password":
		return nil, s.exec(ctx, "UPDATE users SET nc_password = ?, updated_at = ? WHERE telegram_id = ?", s.encrypt(strParam(p, "nc_password")), now(), int64Param(p, "telegram_id"))
	case "reject_user":
		return nil, s.exec(ctx, "UPDATE users SET status = 'rejected', updated_at = ? WHERE telegram_id = ?", now(), int64Param(p, "telegram_id"))
	case "set_quota":
		return nil, s.exec(ctx, "UPDATE users SET quota_gb = ?, updated_at = ? WHERE telegram_id = ?", intParam(p, "quota_gb"), now(), int64Param(p, "telegram_id"))
	case "set_disabled":
		return nil, s.exec(ctx, "UPDATE users SET is_disabled = ?, updated_at = ? WHERE telegram_id = ?", boolInt(p, "is_disabled"), now(), int64Param(p, "telegram_id"))
	case "set_supporter":
		until := strPtrParam(p, "supporter_until")
		if !boolParam(p, "is_supporter") {
			until = nil
		}
		return nil, s.exec(ctx, "UPDATE users SET is_supporter = ?, supporter_until = ?, updated_at = ? WHERE telegram_id = ?", boolInt(p, "is_supporter"), until, now(), int64Param(p, "telegram_id"))
	case "expire_supporters":
		res, err := s.db.ExecContext(ctx, `UPDATE users SET is_supporter = 0, supporter_until = NULL, updated_at = ? WHERE is_supporter = 1 AND supporter_until IS NOT NULL AND supporter_until <= ?`, now(), now())
		if err != nil {
			return nil, err
		}
		n, _ := res.RowsAffected()
		return n, nil
	case "delete_user":
		return nil, s.exec(ctx, "DELETE FROM users WHERE telegram_id = ?", int64Param(p, "telegram_id"))
	case "approved_users":
		return s.listUsers(ctx, ptr("approved"), 100000, 0)
	case "get_setting":
		return s.getSetting(ctx, strParam(p, "key"))
	case "set_setting":
		return nil, s.setSetting(ctx, strParam(p, "key"), strParam(p, "value"))
	case "delete_setting":
		return nil, s.exec(ctx, "DELETE FROM settings WHERE key = ?", strParam(p, "key"))
	case "list_settings":
		return s.listSettings(ctx, strPtrParam(p, "prefix"))
	case "create_payment":
		return nil, s.createPayment(ctx, p)
	case "get_payment":
		return s.getPayment(ctx, strParam(p, "transaction_id"))
	case "update_payment_status":
		return nil, s.exec(ctx, "UPDATE payments SET status = ?, updated_at = ? WHERE transaction_id = ?", strParam(p, "status"), now(), strParam(p, "transaction_id"))
	case "list_users":
		return s.listUsers(ctx, strPtrParam(p, "status"), intDefault(p, "limit", 10), intDefault(p, "offset", 0))
	case "count_users":
		return s.countUsers(ctx, strPtrParam(p, "status"))
	case "approved_telegram_ids":
		return s.approvedTelegramIDs(ctx)
	default:
		return nil, fmt.Errorf("unknown method %s", method)
	}
}

func (s *Server) initSchema(ctx context.Context) error {
	pragmas := []string{
		"PRAGMA foreign_keys = ON",
		"PRAGMA journal_mode = WAL",
		"PRAGMA synchronous = NORMAL",
		"PRAGMA busy_timeout = 5000",
		"PRAGMA secure_delete = ON",
	}
	for _, q := range pragmas {
		if _, err := s.db.ExecContext(ctx, q); err != nil {
			return err
		}
	}
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS users (
			telegram_id INTEGER PRIMARY KEY,
			username TEXT,
			first_name TEXT,
			last_name TEXT,
			status TEXT NOT NULL DEFAULT 'requested' CHECK (status IN ('requested', 'approved', 'rejected')),
			language TEXT NOT NULL DEFAULT 'ru' CHECK (language IN ('ru', 'en')),
			nc_user_id TEXT,
			nc_password TEXT,
			quota_gb INTEGER NOT NULL DEFAULT 0 CHECK (quota_gb >= 0),
			is_supporter INTEGER NOT NULL DEFAULT 0 CHECK (is_supporter IN (0, 1)),
			supporter_until TEXT,
			is_disabled INTEGER NOT NULL DEFAULT 0 CHECK (is_disabled IN (0, 1)),
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			approved_at TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS settings (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS payments (
			transaction_id TEXT PRIMARY KEY,
			telegram_id INTEGER NOT NULL REFERENCES users(telegram_id) ON DELETE CASCADE,
			provider TEXT NOT NULL,
			amount INTEGER NOT NULL CHECK (amount > 0),
			currency TEXT NOT NULL,
			status TEXT NOT NULL,
			payment_url TEXT,
			payload TEXT,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
	}
	for _, q := range stmts {
		if _, err := s.db.ExecContext(ctx, q); err != nil {
			return err
		}
	}
	for _, col := range []struct{ table, name, typ string }{
		{"users", "nc_password", "TEXT"},
		{"users", "language", "TEXT NOT NULL DEFAULT 'ru'"},
		{"users", "is_supporter", "INTEGER NOT NULL DEFAULT 0"},
		{"users", "supporter_until", "TEXT"},
	} {
		if err := s.ensureColumn(ctx, col.table, col.name, col.typ); err != nil {
			return err
		}
	}
	return s.encryptExistingPasswords(ctx)
}

func (s *Server) ensureColumn(ctx context.Context, table, name, typ string) error {
	rows, err := s.db.QueryContext(ctx, "PRAGMA table_info("+table+")")
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var colName, colType string
		var notNull int
		var defaultValue any
		var pk int
		if err := rows.Scan(&cid, &colName, &colType, &notNull, &defaultValue, &pk); err != nil {
			return err
		}
		if colName == name {
			return nil
		}
	}
	_, err = s.db.ExecContext(ctx, "ALTER TABLE "+table+" ADD COLUMN "+name+" "+typ)
	return err
}

func (s *Server) upsertRequest(ctx context.Context, id int64, username, firstName, lastName *string) (*User, error) {
	current, err := s.getUser(ctx, id)
	t := now()
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if current != nil {
		if err := s.exec(ctx, "UPDATE users SET username = ?, first_name = ?, last_name = ?, updated_at = ? WHERE telegram_id = ?", username, firstName, lastName, t, id); err != nil {
			return nil, err
		}
	} else {
		if err := s.exec(ctx, `INSERT INTO users (telegram_id, username, first_name, last_name, status, language, created_at, updated_at) VALUES (?, ?, ?, ?, 'requested', 'ru', ?, ?)`, id, username, firstName, lastName, t, t); err != nil {
			return nil, err
		}
	}
	return s.getUser(ctx, id)
}

func (s *Server) approveUser(ctx context.Context, id int64, ncUserID, password string, quota int) error {
	t := now()
	return s.exec(ctx, `UPDATE users SET status = 'approved', nc_user_id = ?, nc_password = ?, quota_gb = ?, is_disabled = 0, approved_at = COALESCE(approved_at, ?), updated_at = ? WHERE telegram_id = ?`, ncUserID, s.encrypt(password), quota, t, t, id)
}

func (s *Server) getUser(ctx context.Context, id int64) (*User, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT telegram_id, username, first_name, last_name, status, language, nc_user_id, nc_password, quota_gb, is_supporter, supporter_until, is_disabled, created_at, updated_at, approved_at FROM users WHERE telegram_id = ?`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	users, err := s.scanUsers(rows)
	if err != nil {
		return nil, err
	}
	if len(users) == 0 {
		return nil, nil
	}
	return &users[0], nil
}

func (s *Server) listUsers(ctx context.Context, status *string, limit, offset int) ([]User, error) {
	var rows *sql.Rows
	var err error
	if status != nil && *status != "" {
		rows, err = s.db.QueryContext(ctx, `SELECT telegram_id, username, first_name, last_name, status, language, nc_user_id, nc_password, quota_gb, is_supporter, supporter_until, is_disabled, created_at, updated_at, approved_at FROM users WHERE status = ? ORDER BY created_at DESC LIMIT ? OFFSET ?`, *status, limit, offset)
	} else {
		rows, err = s.db.QueryContext(ctx, `SELECT telegram_id, username, first_name, last_name, status, language, nc_user_id, nc_password, quota_gb, is_supporter, supporter_until, is_disabled, created_at, updated_at, approved_at FROM users ORDER BY created_at DESC LIMIT ? OFFSET ?`, limit, offset)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return s.scanUsers(rows)
}

func (s *Server) scanUsers(rows *sql.Rows) ([]User, error) {
	users := []User{}
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.TelegramID, &u.Username, &u.FirstName, &u.LastName, &u.Status, &u.Language, &u.NCUserID, &u.NCPassword, &u.QuotaGB, &u.IsSupporter, &u.SupporterUntil, &u.IsDisabled, &u.CreatedAt, &u.UpdatedAt, &u.ApprovedAt); err != nil {
			return nil, err
		}
		if u.NCPassword != nil {
			plain, err := s.decrypt(*u.NCPassword)
			if err != nil {
				return nil, err
			}
			u.NCPassword = &plain
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

func (s *Server) countUsers(ctx context.Context, status *string) (int, error) {
	var row *sql.Row
	if status != nil && *status != "" {
		row = s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM users WHERE status = ?", *status)
	} else {
		row = s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM users")
	}
	var count int
	if err := row.Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func (s *Server) approvedTelegramIDs(ctx context.Context) ([]int64, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT telegram_id FROM users WHERE status = 'approved' AND is_disabled = 0")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (s *Server) getSetting(ctx context.Context, key string) (*string, error) {
	row := s.db.QueryRowContext(ctx, "SELECT value FROM settings WHERE key = ?", key)
	var value string
	if err := row.Scan(&value); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &value, nil
}

func (s *Server) setSetting(ctx context.Context, key, value string) error {
	return s.exec(ctx, `INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`, key, value, now())
}

func (s *Server) listSettings(ctx context.Context, prefix *string) (map[string]string, error) {
	var rows *sql.Rows
	var err error
	if prefix != nil && *prefix != "" {
		rows, err = s.db.QueryContext(ctx, "SELECT key, value FROM settings WHERE key LIKE ?", *prefix+"%")
	} else {
		rows, err = s.db.QueryContext(ctx, "SELECT key, value FROM settings")
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	settings := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		settings[k] = v
	}
	return settings, rows.Err()
}

func (s *Server) createPayment(ctx context.Context, p map[string]any) error {
	t := now()
	return s.exec(ctx, `INSERT INTO payments (transaction_id, telegram_id, provider, amount, currency, status, payment_url, payload, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(transaction_id) DO UPDATE SET status = excluded.status, payment_url = excluded.payment_url, payload = excluded.payload, updated_at = excluded.updated_at`,
		strParam(p, "transaction_id"), int64Param(p, "telegram_id"), strParam(p, "provider"), intParam(p, "amount"), strParam(p, "currency"), strParam(p, "status"), strPtrParam(p, "payment_url"), strPtrParam(p, "payload"), t, t)
}

func (s *Server) getPayment(ctx context.Context, id string) (*Payment, error) {
	row := s.db.QueryRowContext(ctx, `SELECT transaction_id, telegram_id, provider, amount, currency, status, payment_url, payload, created_at, updated_at FROM payments WHERE transaction_id = ?`, id)
	var p Payment
	if err := row.Scan(&p.TransactionID, &p.TelegramID, &p.Provider, &p.Amount, &p.Currency, &p.Status, &p.PaymentURL, &p.Payload, &p.CreatedAt, &p.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &p, nil
}

func (s *Server) exec(ctx context.Context, query string, args ...any) error {
	_, err := s.db.ExecContext(ctx, query, args...)
	return err
}

func (s *Server) encryptExistingPasswords(ctx context.Context) error {
	if s.aead == nil {
		return nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT telegram_id, nc_password FROM users WHERE nc_password IS NOT NULL AND nc_password != '' AND nc_password NOT LIKE 'gcm:%'`)
	if err != nil {
		return err
	}
	defer rows.Close()
	type item struct {
		id int64
		pw string
	}
	var items []item
	for rows.Next() {
		var it item
		if err := rows.Scan(&it.id, &it.pw); err != nil {
			return err
		}
		items = append(items, it)
	}
	for _, it := range items {
		if err := s.exec(ctx, "UPDATE users SET nc_password = ? WHERE telegram_id = ?", s.encrypt(it.pw), it.id); err != nil {
			return err
		}
	}
	return rows.Err()
}

func (s *Server) encrypt(value string) string {
	if value == "" || s.aead == nil || strings.HasPrefix(value, "gcm:") {
		return value
	}
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		panic(err)
	}
	sealed := s.aead.Seal(nonce, nonce, []byte(value), nil)
	return "gcm:" + base64.RawURLEncoding.EncodeToString(sealed)
}

func (s *Server) decrypt(value string) (string, error) {
	if !strings.HasPrefix(value, "gcm:") {
		return value, nil
	}
	if s.aead == nil {
		return "", errors.New("DATABASE_SECRET_KEY is required to decrypt stored Nextcloud passwords")
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(value, "gcm:"))
	if err != nil {
		return "", err
	}
	if len(raw) < s.aead.NonceSize() {
		return "", errors.New("encrypted value is too short")
	}
	nonce := raw[:s.aead.NonceSize()]
	ciphertext := raw[s.aead.NonceSize():]
	plain, err := s.aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

func writeJSON(w http.ResponseWriter, status int, data RPCResponse) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func now() string { return time.Now().UTC().Format(time.RFC3339) }

func ptr(s string) *string { return &s }

func env(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

func envInt(name string, fallback int) int {
	v, err := strconv.Atoi(os.Getenv(name))
	if err != nil || v <= 0 {
		return fallback
	}
	return v
}

func chmodDB(path string) error {
	if err := os.Chmod(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	for _, p := range []string{path, path + "-wal", path + "-shm"} {
		if _, err := os.Stat(p); err == nil {
			if err := os.Chmod(p, 0o600); err != nil {
				return err
			}
		}
	}
	return nil
}

func strParam(p map[string]any, key string) string {
	if v := strPtrParam(p, key); v != nil {
		return *v
	}
	return ""
}

func strPtrParam(p map[string]any, key string) *string {
	v, ok := p[key]
	if !ok || v == nil {
		return nil
	}
	s := fmt.Sprint(v)
	return &s
}

func intParam(p map[string]any, key string) int {
	return int(int64Param(p, key))
}

func intDefault(p map[string]any, key string, fallback int) int {
	if _, ok := p[key]; !ok || p[key] == nil {
		return fallback
	}
	return intParam(p, key)
}

func int64Param(p map[string]any, key string) int64 {
	v, ok := p[key]
	if !ok || v == nil {
		return 0
	}
	switch x := v.(type) {
	case float64:
		return int64(x)
	case int:
		return int64(x)
	case int64:
		return x
	case string:
		i, _ := strconv.ParseInt(x, 10, 64)
		return i
	default:
		return 0
	}
}

func boolParam(p map[string]any, key string) bool {
	v, ok := p[key]
	if !ok || v == nil {
		return false
	}
	switch x := v.(type) {
	case bool:
		return x
	case string:
		return x == "true" || x == "1"
	case float64:
		return x != 0
	default:
		return false
	}
}

func boolInt(p map[string]any, key string) int {
	if boolParam(p, key) {
		return 1
	}
	return 0
}
