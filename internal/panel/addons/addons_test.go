package addons

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const digest = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func signed(t *testing.T, c any) ([]byte, string, ed25519.PublicKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(c)
	return data, base64.StdEncoding.EncodeToString(ed25519.Sign(priv, data)), pub
}

// The catalog is trusted only with a good signature, and only what this panel can run
// is offered.
func TestParseCatalog(t *testing.T) {
	c := Catalog{Version: 1, Adapters: []Entry{
		{ID: "yookassa", Protocol: 1, Image: "ghcr.io/getmikan/adapter-yookassa", Digest: digest, MinPanel: "0.4.3"},
		{ID: "future", Protocol: 2, Image: "x", Digest: digest},
		{ID: "newer", Protocol: 1, Image: "x", Digest: digest, MinPanel: "0.9.0"},
		{ID: "Bad Id", Protocol: 1, Image: "x", Digest: digest},
		{ID: "tagged", Protocol: 1, Image: "x", Digest: "latest"},
	}}
	data, sig, pub := signed(t, c)
	got, err := ParseCatalog(data, sig+"\n", pub, "0.4.3")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Adapters) != 1 || got.Adapters[0].ID != "yookassa" {
		t.Fatalf("adapters: %+v", got.Adapters)
	}
	tampered := []byte(strings.Replace(string(data), "yookassa", "yookassb", 1))
	if _, err := ParseCatalog(tampered, sig, pub, "0.4.3"); err == nil {
		t.Fatal("a changed catalog passed")
	}
	other, _, _ := ed25519.GenerateKey(rand.Reader)
	if _, err := ParseCatalog(data, sig, other, "0.4.3"); err == nil {
		t.Fatal("another key passed")
	}
	if got, _ := ParseCatalog(data, sig, pub, "0.4.2"); len(got.Adapters) != 0 {
		t.Fatal("an adapter for a newer panel offered to an older one")
	}
}

// fakeAdapter answers protocol v1 behind a token.
func fakeAdapter(t *testing.T, infos *atomic.Int64) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"code":"unauthorized","message":"no"}`)
			return
		}
		switch r.URL.Path {
		case "/v1/info":
			infos.Add(1)
			_, _ = io.WriteString(w, `{"id":"yookassa","protocol":1,"version":"1.0.0","name":{"ru":"ЮKassa"},"currencies":["RUB"],"capabilities":["webhook"],"settings":[{"key":"shop_id","type":"string","required":true}]}`)
		case "/v1/invoices":
			_, _ = io.WriteString(w, `{"external_id":"2f1c","pay_url":"https://pay.example/2f1c"}`)
		case "/v1/check":
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = io.WriteString(w, `{"code":"bad_credentials","message":"shop refused"}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestManagerAndClient(t *testing.T) {
	dir := t.TempDir()
	m := New(dir, "", "0.4.3", slog.New(slog.NewTextHandler(io.Discard, nil)), time.Now)
	if s, err := m.State(); err != nil || len(s.Adapters) != 0 {
		t.Fatalf("empty state: %+v %v", s, err)
	}
	if _, err := m.Client("yookassa"); !errors.Is(err, ErrNotInstalled) {
		t.Fatalf("client before install: %v", err)
	}
	if err := m.Ask("install", "../etc"); !errors.Is(err, ErrUnknown) {
		t.Fatalf("bad id: %v", err)
	}
	if err := m.Ask("install", "yookassa"); err != nil {
		t.Fatal(err)
	}
	if r, ok := m.Pending(); !ok || r.ID != "yookassa" || r.Action != "install" {
		t.Fatalf("pending: %+v %v", r, ok)
	}
	wake, err := os.ReadFile(filepath.Join(dir, "update", "request"))
	if err != nil || !strings.Contains(string(wake), `"do":"addons"`) {
		t.Fatalf("host not woken: %s %v", wake, err)
	}
	if err := m.Ask("remove", "yookassa"); !errors.Is(err, ErrBusy) {
		t.Fatalf("second request while one waits: %v", err)
	}
	// An update the admin asked for is not turned into an adapter request.
	other := t.TempDir()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.MkdirAll(filepath.Join(other, "update"), 0o755))
	must(os.WriteFile(filepath.Join(other, "update", "request"), []byte(`{"at":"2026-10-01T00:00:00Z"}`), 0o644))
	must(New(other, "", "0.4.3", m.log, time.Now).Ask("install", "yookassa"))
	if wake, _ := os.ReadFile(filepath.Join(other, "update", "request")); strings.Contains(string(wake), "addons") {
		t.Fatalf("update request replaced: %s", wake)
	}

	// The host ran it.
	var infos atomic.Int64
	srv := fakeAdapter(t, &infos)
	listen := strings.TrimPrefix(srv.URL, "http://")
	state := State{Adapters: map[string]Installed{"yookassa": {Version: "1.0.0", Digest: digest, Listen: listen, Token: "tok", Status: "running"}}}
	data, _ := json.Marshal(state)
	if err := os.WriteFile(filepath.Join(dir, "addons", "state.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for range 3 {
		info, err := m.Info(ctx, "yookassa")
		if err != nil || info.Name.In("en") != "ЮKassa" || !info.Takes("RUB") || !info.Can("webhook") || info.Can("refund") {
			t.Fatalf("info: %+v %v", info, err)
		}
	}
	if infos.Load() != 1 {
		t.Fatalf("info asked %d times, want once per image", infos.Load())
	}
	c, err := m.Client("yookassa")
	if err != nil {
		t.Fatal(err)
	}
	if inv, err := c.CreateInvoice(ctx, InvoiceRequest{Amount: 19900, Currency: "RUB"}); err != nil || inv.ExternalID != "2f1c" {
		t.Fatalf("invoice: %+v %v", inv, err)
	}
	var ae *Error
	if err := c.Check(ctx, Settings{"shop_id": "1"}); !errors.As(err, &ae) || ae.Code != "bad_credentials" || ae.Status != 422 {
		t.Fatalf("check: %v", err)
	}
	if err := NewClient(srv.URL, "wrong", http.DefaultClient).Check(ctx, nil); !errors.As(err, &ae) || ae.Status != 401 {
		t.Fatalf("wrong token: %v", err)
	}
	if _, err := NewClient("http://127.0.0.1:1", "tok", http.DefaultClient).Info(ctx); !errors.Is(err, ErrUnreachable) {
		t.Fatalf("down: %v", err)
	}
}
