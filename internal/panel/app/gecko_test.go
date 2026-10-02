package app

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"mikan/internal/panel/domain"
)

// Hysteria2 with Gecko over the admin API: the preset goes to mihomo apps alone, and a
// plain Hysteria2 switches its obfuscation both ways, keeping its password.
func TestGeckoOverHTTP(t *testing.T) {
	h := newHarness(t)
	if err := domain.Seed(t.Context(), h.st, h.now); err != nil {
		t.Fatal(err)
	}
	if resp, _ := h.login(password, ""); resp.StatusCode != http.StatusOK {
		t.Fatal("login")
	}
	api := "/" + adminPath + "/api/v1/inbounds"
	csrf := map[string]string{"X-CSRF-Token": h.csrf}
	type view struct {
		ID     int64    `json:"id"`
		Name   string   `json:"name"`
		Obfs   *string  `json:"obfs"`
		Apps   []string `json:"apps"`
		Config string   `json:"config"`
	}

	resp, body := h.do(http.MethodPost, api, map[string]any{"preset": "hysteria2_gecko"}, csrf)
	var g view
	if resp.StatusCode != http.StatusCreated || json.Unmarshal(body, &g) != nil || g.Obfs == nil || *g.Obfs != "gecko" ||
		len(g.Apps) != 1 || g.Apps[0] != "mihomo" || g.Name != "hysteria2-gecko" {
		t.Fatalf("gecko preset: %d %s", resp.StatusCode, body)
	}

	resp, body = h.do(http.MethodGet, api, nil, nil)
	var all []view
	if resp.StatusCode != http.StatusOK || json.Unmarshal(body, &all) != nil {
		t.Fatalf("list: %d %s", resp.StatusCode, body)
	}
	var hy view
	for _, v := range all {
		if v.Name == "hysteria2" {
			hy = v
		}
	}
	if hy.Obfs == nil || *hy.Obfs != "salamander" || len(hy.Apps) < 3 {
		t.Fatalf("plain hysteria2: %+v", hy)
	}
	path := api + "/" + strconv.FormatInt(hy.ID, 10)
	resp, body = h.do(http.MethodPatch, path, map[string]any{"obfs": "gecko"}, csrf)
	var sw view
	if resp.StatusCode != http.StatusOK || json.Unmarshal(body, &sw) != nil || *sw.Obfs != "gecko" || len(sw.Apps) != 1 {
		t.Fatalf("to gecko: %d %s", resp.StatusCode, body)
	}
	resp, body = h.do(http.MethodPatch, path, map[string]any{"obfs": "salamander"}, csrf)
	if resp.StatusCode != http.StatusOK || json.Unmarshal(body, &sw) != nil || *sw.Obfs != "salamander" || sw.Config != hy.Config {
		t.Fatalf("back to salamander, same template: %d %s", resp.StatusCode, body)
	}
	// Only Hysteria2 has it.
	for _, v := range all {
		if v.Name == "tuic" {
			resp, body := h.do(http.MethodPatch, api+"/"+strconv.FormatInt(v.ID, 10), map[string]any{"obfs": "gecko"}, csrf)
			if resp.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(string(body), "config_obfs") {
				t.Fatalf("tuic: %d %s", resp.StatusCode, body)
			}
		}
	}
}
