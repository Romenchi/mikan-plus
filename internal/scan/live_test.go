package scan

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestNeighborsLive scans a real server's network. It needs the network of that server:
// MIKAN_SCAN_IP=<server IPv4> [MIKAN_SCAN_SELF=127.0.0.1:<panel port>,<domain>] go test -run Live -v
func TestNeighborsLive(t *testing.T) {
	ip := os.Getenv("MIKAN_SCAN_IP")
	if ip == "" {
		t.Skip("MIKAN_SCAN_IP is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	start := time.Now()
	found, scanned, err := Neighbors(ctx, ip, 12, Options{Any: true})
	t.Logf("scanned %d addresses in %s, err=%v", scanned, time.Since(start).Round(time.Millisecond), err)
	for _, r := range found {
		t.Logf("  %-40s %-18s %4d ms dns=%v issuer=%s", r.SNI, r.Dest, r.RTTms, r.DNSMatch, r.Issuer)
	}
	for _, d := range []string{"www.microsoft.com:443", "dl.google.com:443"} {
		r := Check(ctx, d, "", Options{})
		t.Logf("check %s: ok=%v tls13=%v h2=%v x25519=%v cert=%v %d ms %s", d, r.OK, r.TLS13, r.H2, r.X25519, r.CertValid, r.RTTms, r.Error)
	}
	if self := os.Getenv("MIKAN_SCAN_SELF"); self != "" {
		dest, sni, _ := cut(self)
		r := Check(ctx, dest, sni, Options{LoopbackPort: 21355})
		t.Logf("self-steal %s as %s: ok=%v tls13=%v h2=%v x25519=%v cert=%v %s", dest, sni, r.OK, r.TLS13, r.H2, r.X25519, r.CertValid, r.Error)
	}
}

func cut(s string) (string, string, bool) {
	for i := 0; i < len(s); i++ {
		if s[i] == ',' {
			return s[:i], s[i+1:], true
		}
	}
	return s, "", false
}
