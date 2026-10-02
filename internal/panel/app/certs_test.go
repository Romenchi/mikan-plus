package app

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"strings"
	"testing"
	"time"

	"mikan/internal/panel/acme"
	"mikan/internal/panel/domain"
	"mikan/internal/panel/settings"
	"mikan/internal/panel/store"
	"mikan/internal/panel/tlscert"
)

func testCert(t *testing.T, name string, until time.Time) (string, string) {
	t.Helper()
	k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: name}, DNSNames: []string{name},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: until}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &k.PublicKey, k)
	if err != nil {
		t.Fatal(err)
	}
	kd, _ := x509.MarshalPKCS8PrivateKey(k)
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})), string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: kd}))
}

// Own certificates over the admin API (GitHub issue #9): refused with the field to fix,
// served once accepted, never handed back with the key, and out of an API key's reach.
func TestCertificatesOverHTTP(t *testing.T) {
	ctx := t.Context()
	// The certificate manager on a store of its own: no address, so any name fits.
	certStore, err := store.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer certStore.Close()
	holder := &tlscert.Holder{}
	certs := acme.New(t.TempDir(), holder, nil, settings.New(certStore.Q), slog.New(slog.NewTextHandler(io.Discard, nil)), time.Now)
	nodeCerts := tlscert.NewNodeStore(t.TempDir(), time.Now)
	h := newHarness(t, func(o *Options) { o.Certs, o.NodeCerts = certs, nodeCerts })
	if err := domain.Seed(ctx, h.st, h.now); err != nil {
		t.Fatal(err)
	}
	if resp, _ := h.login(password, ""); resp.StatusCode != http.StatusOK {
		t.Fatal("login")
	}
	api := "/" + adminPath + "/api/v1"
	csrf := map[string]string{"X-CSRF-Token": h.csrf}
	certPEM, keyPEM := testCert(t, "vpn.example.com", time.Now().Add(90*24*time.Hour))
	_, otherKey := testCert(t, "vpn.example.com", time.Now().Add(90*24*time.Hour))
	oldPEM, oldKey := testCert(t, "vpn.example.com", time.Now().Add(-time.Minute))

	for name, c := range map[string]struct {
		cert, key, field, code string
	}{
		"not PEM":       {"hello", keyPEM, "cert", "cert_pem_invalid"},
		"another's key": {certPEM, otherKey, "key", "cert_key_mismatch"},
		"no key":        {certPEM, certPEM, "key", "key_pem_invalid"},
		"expired":       {oldPEM, oldKey, "cert", "cert_expired"},
	} {
		resp, body := h.do(http.MethodPut, api+"/settings/certificate", map[string]any{"cert": c.cert, "key": c.key}, csrf)
		if resp.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(string(body), c.code) || !strings.Contains(string(body), `"body.`+c.field+`"`) {
			t.Errorf("%s: %d %s", name, resp.StatusCode, body)
		}
	}

	resp, body := h.do(http.MethodPut, api+"/settings/certificate", map[string]any{"cert": certPEM, "key": keyPEM}, csrf)
	var v struct {
		Certificate acme.Status `json:"certificate"`
	}
	if resp.StatusCode != http.StatusOK || json.Unmarshal(body, &v) != nil || v.Certificate.Kind != "custom" || strings.Contains(string(body), "PRIVATE KEY") {
		t.Fatalf("panel certificate: %d %s", resp.StatusCode, body)
	}
	if served, err := holder.Get(nil); err != nil || len(served.Certificate) != 1 {
		t.Fatalf("not served: %v", err)
	}

	// A node's own certificate shows on the node, without its key.
	resp, body = h.do(http.MethodPut, api+"/nodes/1/certificate", map[string]any{"cert": certPEM, "key": keyPEM}, csrf)
	var n struct {
		Certificate *struct {
			Names    []string `json:"names"`
			NotAfter string   `json:"not_after"`
		} `json:"certificate"`
	}
	if resp.StatusCode != http.StatusOK || json.Unmarshal(body, &n) != nil || n.Certificate == nil || n.Certificate.Names[0] != "vpn.example.com" || strings.Contains(string(body), "PRIVATE KEY") {
		t.Fatalf("node certificate: %d %s", resp.StatusCode, body)
	}
	if c, _, err := nodeCerts.Get(1, "vpn.example.com"); c == nil || err != nil {
		t.Fatalf("node store: %v", err)
	}
	if resp, body := h.do(http.MethodPut, api+"/nodes/999/certificate", map[string]any{"cert": certPEM, "key": keyPEM}, csrf); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown node: %d %s", resp.StatusCode, body)
	}

	// An API key cannot touch where the panel's identity comes from.
	resp, body = h.do(http.MethodPost, api+"/api-keys", map[string]any{"name": "ops", "scope": "full", "password": password}, csrf)
	var key struct {
		Key string `json:"key"`
	}
	if resp.StatusCode != http.StatusCreated || json.Unmarshal(body, &key) != nil {
		t.Fatalf("key: %d %s", resp.StatusCode, body)
	}
	bearer := map[string]string{"Authorization": "Bearer " + key.Key}
	for _, path := range []string{"/settings/certificate", "/nodes/1/certificate"} {
		if resp, _ := h.do(http.MethodPut, api+path, map[string]any{"cert": certPEM, "key": keyPEM}, bearer); resp.StatusCode != http.StatusForbidden {
			t.Errorf("api key on %s: %d", path, resp.StatusCode)
		}
	}

	if resp, _ := h.do(http.MethodDelete, api+"/nodes/1/certificate", nil, csrf); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("clear node: %d", resp.StatusCode)
	}
	if c, _, _ := nodeCerts.Get(1, ""); c != nil {
		t.Fatal("node certificate still there")
	}
	if resp, _ := h.do(http.MethodDelete, api+"/settings/certificate", nil, csrf); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("clear panel: %d", resp.StatusCode)
	}
	var leaked int
	if err := h.st.DB.QueryRow(`SELECT count(*) FROM audit_log WHERE details LIKE '%PRIVATE%'`).Scan(&leaked); err != nil || leaked != 0 {
		t.Fatalf("key in the audit log: %d %v", leaked, err)
	}
}
