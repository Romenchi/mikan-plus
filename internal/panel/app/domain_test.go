package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"mikan/internal/panel/dnscheck"
)

// The panel takes a domain only when public DNS leads it to this server: someone else's
// domain would go into every subscription.
func TestDomainMustLeadHere(t *testing.T) {
	records := map[string]string{"vpn.example.com": "203.0.113.10", "other.example.com": "198.51.100.7"}
	doh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ans := map[string]any{"Status": 3, "Answer": []any{}}
		if ip, ok := records[r.URL.Query().Get("name")]; ok {
			ans["Status"] = 0
			if r.URL.Query().Get("type") == "A" {
				ans["Answer"] = []any{map[string]any{"type": 1, "data": ip}}
			}
		}
		_ = json.NewEncoder(w).Encode(ans)
	}))
	t.Cleanup(doh.Close)
	h := newHarness(t, func(o *Options) { o.DNS = &dnscheck.Checker{Resolvers: []string{doh.URL}, HTTP: http.DefaultClient} })
	if resp, _ := h.login(password, ""); resp.StatusCode != http.StatusOK {
		t.Fatal("login")
	}
	csrf := map[string]string{"X-CSRF-Token": h.csrf}
	api := "/" + adminPath + "/api/v1/settings"
	if resp, body := h.do(http.MethodPatch, api, map[string]any{"public_host": "203.0.113.10"}, csrf); resp.StatusCode != http.StatusOK {
		t.Fatalf("public host: %d %s", resp.StatusCode, body)
	}
	for name, code := range map[string]string{"other.example.com": "domain_elsewhere", "nowhere.example.com": "domain_not_found"} {
		resp, body := h.do(http.MethodPatch, api, map[string]any{"domain": name}, csrf)
		if resp.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(string(body), code) {
			t.Fatalf("%s: %d %s", name, resp.StatusCode, body)
		}
	}
	if resp, body := h.do(http.MethodPatch, api, map[string]any{"domain": "vpn.example.com"}, csrf); resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"domain":"vpn.example.com"`) {
		t.Fatalf("own domain: %d %s", resp.StatusCode, body)
	}
	// Moving the server's address away from the domain is refused too; the domain itself
	// is not asked about again on every save.
	if resp, body := h.do(http.MethodPatch, api, map[string]any{"public_host": "203.0.113.99"}, csrf); resp.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(string(body), "domain_elsewhere") {
		t.Fatalf("address moved off the domain: %d %s", resp.StatusCode, body)
	}
	if resp, body := h.do(http.MethodPatch, api, map[string]any{"brand": "X", "domain": "vpn.example.com"}, csrf); resp.StatusCode != http.StatusOK {
		t.Fatalf("saving the same domain: %d %s", resp.StatusCode, body)
	}
	if resp, body := h.do(http.MethodPatch, api, map[string]any{"domain": ""}, csrf); resp.StatusCode != http.StatusOK {
		t.Fatalf("clearing the domain: %d %s", resp.StatusCode, body)
	}
}
