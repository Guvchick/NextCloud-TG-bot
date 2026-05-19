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
	"log"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	_ "github.com/lib/pq"
)

type DB struct {
	db      *sql.DB
	aead    cipher.AEAD
	premium int
}

var placeholderPattern = regexp.MustCompile(`\?`)

func NewDB(cfg Config) (*DB, error) {
	dsn := env("POSTGRES_DSN", "")
	if dsn == "" {
		dsn = fmt.Sprintf(
			"host=%s port=%s user=%s password=%s dbname=%s sslmode=%s",
			env("POSTGRES_HOST", "postgres"),
			env("POSTGRES_PORT", "5432"),
			env("POSTGRES_USER", "bot"),
			os.Getenv("POSTGRES_PASSWORD"),
			env("POSTGRES_DB", "bot"),
			env("POSTGRES_SSLMODE", "disable"),
		)
	}
	sqlDB, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, err
	}
	sqlDB.SetMaxOpenConns(envInt("POSTGRES_MAX_OPEN_CONNS", 10))
	sqlDB.SetMaxIdleConns(envInt("POSTGRES_MAX_IDLE_CONNS", 5))
	sqlDB.SetConnMaxLifetime(30 * time.Minute)
	if err := waitForPostgres(context.Background(), sqlDB); err != nil {
		_ = sqlDB.Close()
		return nil, err
	}
	db := &DB{db: sqlDB, premium: cfg.PremiumDays}
	if secret := os.Getenv("DATABASE_SECRET_KEY"); secret != "" {
		key := sha256.Sum256([]byte(secret))
		block, err := aes.NewCipher(key[:])
		if err != nil {
			_ = sqlDB.Close()
			return nil, err
		}
		aead, err := cipher.NewGCM(block)
		if err != nil {
			_ = sqlDB.Close()
			return nil, err
		}
		db.aead = aead
	}
	return db, nil
}

func pg(query string) string {
	index := 0
	return placeholderPattern.ReplaceAllStringFunc(query, func(_ string) string {
		index++
		return "$" + strconv.Itoa(index)
	})
}

func waitForPostgres(ctx context.Context, db *sql.DB) error {
	var last error
	for i := 0; i < 30; i++ {
		if err := db.PingContext(ctx); err == nil {
			return nil
		} else {
			last = err
		}
		time.Sleep(time.Second)
	}
	return last
}

func (db *DB) Init() error {
	return db.initSchema(context.Background())
}

func (db *DB) UpsertRequest(id int64, username, firstName, lastName *string) (*User, error) {
	return db.upsertRequest(context.Background(), id, username, firstName, lastName)
}

func (db *DB) GetUser(id int64) (*User, error) {
	return db.getUser(context.Background(), id)
}

func (db *DB) ApproveUser(id int64, ncUserID, password string, quota int) error {
	return db.approveUser(context.Background(), id, ncUserID, password, quota)
}

func (db *DB) SetNextcloudPassword(id int64, password string) error {
	return db.exec(context.Background(), "UPDATE users SET nc_password = ?, updated_at = ? WHERE telegram_id = ?", db.encrypt(password), now(), id)
}

func (db *DB) RejectUser(id int64) error {
	return db.exec(context.Background(), "UPDATE users SET status = 'rejected', updated_at = ? WHERE telegram_id = ?", now(), id)
}

func (db *DB) SetLanguage(id int64, language string) error {
	return db.exec(context.Background(), "UPDATE users SET language = ?, updated_at = ? WHERE telegram_id = ?", language, now(), id)
}

func (db *DB) SetQuota(id int64, quota int) error {
	return db.exec(context.Background(), "UPDATE users SET quota_gb = ?, updated_at = ? WHERE telegram_id = ?", quota, now(), id)
}

func (db *DB) SetDisabled(id int64, disabled bool) error {
	return db.exec(context.Background(), "UPDATE users SET is_disabled = ?, updated_at = ? WHERE telegram_id = ?", boolToInt(disabled), now(), id)
}

func (db *DB) DeleteUser(id int64) error {
	return db.exec(context.Background(), "DELETE FROM users WHERE telegram_id = ?", id)
}

func (db *DB) SetSupporter(id int64, enabled bool, until *string) error {
	if !enabled {
		until = nil
	}
	return db.exec(context.Background(), "UPDATE users SET is_supporter = ?, supporter_until = ?, updated_at = ? WHERE telegram_id = ?", boolToInt(enabled), until, now(), id)
}

func (db *DB) ExpireSupporters() (int, error) {
	res, err := db.db.ExecContext(context.Background(), pg(`UPDATE users SET is_supporter = 0, supporter_until = NULL, updated_at = ? WHERE is_supporter = 1 AND supporter_until IS NOT NULL AND supporter_until <= ?`), now(), now())
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

func (db *DB) ListUsers(status string, limit, offset int) ([]User, error) {
	var statusPtr *string
	if status != "" {
		statusPtr = &status
	}
	return db.listUsers(context.Background(), statusPtr, limit, offset)
}

func (db *DB) SearchUsers(query string, limit int) ([]User, error) {
	return db.searchUsers(context.Background(), query, limit)
}

func (db *DB) CountUsers(status string) (int, error) {
	var statusPtr *string
	if status != "" {
		statusPtr = &status
	}
	return db.countUsers(context.Background(), statusPtr)
}

func (db *DB) ApprovedTelegramIDs() ([]int64, error) {
	return db.approvedTelegramIDs(context.Background())
}

func (db *DB) RestoreUsers(users []User) error {
	return db.restoreUsers(context.Background(), users)
}

func (db *DB) GetSetting(key string) (string, error) {
	value, err := db.getSetting(context.Background(), key)
	if value == nil {
		return "", err
	}
	return *value, err
}

func (db *DB) SetSetting(key, value string) error {
	return db.setSetting(context.Background(), key, value)
}

func (db *DB) ListSettings(prefix string) (map[string]string, error) {
	var prefixPtr *string
	if prefix != "" {
		prefixPtr = &prefix
	}
	settings, err := db.listSettings(context.Background(), prefixPtr)
	if settings == nil {
		settings = map[string]string{}
	}
	return settings, err
}

func (db *DB) CreatePayment(transactionID string, telegramID int64, provider string, amount int, currency, status string, paymentURL *string, payload *string) error {
	return db.createPayment(context.Background(), transactionID, telegramID, provider, amount, currency, status, paymentURL, payload)
}

func (db *DB) GetPayment(transactionID string) (*Payment, error) {
	return db.getPayment(context.Background(), transactionID)
}

func (db *DB) UpdatePaymentStatus(transactionID, status string) error {
	return db.exec(context.Background(), "UPDATE payments SET status = ?, updated_at = ? WHERE transaction_id = ?", status, now(), transactionID)
}

func (db *DB) initSchema(ctx context.Context) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS users (
			telegram_id BIGINT PRIMARY KEY,
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
			telegram_id BIGINT NOT NULL REFERENCES users(telegram_id) ON DELETE CASCADE,
			provider TEXT NOT NULL,
			amount INTEGER NOT NULL CHECK (amount > 0),
			currency TEXT NOT NULL,
			status TEXT NOT NULL,
			payment_url TEXT,
			payload TEXT,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS users_status_created_idx ON users (status, created_at DESC)`,
		`CREATE INDEX IF NOT EXISTS users_username_idx ON users (lower(username))`,
		`CREATE INDEX IF NOT EXISTS payments_telegram_id_idx ON payments (telegram_id)`,
	}
	for _, q := range stmts {
		if _, err := db.db.ExecContext(ctx, q); err != nil {
			return err
		}
	}
	for _, col := range []struct{ table, name, typ string }{
		{"users", "nc_password", "TEXT"},
		{"users", "language", "TEXT NOT NULL DEFAULT 'ru'"},
		{"users", "is_supporter", "INTEGER NOT NULL DEFAULT 0"},
		{"users", "supporter_until", "TEXT"},
	} {
		if err := db.ensureColumn(ctx, col.table, col.name, col.typ); err != nil {
			return err
		}
	}
	return db.encryptExistingPasswords(ctx)
}

func (db *DB) ensureColumn(ctx context.Context, table, name, typ string) error {
	var exists bool
	err := db.db.QueryRowContext(
		ctx,
		`SELECT EXISTS (
			SELECT 1 FROM information_schema.columns
			WHERE table_schema = current_schema() AND table_name = $1 AND column_name = $2
		)`,
		table,
		name,
	).Scan(&exists)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	_, err = db.db.ExecContext(ctx, "ALTER TABLE "+table+" ADD COLUMN "+name+" "+typ)
	return err
}

func (db *DB) upsertRequest(ctx context.Context, id int64, username, firstName, lastName *string) (*User, error) {
	current, err := db.getUser(ctx, id)
	t := now()
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if current != nil {
		if err := db.exec(ctx, "UPDATE users SET username = ?, first_name = ?, last_name = ?, updated_at = ? WHERE telegram_id = ?", username, firstName, lastName, t, id); err != nil {
			return nil, err
		}
	} else {
		if err := db.exec(ctx, `INSERT INTO users (telegram_id, username, first_name, last_name, status, language, created_at, updated_at) VALUES (?, ?, ?, ?, 'requested', 'ru', ?, ?)`, id, username, firstName, lastName, t, t); err != nil {
			return nil, err
		}
	}
	return db.getUser(ctx, id)
}

func (db *DB) approveUser(ctx context.Context, id int64, ncUserID, password string, quota int) error {
	t := now()
	return db.exec(ctx, `UPDATE users SET status = 'approved', nc_user_id = ?, nc_password = ?, quota_gb = ?, is_disabled = 0, approved_at = COALESCE(approved_at, ?), updated_at = ? WHERE telegram_id = ?`, ncUserID, db.encrypt(password), quota, t, t, id)
}

func (db *DB) getUser(ctx context.Context, id int64) (*User, error) {
	rows, err := db.db.QueryContext(ctx, pg(`SELECT telegram_id, username, first_name, last_name, status, language, nc_user_id, nc_password, quota_gb, is_supporter, supporter_until, is_disabled, created_at, updated_at, approved_at FROM users WHERE telegram_id = ?`), id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	users, err := db.scanUsers(rows)
	if err != nil {
		return nil, err
	}
	if len(users) == 0 {
		return nil, nil
	}
	return &users[0], nil
}

func (db *DB) listUsers(ctx context.Context, status *string, limit, offset int) ([]User, error) {
	var rows *sql.Rows
	var err error
	if status != nil && *status != "" {
		rows, err = db.db.QueryContext(ctx, pg(`SELECT telegram_id, username, first_name, last_name, status, language, nc_user_id, nc_password, quota_gb, is_supporter, supporter_until, is_disabled, created_at, updated_at, approved_at FROM users WHERE status = ? ORDER BY created_at DESC LIMIT ? OFFSET ?`), *status, limit, offset)
	} else {
		rows, err = db.db.QueryContext(ctx, pg(`SELECT telegram_id, username, first_name, last_name, status, language, nc_user_id, nc_password, quota_gb, is_supporter, supporter_until, is_disabled, created_at, updated_at, approved_at FROM users ORDER BY created_at DESC LIMIT ? OFFSET ?`), limit, offset)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return db.scanUsers(rows)
}

func (db *DB) searchUsers(ctx context.Context, query string, limit int) ([]User, error) {
	query = strings.TrimSpace(strings.TrimPrefix(query, "@"))
	if query == "" {
		return []User{}, nil
	}
	if limit <= 0 || limit > 50 {
		limit = 10
	}
	like := "%" + query + "%"
	id, _ := strconv.ParseInt(query, 10, 64)
	rows, err := db.db.QueryContext(
		ctx,
		pg(`SELECT telegram_id, username, first_name, last_name, status, language, nc_user_id, nc_password, quota_gb, is_supporter, supporter_until, is_disabled, created_at, updated_at, approved_at
		FROM users
		WHERE telegram_id = ?
			OR username ILIKE ?
			OR first_name ILIKE ?
			OR last_name ILIKE ?
		ORDER BY
			CASE WHEN telegram_id = ? THEN 0 WHEN lower(username) = lower(?) THEN 1 ELSE 2 END,
			created_at DESC
		LIMIT ?`),
		id,
		like,
		like,
		like,
		id,
		query,
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return db.scanUsers(rows)
}

func (db *DB) scanUsers(rows *sql.Rows) ([]User, error) {
	users := []User{}
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.TelegramID, &u.Username, &u.FirstName, &u.LastName, &u.Status, &u.Language, &u.NCUserID, &u.NCPassword, &u.QuotaGB, &u.IsSupporter, &u.SupporterUntil, &u.IsDisabled, &u.CreatedAt, &u.UpdatedAt, &u.ApprovedAt); err != nil {
			return nil, err
		}
		if u.NCPassword != nil {
			plain, err := db.decrypt(*u.NCPassword)
			if err != nil {
				return nil, err
			}
			u.NCPassword = &plain
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

func (db *DB) countUsers(ctx context.Context, status *string) (int, error) {
	var row *sql.Row
	if status != nil && *status != "" {
		row = db.db.QueryRowContext(ctx, pg("SELECT COUNT(*) FROM users WHERE status = ?"), *status)
	} else {
		row = db.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM users")
	}
	var count int
	if err := row.Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func (db *DB) approvedTelegramIDs(ctx context.Context) ([]int64, error) {
	rows, err := db.db.QueryContext(ctx, "SELECT telegram_id FROM users WHERE status = 'approved' AND is_disabled = 0")
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

func (db *DB) getSetting(ctx context.Context, key string) (*string, error) {
	row := db.db.QueryRowContext(ctx, pg("SELECT value FROM settings WHERE key = ?"), key)
	var value string
	if err := row.Scan(&value); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &value, nil
}

func (db *DB) setSetting(ctx context.Context, key, value string) error {
	return db.exec(ctx, `INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`, key, value, now())
}

func (db *DB) listSettings(ctx context.Context, prefix *string) (map[string]string, error) {
	var rows *sql.Rows
	var err error
	if prefix != nil && *prefix != "" {
		rows, err = db.db.QueryContext(ctx, pg("SELECT key, value FROM settings WHERE key LIKE ?"), *prefix+"%")
	} else {
		rows, err = db.db.QueryContext(ctx, "SELECT key, value FROM settings")
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

func (db *DB) createPayment(ctx context.Context, transactionID string, telegramID int64, provider string, amount int, currency, status string, paymentURL *string, payload *string) error {
	t := now()
	return db.exec(ctx, `INSERT INTO payments (transaction_id, telegram_id, provider, amount, currency, status, payment_url, payload, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(transaction_id) DO UPDATE SET status = excluded.status, payment_url = excluded.payment_url, payload = excluded.payload, updated_at = excluded.updated_at`,
		transactionID, telegramID, provider, amount, currency, status, paymentURL, payload, t, t)
}

func (db *DB) getPayment(ctx context.Context, id string) (*Payment, error) {
	row := db.db.QueryRowContext(ctx, pg(`SELECT transaction_id, telegram_id, provider, amount, currency, status FROM payments WHERE transaction_id = ?`), id)
	var p Payment
	if err := row.Scan(&p.TransactionID, &p.TelegramID, &p.Provider, &p.Amount, &p.Currency, &p.Status); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &p, nil
}

func (db *DB) restoreUsers(ctx context.Context, users []User) error {
	tx, err := db.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt := pg(`INSERT INTO users (
		telegram_id, username, first_name, last_name, status, language, nc_user_id, nc_password,
		quota_gb, is_supporter, supporter_until, is_disabled, created_at, updated_at, approved_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(telegram_id) DO UPDATE SET
		username = excluded.username,
		first_name = excluded.first_name,
		last_name = excluded.last_name,
		status = excluded.status,
		language = excluded.language,
		nc_user_id = excluded.nc_user_id,
		nc_password = excluded.nc_password,
		quota_gb = excluded.quota_gb,
		is_supporter = excluded.is_supporter,
		supporter_until = excluded.supporter_until,
		is_disabled = excluded.is_disabled,
		created_at = excluded.created_at,
		updated_at = excluded.updated_at,
		approved_at = excluded.approved_at`)
	for _, u := range users {
		status := u.Status
		if status == "" {
			status = "requested"
		}
		language := u.Language
		if language == "" {
			language = "ru"
		}
		createdAt := u.CreatedAt
		if createdAt == "" {
			createdAt = now()
		}
		updatedAt := u.UpdatedAt
		if updatedAt == "" {
			updatedAt = now()
		}
		var password *string
		if u.NCPassword != nil {
			encrypted := db.encrypt(*u.NCPassword)
			password = &encrypted
		}
		if _, err := tx.ExecContext(
			ctx,
			stmt,
			u.TelegramID,
			u.Username,
			u.FirstName,
			u.LastName,
			status,
			language,
			u.NCUserID,
			password,
			u.QuotaGB,
			u.IsSupporter,
			u.SupporterUntil,
			u.IsDisabled,
			createdAt,
			updatedAt,
			u.ApprovedAt,
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (db *DB) exec(ctx context.Context, query string, args ...any) error {
	_, err := db.db.ExecContext(ctx, pg(query), args...)
	return err
}

func (db *DB) encryptExistingPasswords(ctx context.Context) error {
	if db.aead == nil {
		return nil
	}
	rows, err := db.db.QueryContext(ctx, `SELECT telegram_id, nc_password FROM users WHERE nc_password IS NOT NULL AND nc_password != '' AND nc_password NOT LIKE 'gcm:%'`)
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
		if err := db.exec(ctx, "UPDATE users SET nc_password = ? WHERE telegram_id = ?", db.encrypt(it.pw), it.id); err != nil {
			return err
		}
	}
	return rows.Err()
}

func (db *DB) encrypt(value string) string {
	if value == "" || db.aead == nil || strings.HasPrefix(value, "gcm:") {
		return value
	}
	nonce := make([]byte, db.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		panic(err)
	}
	sealed := db.aead.Seal(nonce, nonce, []byte(value), nil)
	return "gcm:" + base64.RawURLEncoding.EncodeToString(sealed)
}

func (db *DB) decrypt(value string) (string, error) {
	if !strings.HasPrefix(value, "gcm:") {
		return value, nil
	}
	if db.aead == nil {
		return "", errors.New("DATABASE_SECRET_KEY is required to decrypt stored Nextcloud passwords")
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(value, "gcm:"))
	if err != nil {
		return "", err
	}
	if len(raw) < db.aead.NonceSize() {
		return "", errors.New("encrypted value is too short")
	}
	nonce := raw[:db.aead.NonceSize()]
	ciphertext := raw[db.aead.NonceSize():]
	plain, err := db.aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

func now() string { return time.Now().UTC().Format(time.RFC3339) }

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func mustJSON(value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		log.Printf("json marshal failed: %v", err)
		return ""
	}
	return string(raw)
}
