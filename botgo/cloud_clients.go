package main

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
)

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

