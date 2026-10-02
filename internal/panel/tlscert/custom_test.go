package tlscert

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// issue makes a certificate for names, signed by itself, and its key in PEM.
func issue(t *testing.T, key crypto.Signer, names []string, from, until time.Time) (certPEM, keyPEM []byte) {
	t.Helper()
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: names[0]}, Issuer: pkix.Name{CommonName: "Test CA"},
		NotBefore: from, NotAfter: until, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	for _, n := range names {
		if ip := net.ParseIP(n); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		} else {
			tmpl.DNSNames = append(tmpl.DNSNames, n)
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, key.Public(), key)
	if err != nil {
		t.Fatal(err)
	}
	kd, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: kd})
}

func ecKey(t *testing.T) crypto.Signer {
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func TestParseCustom(t *testing.T) {
	now := time.Now()
	day := 24 * time.Hour
	good, goodKey := issue(t, ecKey(t), []string{"*.example.com", "203.0.113.5"}, now.Add(-day), now.Add(90*day))
	c, err := ParseCustom(good, goodKey, now)
	if err != nil {
		t.Fatal(err)
	}
	for host, want := range map[string]bool{"vpn.example.com": true, "203.0.113.5": true, "example.com": false, "a.b.example.com": false, "other.org": false, "": false} {
		if Covers(c.Leaf, host) != want {
			t.Errorf("covers %q: %v", host, !want)
		}
	}
	if info := Describe(c, "vpn.example.com", now); info.Trusted || info.Issuer != "*.example.com" || len(info.Names) != 2 {
		t.Fatalf("describe: %+v", info)
	}
	// A key and a certificate given in one stream are told apart (mikan cert set).
	both := append(append([]byte{}, good...), goodKey...)
	if _, err := ParseCustom(both, both, now); err != nil {
		t.Fatalf("one stream: %v", err)
	}
	// A traditional EC key and an Ed25519 one load too.
	ek, _ := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	ecDER, _ := x509.MarshalECPrivateKey(ek)
	ecCert, _ := issue(t, ek, []string{"a.example.com"}, now.Add(-day), now.Add(day))
	if _, err := ParseCustom(ecCert, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: ecDER}), now); err != nil {
		t.Fatalf("EC PRIVATE KEY: %v", err)
	}
	_, edKey, _ := ed25519.GenerateKey(rand.Reader)
	edCert, edPEM := issue(t, edKey, []string{"a.example.com"}, now.Add(-day), now.Add(day))
	if _, err := ParseCustom(edCert, edPEM, now); err != nil {
		t.Fatalf("ed25519: %v", err)
	}

	_, otherKey := issue(t, ecKey(t), []string{"x.example.com"}, now.Add(-day), now.Add(day))
	weak, _ := rsa.GenerateKey(rand.Reader, 1024)
	weakCert, weakKey := issue(t, weak, []string{"a.example.com"}, now.Add(-day), now.Add(day))
	expired, expiredKey := issue(t, ecKey(t), []string{"a.example.com"}, now.Add(-90*day), now.Add(-day))
	future, futureKey := issue(t, ecKey(t), []string{"a.example.com"}, now.Add(day), now.Add(90*day))
	for name, c := range map[string]struct {
		cert, key []byte
		want      error
	}{
		"no certificate": {goodKey, goodKey, ErrCertPEM},
		"garbage":        {[]byte("-----BEGIN CERTIFICATE-----\nAAAA\n-----END CERTIFICATE-----\n"), goodKey, ErrCertPEM},
		"no key":         {good, good, ErrKeyPEM},
		"empty":          {nil, nil, ErrCertPEM},
		"another's key":  {good, otherKey, ErrKeyMismatch},
		"RSA 1024":       {weakCert, weakKey, ErrKeyWeak},
		"expired":        {expired, expiredKey, ErrExpired},
		"not yet":        {future, futureKey, ErrNotYet},
		"too big":        {make([]byte, MaxPEM+1), goodKey, ErrCertPEM},
	} {
		if _, err := ParseCustom(c.cert, c.key, now); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", name, err, c.want)
		}
	}
}

// Kept as files only the panel reads; an expired one is told apart from none.
func TestCustomFiles(t *testing.T) {
	now := time.Now()
	dir := filepath.Join(t.TempDir(), "custom")
	if _, err := LoadCustom(dir, now); !errors.Is(err, os.ErrNotExist) || HasCustom(dir) || !CustomModTime(dir).IsZero() {
		t.Fatalf("none: %v", err)
	}
	certPEM, keyPEM := issue(t, ecKey(t), []string{"a.example.com"}, now.Add(-time.Hour), now.Add(48*time.Hour))
	c, err := ParseCustom(certPEM, keyPEM, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := SaveCustom(dir, c); err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(filepath.Join(dir, "custom.key")); err != nil || (st.Mode().Perm()&0o077 != 0 && os.PathSeparator == '/') {
		t.Fatalf("key file: %v %v", st.Mode(), err)
	}
	back, err := LoadCustom(dir, now)
	if err != nil || Pin(back) != Pin(c) {
		t.Fatalf("load: %v", err)
	}
	if _, err := LoadCustom(dir, now.Add(72*time.Hour)); !errors.Is(err, ErrExpired) {
		t.Fatalf("later: %v", err)
	}
	if err := RemoveCustom(dir); err != nil || HasCustom(dir) {
		t.Fatalf("remove: %v", err)
	}
	if err := RemoveCustom(dir); err != nil {
		t.Fatalf("remove twice: %v", err)
	}
}

// A node's certificate is read once per change of its files, and an expired one is
// reported rather than served.
func TestNodeStore(t *testing.T) {
	now := time.Now()
	clock := now
	s := NewNodeStore(t.TempDir(), func() time.Time { return clock })
	if c, _, err := s.Get(3, "n.example.com"); c != nil || err != nil {
		t.Fatalf("none: %v %v", c, err)
	}
	certPEM, keyPEM := issue(t, ecKey(t), []string{"n.example.com"}, now.Add(-time.Hour), now.Add(48*time.Hour))
	if _, err := s.Set(3, certPEM, []byte("junk")); !errors.Is(err, ErrKeyPEM) {
		t.Fatalf("bad key: %v", err)
	}
	if _, err := s.Set(3, certPEM, keyPEM); err != nil {
		t.Fatal(err)
	}
	a, trusted, err := s.Get(3, "n.example.com")
	if a == nil || trusted || err != nil {
		t.Fatalf("get: %v %v %v", a, trusted, err)
	}
	if b, _, _ := s.Get(3, "n.example.com"); b != a {
		t.Fatal("read again instead of cached")
	}
	if other, _, _ := s.Get(4, "n.example.com"); other != nil {
		t.Fatal("another node has it too")
	}
	clock = now.Add(72 * time.Hour)
	if c, _, err := s.Get(3, "n.example.com"); c != nil || !errors.Is(err, ErrExpired) {
		t.Fatalf("expired: %v %v", c, err)
	}
	if err := s.Clear(3); err != nil {
		t.Fatal(err)
	}
	if c, _, err := s.Get(3, "n.example.com"); c != nil || err != nil {
		t.Fatalf("cleared: %v %v", c, err)
	}
}
