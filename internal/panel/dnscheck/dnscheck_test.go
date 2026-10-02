package dnscheck

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"
)

// FakeDoH answers the JSON DNS-over-HTTPS API from a table: name → A and AAAA records.
func fakeDoH(t *testing.T, records map[string][]string) string {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name, typ := r.URL.Query().Get("name"), r.URL.Query().Get("type")
		ips, ok := records[name]
		ans := map[string]any{"Status": 0, "Answer": []any{}}
		if !ok {
			ans["Status"] = 3
		}
		list := []any{}
		for _, s := range ips {
			ip := netip.MustParseAddr(s)
			if ip.Is4() && typ == "A" {
				list = append(list, map[string]any{"type": 1, "data": s})
			}
			if ip.Is6() && typ == "AAAA" {
				list = append(list, map[string]any{"type": 28, "data": s})
			}
		}
		// A CNAME on the way is skipped, as real answers carry one.
		if len(list) > 0 {
			list = append([]any{map[string]any{"type": 5, "data": "edge.example."}}, list...)
		}
		ans["Answer"] = list
		_ = json.NewEncoder(w).Encode(ans)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestPointsTo(t *testing.T) {
	url := fakeDoH(t, map[string][]string{
		"vpn.example.com":   {"203.0.113.10"},
		"dual.example.com":  {"203.0.113.10", "2001:db8::10"},
		"other.example.com": {"198.51.100.7"},
		"mixed.example.com": {"203.0.113.10", "198.51.100.7"},
		"empty.example.com": {},
	})
	c := &Checker{Resolvers: []string{url}, HTTP: http.DefaultClient}
	own := []netip.Addr{netip.MustParseAddr("203.0.113.10"), netip.MustParseAddr("2001:db8::10")}
	ctx := context.Background()
	for _, ok := range []string{"vpn.example.com", "dual.example.com"} {
		if err := c.PointsTo(ctx, ok, own); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	var e *Elsewhere
	if err := c.PointsTo(ctx, "other.example.com", own); !errors.As(err, &e) || Join(e.Foreign) != "198.51.100.7" {
		t.Errorf("another server's domain: %v", err)
	}
	if err := c.PointsTo(ctx, "mixed.example.com", own); !errors.As(err, &e) || Join(e.Foreign) != "198.51.100.7" {
		t.Errorf("one address elsewhere: %v", err)
	}
	for _, name := range []string{"nowhere.example.com", "empty.example.com"} {
		if err := c.PointsTo(ctx, name, own); !errors.Is(err, ErrNotFound) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if err := c.PointsTo(ctx, "vpn.example.com", nil); !errors.Is(err, ErrUnknownServer) {
		t.Errorf("without the server's address: %v", err)
	}
	// The first resolver down, the next one answers.
	down := &Checker{Resolvers: []string{"http://127.0.0.1:1", url}, HTTP: http.DefaultClient}
	if err := down.PointsTo(ctx, "vpn.example.com", own); err != nil {
		t.Errorf("fallback to the next resolver: %v", err)
	}
}

func TestOwn(t *testing.T) {
	got := Own("203.0.113.10")
	if len(got) == 0 || got[0] != netip.MustParseAddr("203.0.113.10") {
		t.Fatalf("the public host comes first: %v", got)
	}
	for _, a := range Own("vpn.example.com") {
		if a.IsPrivate() || a.IsLoopback() {
			t.Fatalf("a private address counted as the server's: %v", a)
		}
	}
}

// Where a public resolver is blocked (it answers nothing), the others are asked at once:
// the lookup takes what the answering one takes, not the blocked one's timeout first.
func TestLookupDoesNotWaitForABlockedResolver(t *testing.T) {
	block := make(chan struct{})
	blocked := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-block:
		case <-r.Context().Done():
		}
	}))
	defer blocked.Close()
	defer close(block)
	good := fakeDoH(t, map[string][]string{"vpn.example.com": {"203.0.113.10"}})
	c := &Checker{Resolvers: []string{blocked.URL, good}, HTTP: http.DefaultClient}
	start := time.Now()
	ips, err := c.Lookup(context.Background(), "vpn.example.com")
	if err != nil || len(ips) != 1 || ips[0].String() != "203.0.113.10" {
		t.Fatalf("lookup: %v %v", ips, err)
	}
	if took := time.Since(start); took > time.Second {
		t.Fatalf("the lookup waited %v for the blocked resolver", took)
	}
}
