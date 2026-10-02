package cli

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mikan/internal/panel/domain"
	"mikan/internal/panel/settings"
	"mikan/internal/panel/store"
	"mikan/internal/panel/tlscert"
)

// `mikan cert set` pipes the chain and the key in one stream: the panel's certificate has
// to cover its address, a node's goes to that node, and show tells what is there.
func TestCertCommand(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	st, err := store.Open(ctx, dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := domain.Seed(ctx, st, time.Now()); err != nil {
		t.Fatal(err)
	}
	set := settings.New(st.Q)
	if err := settings.Set(ctx, set, settings.KeyDomain, "vpn.example.com"); err != nil {
		t.Fatal(err)
	}
	pair := func(name string) string {
		k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		tmpl := &x509.Certificate{SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: name}, DNSNames: []string{name},
			NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(90 * 24 * time.Hour)}
		der, _ := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &k.PublicKey, k)
		kd, _ := x509.MarshalPKCS8PrivateKey(k)
		return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})) + string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: kd}))
	}
	run := func(stdin string, args ...string) (string, error) {
		var out bytes.Buffer
		err := certCmd(ctx, st, set, dataDir, args, strings.NewReader(stdin), &out)
		return out.String(), err
	}

	if _, err := run(pair("other.org"), "set"); err == nil || !strings.Contains(err.Error(), "vpn.example.com") {
		t.Fatalf("another host: %v", err)
	}
	if _, err := run("not a certificate", "set"); err == nil || !strings.Contains(err.Error(), "fullchain.pem") {
		t.Fatalf("garbage: %v", err)
	}
	out, err := run(pair("vpn.example.com"), "set")
	if err != nil || !strings.Contains(out, "vpn.example.com") || !tlscert.HasCustom(filepath.Join(dataDir, "tls", "custom")) {
		t.Fatalf("panel: %q %v", out, err)
	}
	if out, err := run("", "show"); err != nil || !strings.Contains(out, "Own certificate of the panel: vpn.example.com") {
		t.Fatalf("show: %q %v", out, err)
	}
	// A node's certificate is for its own name, not the panel's.
	if out, err := run(pair("de.example.com"), "set", "--node", "1"); err != nil || !strings.Contains(out, "de.example.com") {
		t.Fatalf("node: %q %v", out, err)
	}
	if _, err := run(pair("de.example.com"), "set", "--node", "42"); err == nil {
		t.Fatal("unknown node accepted")
	}
	if out, err := run("", "clear"); err != nil || tlscert.HasCustom(filepath.Join(dataDir, "tls", "custom")) || !strings.Contains(out, "Removed") {
		t.Fatalf("clear: %q %v", out, err)
	}
	if out, _ := run("", "show"); !strings.Contains(out, "no own certificate") {
		t.Fatalf("show after clear: %q", out)
	}
}
