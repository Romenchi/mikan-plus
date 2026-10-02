package acme

import (
	"context"
	"encoding/pem"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"mikan/internal/panel/settings"
	"mikan/internal/panel/store"
	"mikan/internal/panel/tlscert"
)

// directory is an ACME directory that answers h, over TLS as lego insists, with its
// certificate trusted by lego's client.
func directory(t *testing.T, h http.HandlerFunc) string {
	t.Helper()
	srv := httptest.NewTLSServer(h)
	t.Cleanup(srv.Close)
	ca := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LEGO_CA_CERTIFICATES", ca)
	return srv.URL + "/directory"
}

// manager is a Manager for a domain whose Let's Encrypt is at dir.
func manager(t *testing.T, dir string) (*Manager, *tlscert.Holder) {
	t.Helper()
	ctx := context.Background()
	t.Setenv("MIKAN_ACME_DIRECTORY", dir)
	st, err := store.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	set := settings.New(st.Q)
	if err := settings.Set(ctx, set, settings.KeyDomain, "vpn.example.com"); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	fallback, err := tlscert.LoadOrCreateSelfSigned(t.TempDir(), "vpn.example.com", now)
	if err != nil {
		t.Fatal(err)
	}
	holder := &tlscert.Holder{}
	holder.Set(fallback)
	m := New(t.TempDir(), holder, fallback, set, slog.New(slog.NewTextHandler(io.Discard, nil)), time.Now)
	return m, holder
}

// An order for a certificate can take minutes when port 80 hangs. Uploading the admin's own
// certificate meanwhile is served at once, not after the order, and the order that then
// fails is not an error of the panel.
func TestCustomCertificateDoesNotWaitForAnOrder(t *testing.T) {
	release := make(chan struct{})
	var hits atomic.Int32
	dir := directory(t, func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		<-release
		w.WriteHeader(http.StatusInternalServerError)
	})
	m, _ := manager(t, dir)
	ctx := context.Background()

	ordered := make(chan bool, 1)
	go func() { ordered <- m.ensure(ctx) }()
	deadline := time.Now().Add(5 * time.Second)
	for hits.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the order did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}

	certPEM, keyPEM := ownCert(t, "vpn.example.com", time.Now().Add(90*24*time.Hour))
	done := make(chan error, 1)
	go func() { done <- m.SetCustom(ctx, certPEM, keyPEM) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("uploading a certificate waits for the order that is running")
	}
	if s := m.Status(); s.Kind != "custom" || s.Error != "" {
		t.Fatalf("the uploaded certificate is not served at once: %+v", s)
	}

	close(release) // the order fails now
	select {
	case failed := <-ordered:
		if failed {
			t.Fatal("an order that fails behind the admin's own certificate is no failure")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the order did not end")
	}
	if s := m.Status(); s.Kind != "custom" || s.Error != "" {
		t.Fatalf("the failed order left a mark on the custom certificate: %+v", s)
	}
}

// An order that failed is tried again within the half hour, not at the next six-hour round.
func TestFailedOrderIsRetriedSoon(t *testing.T) {
	var hits atomic.Int32
	dir := directory(t, func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	})
	m, _ := manager(t, dir)
	oldCheck, oldRetry := checkEvery, retryAfter
	checkEvery, retryAfter = time.Hour, 80*time.Millisecond
	t.Cleanup(func() { checkEvery, retryAfter = oldCheck, oldRetry })
	if retryAfter >= 6*time.Hour || oldRetry > time.Hour {
		t.Fatalf("the production retry is %s", oldRetry)
	}
	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() { m.Run(ctx); close(stopped) }()
	defer func() { cancel(); <-stopped }()
	deadline := time.Now().Add(8 * time.Second)
	for hits.Load() < 3 {
		if time.Now().After(deadline) {
			t.Fatalf("a failed order was tried %d times", hits.Load())
		}
		time.Sleep(20 * time.Millisecond)
	}
	// The error is put on the status once the order has failed, a moment after it was tried.
	shown := time.Now().Add(3 * time.Second)
	for m.Status().Error == "" {
		if time.Now().After(shown) {
			t.Fatalf("the failure is not shown: %+v", m.Status())
		}
		time.Sleep(10 * time.Millisecond)
	}
}
