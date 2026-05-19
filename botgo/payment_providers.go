package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/md5"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type Pally struct {
	token   string
	shopID  string
	baseURL string
	client  *http.Client
}

type CryptoBotPay struct {
	token   string
	baseURL string
	client  *http.Client
}

type Heleket struct {
	merchantID string
	apiKey     string
	baseURL    string
	client     *http.Client
}

func (p *Pally) CreateBill(amount int, description, payload string) (map[string]any, error) {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	_ = writer.WriteField("amount", strconv.Itoa(amount))
	_ = writer.WriteField("order_id", newOrderID("pally", payload))
	_ = writer.WriteField("description", description)
	_ = writer.WriteField("type", "normal")
	_ = writer.WriteField("shop_id", p.shopID)
	_ = writer.WriteField("currency_in", "RUB")
	_ = writer.WriteField("custom", payload)
	_ = writer.WriteField("payer_pays_commission", "1")
	_ = writer.WriteField("name", "Cloud")
	_ = writer.Close()

	req, err := http.NewRequest(http.MethodPost, strings.TrimRight(p.baseURL, "/")+"/api/v1/bill/create", body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+p.token)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Accept", "application/json")
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, fmt.Errorf("Pally returned non-JSON response (%d): %s", resp.StatusCode, string(raw[:min(len(raw), 300)]))
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("Pally HTTP %d: %s", resp.StatusCode, string(raw[:min(len(raw), 300)]))
	}
	billID := firstMapString(data, "bill_id", "id")
	paymentURL := firstMapString(data, "link_page_url", "link_url", "url")
	if billID == "" || paymentURL == "" {
		return nil, errors.New("Pally response does not contain bill_id or link_page_url")
	}
	data["transactionId"] = billID
	data["url"] = paymentURL
	data["status"] = "PENDING"
	return data, nil
}

func (p *Pally) VerifyPostback(form url.Values) bool {
	expectedRaw := form.Get("OutSum") + ":" + form.Get("InvId") + ":" + p.token
	sum := md5.Sum([]byte(expectedRaw))
	expected := strings.ToUpper(hex.EncodeToString(sum[:]))
	return expected != "" && hmac.Equal([]byte(expected), []byte(strings.ToUpper(form.Get("SignatureValue"))))
}

func (c *CryptoBotPay) request(path string, body any) (map[string]any, error) {
	var reader io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(http.MethodPost, strings.TrimRight(c.baseURL, "/")+"/api/"+path, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Crypto-Pay-API-Token", c.token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	var envelope map[string]any
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("CryptoBot returned non-JSON response (%d): %s", resp.StatusCode, string(raw[:min(len(raw), 300)]))
	}
	if resp.StatusCode >= 400 || fmt.Sprint(envelope["ok"]) != "true" {
		return nil, fmt.Errorf("CryptoBot HTTP %d: %s", resp.StatusCode, string(raw[:min(len(raw), 300)]))
	}
	result, _ := envelope["result"].(map[string]any)
	if result == nil {
		if items, ok := envelope["result"].([]any); ok {
			return map[string]any{"items": items}, nil
		}
		return nil, errors.New("CryptoBot response does not contain result")
	}
	return result, nil
}

func (c *CryptoBotPay) CreateInvoice(amount int, description, payload, returnURL string) (map[string]any, error) {
	body := map[string]any{
		"currency_type":   "fiat",
		"fiat":            "RUB",
		"amount":          strconv.Itoa(amount),
		"description":     description,
		"payload":         payload,
		"allow_comments":  false,
		"allow_anonymous": true,
	}
	if returnURL != "" {
		body["paid_btn_name"] = "callback"
		body["paid_btn_url"] = returnURL
	}
	data, err := c.request("createInvoice", body)
	if err != nil {
		return nil, err
	}
	transactionID := firstMapString(data, "invoice_id")
	paymentURL := firstMapString(data, "bot_invoice_url", "web_app_invoice_url", "mini_app_invoice_url", "pay_url")
	if transactionID == "" || paymentURL == "" {
		return nil, errors.New("CryptoBot response does not contain invoice_id or invoice URL")
	}
	data["transactionId"] = transactionID
	data["url"] = paymentURL
	data["status"] = strings.ToUpper(firstMapString(data, "status"))
	return data, nil
}

func (c *CryptoBotPay) Invoice(invoiceID string) (map[string]any, error) {
	result, err := c.request("getInvoices", map[string]any{"invoice_ids": invoiceID})
	if err != nil {
		return nil, err
	}
	items, _ := result["items"].([]any)
	if len(items) > 0 {
		if item, ok := items[0].(map[string]any); ok {
			return item, nil
		}
	}
	return result, nil
}

func (c *CryptoBotPay) VerifyWebhook(raw []byte, signature string) bool {
	if signature == "" {
		return false
	}
	secret := sha256.Sum256([]byte(c.token))
	mac := hmac.New(sha256.New, secret[:])
	_, _ = mac.Write(raw)
	expected := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(expected), []byte(strings.ToLower(signature)))
}

func (h *Heleket) request(path string, body map[string]any) (map[string]any, error) {
	if body == nil {
		body = map[string]any{}
	}
	raw := marshalJSONNoEscape(body)
	req, err := http.NewRequest(http.MethodPost, strings.TrimRight(h.baseURL, "/")+path, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("merchant", h.merchantID)
	req.Header.Set("sign", h.sign(raw))
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	resp, err := h.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	respRaw, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	var data map[string]any
	if err := json.Unmarshal(respRaw, &data); err != nil {
		return nil, fmt.Errorf("Heleket returned non-JSON response (%d): %s", resp.StatusCode, string(respRaw[:min(len(respRaw), 300)]))
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("Heleket HTTP %d: %s", resp.StatusCode, string(respRaw[:min(len(respRaw), 300)]))
	}
	return unwrapResult(data), nil
}

func (h *Heleket) CreatePayment(amount int, currency, toCurrency, callbackURL, returnURL string, payload string) (map[string]any, error) {
	orderID := newOrderID("hel", payload)
	body := map[string]any{
		"amount":              strconv.Itoa(amount),
		"currency":            currency,
		"order_id":            orderID,
		"is_payment_multiple": false,
		"lifetime":            3600,
	}
	if callbackURL != "" {
		body["url_callback"] = callbackURL
	}
	if toCurrency != "" {
		body["to_currency"] = toCurrency
	}
	if returnURL != "" {
		body["url_return"] = returnURL
		body["url_success"] = returnURL
	}
	data, err := h.request("/v1/payment", body)
	if err != nil {
		return nil, err
	}
	paymentURL := firstMapString(data, "url", "payment_url")
	if paymentURL == "" {
		return nil, errors.New("Heleket response does not contain payment url")
	}
	data["transactionId"] = orderID
	data["url"] = paymentURL
	data["status"] = strings.ToUpper(firstMapString(data, "status"))
	return data, nil
}

func (h *Heleket) PaymentInfo(orderID string) (map[string]any, error) {
	return h.request("/v1/payment/info", map[string]any{"order_id": orderID})
}

func (h *Heleket) sign(raw []byte) string {
	sum := md5.Sum([]byte(base64.StdEncoding.EncodeToString(raw) + h.apiKey))
	return hex.EncodeToString(sum[:])
}

func (h *Heleket) VerifyWebhook(payload map[string]any) bool {
	signature := strings.TrimSpace(fmt.Sprint(payload["sign"]))
	if signature == "" {
		return false
	}
	clone := map[string]any{}
	for key, value := range payload {
		if key != "sign" {
			clone[key] = value
		}
	}
	raw := marshalJSONNoEscape(clone)
	return hmac.Equal([]byte(strings.ToLower(signature)), []byte(h.sign(raw)))
}

func marshalJSONNoEscape(value any) []byte {
	body := &bytes.Buffer{}
	encoder := json.NewEncoder(body)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(value)
	return bytes.TrimSpace(body.Bytes())
}

func unwrapResult(data map[string]any) map[string]any {
	if result, ok := data["result"].(map[string]any); ok {
		return result
	}
	return data
}

func safeOrderID(payload string) string {
	replacer := strings.NewReplacer(":", "_", "-", "_", ".", "_", " ", "_")
	value := replacer.Replace(payload)
	if len(value) > 96 {
		value = value[:96]
	}
	return value
}

func newOrderID(prefix, payload string) string {
	suffix := strconv.FormatInt(time.Now().UnixNano(), 10)
	value := prefix + "_" + safeOrderID(payload) + "_" + suffix
	if len(value) > 120 {
		value = value[:100] + "_" + suffix
	}
	return value
}
