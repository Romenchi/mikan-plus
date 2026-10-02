package app

import (
	"crypto/x509"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"mikan/internal/nodetls"
	"mikan/internal/panel/domain"
	"mikan/internal/panel/settings"
	"mikan/internal/panel/tlscert"
)

// A deleted node takes its certificates and private keys with it. Node ids are reused (the
// table has no AUTOINCREMENT): a node added later under the same id must not find them.
func TestNodeFilesGoWithTheNode(t *testing.T) {
	ctx := t.Context()
	dataDir := t.TempDir()
	nodeCerts := tlscert.NewNodeStore(filepath.Join(dataDir, "tls", "custom-nodes"), time.Now)
	panel, err := nodetls.Generate("mikan-panel", x509.ExtKeyUsageClientAuth, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	h := newHarness(t, func(o *Options) {
		o.DataDir, o.NodeCerts = dataDir, nodeCerts
		o.PanelCert = func() (nodetls.Pair, error) { return panel, nil }
	})
	if err := domain.Seed(ctx, h.st, h.now); err != nil {
		t.Fatal(err)
	}
	set := settings.New(h.st.Q)
	for k, v := range map[string]any{settings.KeyPublicHost: "203.0.113.10", settings.KeyPanelPort: 21355} {
		if err := settings.Set(ctx, set, k, v); err != nil {
			t.Fatal(err)
		}
	}
	if resp, _ := h.login(password, ""); resp.StatusCode != http.StatusOK {
		t.Fatal("login")
	}
	csrf := map[string]string{"X-CSRF-Token": h.csrf}
	api := "/" + adminPath + "/api/v1"

	// What a node leaves behind: its self-signed pair for QUIC and the admin's own certificate.
	leave := func(id int64) (selfDir, customDir string) {
		t.Helper()
		selfDir = filepath.Join(dataDir, "tls", "nodes", strconv.FormatInt(id, 10))
		if err := os.MkdirAll(selfDir, 0o700); err != nil {
			t.Fatal(err)
		}
		for _, f := range []string{"self.crt", "self.key"} {
			if err := os.WriteFile(filepath.Join(selfDir, f), []byte("x"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		certPEM, keyPEM := testCert(t, "node.example.com", time.Now().Add(90*24*time.Hour))
		if _, err := nodeCerts.Set(id, []byte(certPEM), []byte(keyPEM)); err != nil {
			t.Fatal(err)
		}
		return selfDir, filepath.Join(dataDir, "tls", "custom-nodes", strconv.FormatInt(id, 10))
	}
	exists := func(path string) bool {
		_, err := os.Stat(path)
		return err == nil
	}
	add := func(name, host string) int64 {
		t.Helper()
		resp, body := h.do(http.MethodPost, api+"/nodes", map[string]any{"name": name, "host": host, "api_port": 40000}, csrf)
		var out struct {
			Node struct {
				ID int64 `json:"id"`
			} `json:"node"`
		}
		if resp.StatusCode != http.StatusCreated || json.Unmarshal(body, &out) != nil {
			t.Fatalf("add %s: %d %s", name, resp.StatusCode, body)
		}
		return out.Node.ID
	}

	id := add("B", "198.51.100.20")
	selfDir, customDir := leave(id)
	if !tlscert.HasCustom(customDir) {
		t.Fatal("the test did not store the own certificate")
	}
	if resp, body := h.do(http.MethodDelete, api+"/nodes/"+strconv.FormatInt(id, 10), nil, csrf); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete: %d %s", resp.StatusCode, body)
	}
	if exists(selfDir) || tlscert.HasCustom(customDir) {
		t.Fatalf("the deleted node's files are still there: self %v own certificate %v", exists(selfDir), tlscert.HasCustom(customDir))
	}

	// Files an older panel left under an id (a delete before this fix) go when the id is
	// given to a new node.
	selfDir, customDir = leave(id)
	if again := add("C", "198.51.100.30"); again != id {
		t.Fatalf("expected the freed id %d to be reused, got %d", id, again)
	}
	if exists(selfDir) || tlscert.HasCustom(customDir) {
		t.Fatalf("a new node inherits the old files: self %v own certificate %v", exists(selfDir), tlscert.HasCustom(customDir))
	}
}
