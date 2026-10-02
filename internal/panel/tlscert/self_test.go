package tlscert

import (
	"crypto/tls"
	"sync"
	"testing"
	"time"
)

// A subscription fetch and a node's sync both ask for the self-signed pair at once. Only
// one certificate may be made, and everybody must see the same whole pair: a pin taken
// from a certificate that the next call overwrites would break the clients' links.
func TestSelfSignedConcurrentCallersShareOnePair(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	const callers = 24
	pins := make([]string, callers)
	var wg sync.WaitGroup
	for i := range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := LoadOrCreateSelfSigned(dir, "203.0.113.7", now); err != nil {
				t.Error(err)
				return
			}
			c, k, pin, err := PEM(dir)
			if err != nil {
				t.Error(err)
				return
			}
			if _, err := tls.X509KeyPair([]byte(c), []byte(k)); err != nil {
				t.Errorf("a torn pair: %v", err)
			}
			pins[i] = pin
		}()
	}
	wg.Wait()
	for i, p := range pins {
		if p == "" || p != pins[0] {
			t.Fatalf("caller %d saw pin %q, caller 0 saw %q: the pair was generated more than once", i, p, pins[0])
		}
	}
}

func TestSelfSignedIsRenewedBeforeItExpires(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	first, err := LoadOrCreateSelfSigned(dir, "example.com", now)
	if err != nil {
		t.Fatal(err)
	}
	again, err := LoadOrCreateSelfSigned(dir, "example.com", now.Add(24*time.Hour))
	if err != nil || string(again.Certificate[0]) != string(first.Certificate[0]) {
		t.Fatalf("a valid certificate is reused: %v", err)
	}
	renewed, err := LoadOrCreateSelfSigned(dir, "example.com", now.Add(800*24*time.Hour))
	if err != nil || string(renewed.Certificate[0]) == string(first.Certificate[0]) {
		t.Fatalf("a certificate in its last 30 days is replaced: %v", err)
	}
}
