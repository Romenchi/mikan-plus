package billing

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"mikan/internal/panel/addons"
)

const adapterSecret = "sk_live_ADAPTERsecret0"

// fakeAdapter is a payment adapter of protocol v1 for a provider that signs its webhooks
// with a header.
type fakeAdapter struct {
	id       string // what /v1/info answers as; "fake" when empty
	mu       sync.Mutex
	status   map[string]addons.Status
	invoices []addons.InvoiceRequest
}

func (f *fakeAdapter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer adapter-token" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	var in struct {
		addons.InvoiceRequest
		ExternalID string              `json:"external_id"`
		Headers    map[string][]string `json:"headers"`
		Body       string              `json:"body"`
	}
	_ = json.NewDecoder(r.Body).Decode(&in)
	fail := func(status int, code string) {
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]string{"code": code, "message": "refused"})
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	switch r.URL.Path {
	case "/v1/info":
		id := f.id
		if id == "" {
			id = "fake"
		}
		_, _ = io.WriteString(w, `{"id":"`+id+`","protocol":1,"version":"1.0.0","name":{"en":"Fake"},"currencies":["RUB"],"capabilities":["webhook"],
			"settings":[{"key":"shop_id","type":"string","required":true,"pattern":"^[0-9]+$"},{"key":"secret_key","type":"string","secret":true,"required":true},{"key":"testnet","type":"bool"}]}`)
		return
	}
	if in.Settings["secret_key"] != adapterSecret {
		fail(http.StatusUnprocessableEntity, "bad_credentials")
		return
	}
	switch r.URL.Path {
	case "/v1/check":
		_, _ = io.WriteString(w, `{}`)
	case "/v1/invoices":
		f.invoices = append(f.invoices, in.InvoiceRequest)
		ext := "fk-" + strconv.FormatInt(in.PaymentID, 10)
		f.status[ext] = addons.Status{Status: "pending", Amount: in.Amount, Currency: in.Currency}
		_ = json.NewEncoder(w).Encode(addons.Invoice{ExternalID: ext, PayURL: "https://pay.example/" + ext})
	case "/v1/status":
		st, ok := f.status[in.ExternalID]
		if !ok {
			fail(http.StatusNotFound, "not_found")
			return
		}
		_ = json.NewEncoder(w).Encode(st)
	case "/v1/webhook":
		if len(in.Headers["X-Sig"]) != 1 || in.Headers["X-Sig"][0] != "ok" {
			fail(http.StatusBadRequest, "bad_request")
			return
		}
		body, _ := base64.StdEncoding.DecodeString(in.Body)
		var n struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(body, &n)
		_ = json.NewEncoder(w).Encode(map[string]string{"external_id": n.ID})
	}
}

func (f *fakeAdapter) set(ext string, fn func(*addons.Status)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	st := f.status[ext]
	fn(&st)
	f.status[ext] = st
}

// addonEnv is newEnv with the adapter "fake" installed and running.
func addonEnv(t *testing.T) (*env, *fakeAdapter) {
	e, fa, dir := adapterEnv(t, "fake")
	runAdapter(t, dir, "fake", fa)
	return e, fa
}

// adapterEnv is newEnv with the marketplace on a data directory where nothing runs yet,
// and an adapter answering as id for runAdapter to start.
func adapterEnv(t *testing.T, id string) (*env, *fakeAdapter, string) {
	e := newEnv(t)
	fa := &fakeAdapter{id: id, status: map[string]addons.Status{}}
	dir := t.TempDir()
	e.s.d.Addons = addons.New(dir, "", "0.4.3", slog.New(slog.NewTextHandler(io.Discard, nil)), func() time.Time { return e.now })
	e.s.d.SubBase = func(context.Context) string { return "https://panel.example:2053/sub" }
	return e, fa, dir
}

// runAdapter is the host having installed id: its state lists it running.
func runAdapter(t *testing.T, dir, id string, fa *fakeAdapter) {
	srv := httptest.NewServer(fa)
	t.Cleanup(srv.Close)
	state, _ := json.Marshal(addons.State{Adapters: map[string]addons.Installed{id: {Version: "1.0.0", Digest: "sha256:1", Status: "running",
		Listen: strings.TrimPrefix(srv.URL, "http://"), Token: "adapter-token"}}})
	must(t, os.MkdirAll(filepath.Join(dir, "addons"), 0o755))
	must(t, os.WriteFile(filepath.Join(dir, "addons", "state.json"), state, 0o600))
}

// The adapter's settings are checked against its own description and with the provider;
// the secret is write-only and kept when left empty.
func TestAddonSettings(t *testing.T) {
	e, _ := addonEnv(t)
	ctx := context.Background()
	if av := e.s.Available(ctx); len(av.Addons) != 0 {
		t.Fatalf("offered before it was set up: %v", av.Addons)
	}
	var se *AddonSettingError
	if _, err := e.s.SetAddonConfig(ctx, "fake", false, addons.Settings{"shop_id": "12"}); err != nil {
		t.Fatalf("half filled in, off: %v", err)
	}
	if _, err := e.s.SetAddonConfig(ctx, "fake", true, nil); !errors.As(err, &se) || se.Key != "secret_key" {
		t.Fatalf("on without the secret: %v", err)
	}
	if _, err := e.s.SetAddonConfig(ctx, "fake", true, addons.Settings{"shop_id": "12a", "secret_key": adapterSecret}); !errors.As(err, &se) || se.Key != "shop_id" {
		t.Fatalf("pattern: %v", err)
	}
	if _, err := e.s.SetAddonConfig(ctx, "fake", true, addons.Settings{"shop_id": "12", "testnet": "yes", "secret_key": adapterSecret}); !errors.As(err, &se) || se.Key != "testnet" {
		t.Fatalf("bool as a string: %v", err)
	}
	var ae *addons.Error
	if _, err := e.s.SetAddonConfig(ctx, "fake", true, addons.Settings{"shop_id": "12", "secret_key": "wrong"}); !errors.As(err, &ae) || ae.Code != "bad_credentials" {
		t.Fatalf("refused by the provider: %v", err)
	}
	if _, err := e.s.SetAddonConfig(ctx, "fake", true, addons.Settings{"shop_id": "12", "secret_key": adapterSecret, "extra": "x"}); err != nil {
		t.Fatal(err)
	}
	c, err := e.s.SetAddonConfig(ctx, "fake", true, addons.Settings{"shop_id": "34", "secret_key": ""})
	if err != nil || c.Values["secret_key"] != adapterSecret || c.Values["shop_id"] != "34" || c.Values["extra"] != nil {
		t.Fatalf("kept secret: %+v %v", c, err)
	}
	if av := e.s.Available(ctx); len(av.Addons) != 1 || av.Addons[0] != "fake" || !av.Rub() {
		t.Fatalf("available: %+v", av)
	}
	if _, err := e.s.SetAddonConfig(ctx, "ghost", true, nil); !errors.Is(err, addons.ErrNotInstalled) {
		t.Fatalf("not installed: %v", err)
	}
	// Switched off, it is not offered; its settings stay without a check.
	if _, err := e.s.SetAddonConfig(ctx, "fake", false, addons.Settings{}); err != nil {
		t.Fatal(err)
	}
	if av := e.s.Available(ctx); len(av.Addons) != 0 {
		t.Fatalf("off but offered: %v", av.Addons)
	}
	if _, err := e.s.Invoice(ctx, InvoiceRequest{TgID: 555, TariffID: e.sale.ID, Provider: "addon:fake"}); !errors.Is(err, ErrProviderOff) {
		t.Fatalf("invoice while off: %v", err)
	}
}

// A payment through an adapter: the webhook only makes the panel ask, the status the
// adapter reports must match the invoice, and the payment applies once.
func TestAddonPayment(t *testing.T) {
	e, fa := addonEnv(t)
	ctx := context.Background()
	if _, err := e.s.SetAddonConfig(ctx, "fake", true, addons.Settings{"shop_id": "12", "secret_key": adapterSecret}); err != nil {
		t.Fatal(err)
	}
	p := e.invoice(555, 0, "addon:fake")
	if p.Amount != 19900 || p.Currency != "RUB" || p.PayUrl != "https://pay.example/"+p.ExternalID.String {
		t.Fatalf("invoice: %+v", p)
	}
	if inv := fa.invoices[0]; inv.WebhookURL != "https://panel.example:2053/sub/pay/addon/fake/"+e.token || inv.IdempotencyKey != "mikan-"+strconv.FormatInt(p.ID, 10) ||
		inv.ReturnURL != "https://t.me/mikan_test_bot" {
		t.Fatalf("invoice request: %+v", inv)
	}
	if again := e.invoice(555, 0, "addon:fake"); again.ID != p.ID {
		t.Fatal("a second invoice for the same purchase")
	}
	note := []byte(`{"id":"` + p.ExternalID.String + `"}`)
	ok := map[string]string{"X-Sig": "ok"}
	if code := e.hook("addon/fake", "wrong-token-wrong-token-wrong-tok", "203.0.113.9", note, ok); code != http.StatusNotFound {
		t.Fatalf("wrong token: %d", code)
	}
	if code := e.hook("addon/../fake", e.token, "203.0.113.9", note, ok); code != http.StatusNotFound {
		t.Fatalf("bad id: %d", code)
	}
	if code := e.hook("addon/fake", e.token, "203.0.113.9", note, nil); code != http.StatusForbidden {
		t.Fatalf("forged: %d", code)
	}
	if code := e.hook("addon/fake", e.token, "203.0.113.9", note, ok); code != http.StatusOK || e.payment(p.ID).Status != "pending" {
		t.Fatalf("unconfirmed: %d %s", code, e.payment(p.ID).Status)
	}
	fa.set(p.ExternalID.String, func(s *addons.Status) { s.Status, s.Amount = "paid", 100 })
	e.hook("addon/fake", e.token, "203.0.113.9", note, ok)
	if e.payment(p.ID).Status != "pending" {
		t.Fatal("a smaller amount was accepted")
	}
	fa.set(p.ExternalID.String, func(s *addons.Status) { s.Amount = 19900 })
	before := e.users()
	for range 3 {
		if code := e.hook("addon/fake", e.token, "203.0.113.9", note, ok); code != http.StatusOK {
			t.Fatalf("webhook: %d", code)
		}
	}
	if e.payment(p.ID).Status != "applied" || e.users() != before+1 || e.tg.told() != 1 {
		t.Fatalf("applied: %s users %d→%d told %d", e.payment(p.ID).Status, before, e.users(), e.tg.told())
	}
	if code := e.hook("addon/fake", e.token, "203.0.113.9", []byte(`{"id":"fk-999"}`), ok); code != http.StatusOK {
		t.Fatalf("unknown invoice: %d", code)
	}

	// Without a webhook the reconcile pass finds it; canceled ones fail.
	q := e.invoice(556, 0, "addon:fake")
	c := e.invoice(557, 0, "addon:fake")
	fa.set(q.ExternalID.String, func(s *addons.Status) { s.Status = "paid" })
	fa.set(c.ExternalID.String, func(s *addons.Status) { s.Status = "canceled" })
	e.s.Reconcile(ctx)
	if e.payment(q.ID).Status != "applied" || e.payment(c.ID).Status != "failed" {
		t.Fatalf("reconcile: %s %s", e.payment(q.ID).Status, e.payment(c.ID).Status)
	}
	if strings.Contains(e.logs.String(), adapterSecret) {
		t.Fatal("the adapter's secret is in the log")
	}
}
