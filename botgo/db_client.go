package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
)

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

