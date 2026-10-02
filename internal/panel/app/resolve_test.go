package app

import (
	"context"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"testing"

	"mikan/internal/panel/domain"
	"mikan/internal/panel/settings"
)

// testResolve is the resolver of the tests: every name leads to a public address, except
// the ones a test uses to lead inside.
func testResolve(_ context.Context, host string) ([]netip.Addr, error) {
	switch host {
	case "127.0.0.1.nip.io":
		return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
	case "internal.attacker.example":
		return []netip.Addr{netip.MustParseAddr("10.0.0.7")}, nil
	case "metadata.attacker.example":
		return []netip.Addr{netip.MustParseAddr("169.254.169.254")}, nil
	}
	return []netip.Addr{netip.MustParseAddr("198.51.100.50")}, nil
}

// A DNS name passes the syntax check of a host, so a name that leads to this host or its
// network must be caught where it is looked up: when a REALITY target is saved (the node
// dials it past the rules that fence its users in) and when the panel checks one.
func TestNamesThatLeadInsideAreNotTargets(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	if err := domain.Seed(ctx, h.st, h.now); err != nil {
		t.Fatal(err)
	}
	if err := settings.Set(ctx, settings.New(h.st.Q), settings.KeyPanelPort, 21355); err != nil {
		t.Fatal(err)
	}
	if resp, _ := h.login(password, ""); resp.StatusCode != http.StatusOK {
		t.Fatal("login")
	}
	csrf := map[string]string{"X-CSRF-Token": h.csrf}
	api := "/" + adminPath + "/api/v1"
	ins, _ := h.st.Q.ListInbounds(ctx)
	var xhttp int64
	for _, in := range ins {
		if in.Name == "vless-xhttp" {
			xhttp = in.ID
		}
	}
	before, _ := h.st.Q.GetInbound(ctx, xhttp)

	for _, dest := range []string{"127.0.0.1.nip.io:8080", "internal.attacker.example:443", "metadata.attacker.example:80"} {
		// Saved: refused, nothing changes.
		resp, body := h.do(http.MethodPatch, api+"/inbounds/"+strconv.FormatInt(xhttp, 10), map[string]any{"dest": dest}, csrf)
		if resp.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(string(body), "reality_dest_private") {
			t.Errorf("PATCH dest %s: %d %s", dest, resp.StatusCode, body)
		}
		resp, body = h.do(http.MethodPost, api+"/inbounds", map[string]any{"preset": "vless_reality_xhttp", "port": "20443", "dest": dest}, csrf)
		if resp.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(string(body), "reality_dest_private") {
			t.Errorf("POST with dest %s: %d %s", dest, resp.StatusCode, body)
		}
		// Checked: refused before any connection is made.
		resp, body = h.do(http.MethodPost, api+"/inbounds/check-target", map[string]any{"dest": dest, "sni": "site.example"}, csrf)
		if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"error":"private"`) {
			t.Errorf("check-target %s: %d %s", dest, resp.StatusCode, body)
		}
	}
	if after, _ := h.st.Q.GetInbound(ctx, xhttp); after != before {
		t.Fatal("a refused target changed the inbound")
	}
	// A name that leads to a public address still goes through.
	if resp, body := h.do(http.MethodPatch, api+"/inbounds/"+strconv.FormatInt(xhttp, 10), map[string]any{"dest": "www.example.org:443"}, csrf); resp.StatusCode != http.StatusOK {
		t.Fatalf("a public name: %d %s", resp.StatusCode, body)
	}
}
