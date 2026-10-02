package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"mikan/internal/panel/domain"
)

const warpConf = `[Interface]
PrivateKey = yAnz5TF+lXXJte14tji3zlMNq+hd2rYUIgJBgB3fBmk=
Address = 172.16.0.2/32, 2606:4700:110:8a36::2/128
MTU = 1280
[Peer]
PublicKey = bmXOC+F1FxEMF9dyiK2H5/1SUtzH0JuVo51h2wPfgyo=
Endpoint = 162.159.192.1:2408
`

// WARP over HTTP: a node gets an account by registration or import, the private key never
// comes back, lists are checked, an inbound picks its way out.
func TestWarpOverHTTP(t *testing.T) {
	cf := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/reg" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "d1", "token": "t1", "account": map[string]any{"warp_plus": false},
			"config": map[string]any{"client_id": base64.StdEncoding.EncodeToString([]byte{1, 2, 3}),
				"peers":     []any{map[string]any{"public_key": "bmXOC+F1FxEMF9dyiK2H5/1SUtzH0JuVo51h2wPfgyo=", "endpoint": map[string]any{"v4": "162.159.192.7:0"}}},
				"interface": map[string]any{"addresses": map[string]any{"v4": "172.16.0.2", "v6": "2606:4700:110:8a36::2"}}}})
	}))
	t.Cleanup(cf.Close)
	h := newHarness(t, func(o *Options) { o.WarpAPI = cf.URL })
	ctx := context.Background()
	if err := domain.Seed(ctx, h.st, h.now); err != nil {
		t.Fatal(err)
	}
	if resp, _ := h.login(password, ""); resp.StatusCode != http.StatusOK {
		t.Fatal("login")
	}
	csrf := map[string]string{"X-CSRF-Token": h.csrf}
	api := "/" + adminPath + "/api/v1"
	node := api + "/nodes/1/warp"

	if resp, body := h.do(http.MethodGet, node, nil, nil); resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"configured":false`) {
		t.Fatalf("no warp yet: %d %s", resp.StatusCode, body)
	}
	if resp, _ := h.do(http.MethodPatch, node, map[string]any{"enabled": true}, csrf); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("options without an account: %d", resp.StatusCode)
	}
	if resp, _ := h.do(http.MethodGet, api+"/nodes/99/warp", nil, nil); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown node: %d", resp.StatusCode)
	}
	for name, conf := range map[string]string{
		"garbage":           "hello",
		"injected endpoint": strings.Replace(warpConf, "162.159.192.1:2408", "x.com,DIRECT:2408", 1),
	} {
		if resp, body := h.do(http.MethodPost, node+"/import", map[string]any{"config": conf}, csrf); resp.StatusCode != http.StatusUnprocessableEntity {
			t.Fatalf("import %s: %d %s", name, resp.StatusCode, body)
		}
	}
	resp, body := h.do(http.MethodPost, node+"/import", map[string]any{"config": warpConf}, csrf)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"source":"import"`) || strings.Contains(string(body), "yAnz5TF") {
		t.Fatalf("import: %d %s", resp.StatusCode, body)
	}
	// Registration replaces the account; a malformed WARP+ key is refused first.
	if resp, body := h.do(http.MethodPost, node+"/register", map[string]any{"license": "nope"}, csrf); resp.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(string(body), "warp_license_format") {
		t.Fatalf("license format: %d %s", resp.StatusCode, body)
	}
	resp, body = h.do(http.MethodPost, node+"/register", map[string]any{}, csrf)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"endpoint":"162.159.192.7:2408"`) || !strings.Contains(string(body), `"source":"register"`) {
		t.Fatalf("register: %d %s", resp.StatusCode, body)
	}

	if resp, body := h.do(http.MethodPatch, node, map[string]any{"routes": []string{"openai.com", "bad_name"}}, csrf); resp.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(string(body), "bad_name") {
		t.Fatalf("bad route: %d %s", resp.StatusCode, body)
	}
	resp, body = h.do(http.MethodPatch, node, map[string]any{"routes": []string{"*.OpenAI.com", "104.16.0.0/13"}}, csrf)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"routes":["openai.com","104.16.0.0/13"]`) {
		t.Fatalf("routes: %d %s", resp.StatusCode, body)
	}

	ins, _ := h.st.Q.ListInbounds(ctx)
	id := strconv.FormatInt(ins[0].ID, 10)
	if resp, _ := h.do(http.MethodPatch, api+"/inbounds/"+id, map[string]any{"outbound": "tor"}, csrf); resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("unknown outbound: %d", resp.StatusCode)
	}
	before, _ := h.st.Q.GetInbound(ctx, ins[0].ID)
	resp, body = h.do(http.MethodPatch, api+"/inbounds/"+id, map[string]any{"outbound": "warp"}, csrf)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"outbound":"warp"`) {
		t.Fatalf("outbound: %d %s", resp.StatusCode, body)
	}
	// Clients get nothing new: the subscription stays fresh.
	if after, _ := h.st.Q.GetInbound(ctx, ins[0].ID); after.UpdatedAt != before.UpdatedAt {
		t.Fatal("the way out changed what clients get")
	}
	if _, body := h.do(http.MethodGet, node, nil, nil); !strings.Contains(string(body), `"inbounds":["`+ins[0].Name+`"]`) {
		t.Fatalf("warp inbounds: %s", body)
	}
	// WARP is not taken away from an inbound that goes out through it: it would leave
	// directly, from the node's own address.
	for name, do := range map[string]func() (*http.Response, []byte){
		"delete": func() (*http.Response, []byte) { return h.do(http.MethodDelete, node, nil, csrf) },
		"disable": func() (*http.Response, []byte) {
			return h.do(http.MethodPatch, node, map[string]any{"enabled": false}, csrf)
		},
	} {
		resp, body := do()
		if resp.StatusCode != http.StatusConflict || !strings.Contains(string(body), "warp_in_use") || !strings.Contains(string(body), ins[0].Name) {
			t.Fatalf("%s while an inbound uses WARP: %d %s", name, resp.StatusCode, body)
		}
	}
	if _, body := h.do(http.MethodGet, node, nil, nil); !strings.Contains(string(body), `"configured":true`) || !strings.Contains(string(body), `"enabled":true`) {
		t.Fatalf("a refused delete changed WARP: %s", body)
	}
	if resp, body := h.do(http.MethodPatch, api+"/inbounds/"+id, map[string]any{"outbound": "direct"}, csrf); resp.StatusCode != http.StatusOK {
		t.Fatalf("back to direct: %d %s", resp.StatusCode, body)
	}
	if resp, _ := h.do(http.MethodDelete, node, nil, csrf); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete: %d", resp.StatusCode)
	}
	if resp, _ := h.do(http.MethodDelete, node, nil, csrf); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("delete of nothing: %d", resp.StatusCode)
	}
	if _, body := h.do(http.MethodGet, node, nil, nil); !strings.Contains(string(body), `"configured":false`) {
		t.Fatalf("after delete: %s", body)
	}
}
