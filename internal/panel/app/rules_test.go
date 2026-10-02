package app

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"mikan/internal/panel/domain"
	"mikan/internal/panel/settings"
)

// The admin's own Clash rules over the API: a bad line is refused with its number, a
// group a rule points at cannot be renamed away, and Clash apps get the rules their
// core can parse, ahead of the built-in routing.
func TestClashRulesOverHTTP(t *testing.T) {
	h := newHarness(t)
	ctx := t.Context()
	if err := domain.Seed(ctx, h.st, h.now); err != nil {
		t.Fatal(err)
	}
	for k, v := range map[string]any{settings.KeyPublicHost: "203.0.113.10", settings.KeyPanelPort: 21355} {
		if err := settings.Set(ctx, settings.New(h.st.Q), k, v); err != nil {
			t.Fatal(err)
		}
	}
	if resp, _ := h.login(password, ""); resp.StatusCode != http.StatusOK {
		t.Fatal("login")
	}
	api := "/" + adminPath + "/api/v1/settings"
	csrf := map[string]string{"X-CSRF-Token": h.csrf}

	resp, body := h.do(http.MethodPatch, api, map[string]any{"sub_rules": "# ok\nDOMAIN-SUFFIX,sber.ru,DIRECT\nDOMAIN,x.com,Nowhere"}, csrf)
	if resp.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(string(body), `"rule_target"`) || !strings.Contains(string(body), `"value":3`) {
		t.Fatalf("bad line: %d %s", resp.StatusCode, body)
	}

	rules := "# banks\nDOMAIN-SUFFIX,sber.ru,DIRECT\nDOMAIN-WILDCARD,*.ads.example.com,REJECT\nGEOSITE,youtube,VPN\n"
	resp, body = h.do(http.MethodPatch, api, map[string]any{"sub_rules": rules, "sub_group_main": "VPN"}, csrf)
	var v struct {
		SubRules    string   `json:"sub_rules"`
		RuleTargets []string `json:"rule_targets"`
	}
	if resp.StatusCode != http.StatusOK || json.Unmarshal(body, &v) != nil || v.SubRules != strings.TrimSpace(rules) || !strings.Contains(strings.Join(v.RuleTargets, ","), "PROXY") {
		t.Fatalf("rules: %d %s", resp.StatusCode, body)
	}
	// Renaming the group a rule sends traffic to would leave the rule pointing nowhere.
	resp, body = h.do(http.MethodPatch, api, map[string]any{"sub_group_main": "Мой VPN"}, csrf)
	if resp.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(string(body), "group_in_rules") || !strings.Contains(string(body), "sub_group_main") {
		t.Fatalf("rename: %d %s", resp.StatusCode, body)
	}

	clock := func() time.Time { return h.now }
	tariffs, _ := h.st.Q.ListTariffs(ctx)
	u, err := domain.NewUsers(h.st, domain.NewPool(h.st, clock), noChanges{}, clock).Create(ctx, domain.CreateInput{Name: "a", TariffID: tariffs[1].ID})
	if err != nil {
		t.Fatal(err)
	}
	profile := func(ua string) []string {
		t.Helper()
		resp, body := h.do(http.MethodGet, "/"+subPath+"/"+u.SubToken, nil, map[string]string{"User-Agent": ua})
		var cfg struct {
			Rules []string `json:"rules"`
		}
		if resp.StatusCode != http.StatusOK || json.Unmarshal(body, &cfg) != nil {
			t.Fatalf("%s: %d %s", ua, resp.StatusCode, body)
		}
		return cfg.Rules
	}
	fresh := strings.Join(profile("mihomo/1.19.31"), "\n")
	if !strings.Contains(fresh, "DOMAIN-SUFFIX,sber.ru,DIRECT") || !strings.Contains(fresh, "DOMAIN-WILDCARD,*.ads.example.com,REJECT") ||
		strings.Index(fresh, "GEOSITE,youtube,VPN") > strings.Index(fresh, "GEOSITE,category-ru,DIRECT") {
		t.Fatalf("fresh core: %s", fresh)
	}
	if old := strings.Join(profile("mihomo/1.19.11"), "\n"); strings.Contains(old, "DOMAIN-WILDCARD") || !strings.Contains(old, "DOMAIN-SUFFIX,sber.ru,DIRECT") {
		t.Fatalf("old core: %s", old)
	}
}
