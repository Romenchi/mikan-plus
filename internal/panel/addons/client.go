package addons

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
)

// Settings are an adapter's settings as the admin filled them in; secrets included. They go
// with every call and the adapter keeps none of them.
type Settings map[string]any

// Field is one setting the adapter asks for.
type Field struct {
	Key      string `json:"key"`
	Label    Text   `json:"label"`
	Type     string `json:"type"` // string | bool
	Secret   bool   `json:"secret"`
	Required bool   `json:"required"`
	Pattern  string `json:"pattern,omitempty"`
}

// Info is an adapter's description of itself.
type Info struct {
	ID           string   `json:"id"`
	Protocol     int      `json:"protocol"`
	Version      string   `json:"version"`
	Name         Text     `json:"name"`
	Currencies   []string `json:"currencies"`
	Capabilities []string `json:"capabilities"`
	Settings     []Field  `json:"settings"`
	Help         Text     `json:"help"`
}

func (i Info) Can(capability string) bool { return slices.Contains(i.Capabilities, capability) }

func (i Info) Takes(currency string) bool { return slices.Contains(i.Currencies, currency) }

// Error is an adapter's refusal: bad_settings, bad_credentials, provider_unavailable,
// not_found, bad_request, or its own code.
type Error struct {
	Status  int    `json:"-"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return fmt.Sprintf("adapter %d %s: %s", e.Status, e.Code, e.Message) }

// ErrUnreachable: the adapter did not answer.
var ErrUnreachable = errors.New("addon_unreachable")

type Client struct {
	base, token string
	hc          *http.Client
}

func NewClient(base, token string, hc *http.Client) *Client {
	return &Client{base: strings.TrimRight(base, "/"), token: token, hc: hc}
}

func (c *Client) call(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		data, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnreachable, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnreachable, err)
	}
	if resp.StatusCode/100 != 2 {
		e := &Error{Status: resp.StatusCode}
		if json.Unmarshal(data, e) != nil || e.Code == "" {
			e.Code = "adapter_error"
		}
		return e
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("adapter %s: %w", path, err)
	}
	return nil
}

func (c *Client) Info(ctx context.Context) (Info, error) {
	var i Info
	err := c.call(ctx, http.MethodGet, "/v1/info", nil, &i)
	return i, err
}

// Check asks the provider whether the settings work.
func (c *Client) Check(ctx context.Context, s Settings) error {
	return c.call(ctx, http.MethodPost, "/v1/check", map[string]any{"settings": s}, nil)
}

type InvoiceRequest struct {
	Settings       Settings `json:"settings"`
	PaymentID      int64    `json:"payment_id"`
	IdempotencyKey string   `json:"idempotency_key"`
	Amount         int64    `json:"amount"`
	Currency       string   `json:"currency"`
	Description    string   `json:"description"`
	ReturnURL      string   `json:"return_url"`
	WebhookURL     string   `json:"webhook_url"`
}

type Invoice struct {
	ExternalID string `json:"external_id"`
	PayURL     string `json:"pay_url"`
}

func (c *Client) CreateInvoice(ctx context.Context, r InvoiceRequest) (Invoice, error) {
	var inv Invoice
	if err := c.call(ctx, http.MethodPost, "/v1/invoices", r, &inv); err != nil {
		return inv, err
	}
	if inv.ExternalID == "" || !strings.HasPrefix(inv.PayURL, "https://") {
		return inv, &Error{Code: "adapter_bad_invoice", Message: "no id or no https payment link"}
	}
	return inv, nil
}

// Status of an invoice at the provider: pending, paid or canceled, with what was paid.
type Status struct {
	Status   string `json:"status"`
	Amount   int64  `json:"amount"`
	Currency string `json:"currency"`
}

func (c *Client) Status(ctx context.Context, s Settings, externalID string) (Status, error) {
	var st Status
	err := c.call(ctx, http.MethodPost, "/v1/status", map[string]any{"settings": s, "external_id": externalID}, &st)
	return st, err
}

// Webhook hands the provider's request to the adapter, which says which invoice it is
// about ("" for one to ignore). The caller checks the invoice's status before trusting it.
func (c *Client) Webhook(ctx context.Context, s Settings, remoteIP string, headers http.Header, body []byte) (string, error) {
	var out struct {
		ExternalID string `json:"external_id"`
	}
	err := c.call(ctx, http.MethodPost, "/v1/webhook", map[string]any{"settings": s, "remote_ip": remoteIP, "headers": headers,
		"body": base64.StdEncoding.EncodeToString(body)}, &out)
	return out.ExternalID, err
}

func (c *Client) Refund(ctx context.Context, s Settings, externalID string, amount int64) error {
	return c.call(ctx, http.MethodPost, "/v1/refund", map[string]any{"settings": s, "external_id": externalID, "amount": amount}, nil)
}
