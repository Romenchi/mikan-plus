package scan

import (
	"context"
	"errors"
	"testing"
)

func TestResolveIPv4(t *testing.T) {
	ctx := context.Background()
	for host, want := range map[string]string{"203.0.113.7": "203.0.113.7", "::ffff:198.51.100.2": "198.51.100.2", "127.0.0.1": "127.0.0.1"} {
		if got, err := ResolveIPv4(ctx, host); err != nil || got != want {
			t.Errorf("ResolveIPv4(%q) = %q, %v; want %q", host, got, err, want)
		}
	}
	if _, err := ResolveIPv4(ctx, "2001:db8::1"); !errors.Is(err, ErrNotIPv4) {
		t.Errorf("an IPv6 address: %v", err)
	}
	if _, err := ResolveIPv4(ctx, ""); !errors.Is(err, ErrNoIPv4) {
		t.Errorf("no host at all: %v", err)
	}
}

func TestProblem(t *testing.T) {
	for _, c := range []struct {
		r    Result
		want string
	}{
		{Result{OK: true}, ""},
		{Result{Error: "timeout"}, "timeout"},
		{Result{}, "no_tls13"},
		{Result{TLS13: true}, "no_x25519"},
		{Result{TLS13: true, X25519: true}, "no_h2"},
		{Result{TLS13: true, X25519: true, H2: true}, "cert"},
		{Result{OK: true, Error: "stale"}, ""},
	} {
		if got := Problem(c.r); got != c.want {
			t.Errorf("Problem(%+v) = %q, want %q", c.r, got, c.want)
		}
	}
}
