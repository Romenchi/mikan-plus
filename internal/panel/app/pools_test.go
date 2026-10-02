package app

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"mikan/internal/panel/domain"
	"mikan/internal/panel/settings"
)

// Traffic pools over HTTP: make a pool, put an inbound in it, give the tariff a pool
// limit; the user gets it, the admin may change it, and a used-up pool leaves the
// subscription while the rest stays.
func TestTrafficPoolsOverHTTP(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
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
	csrf := map[string]string{"X-CSRF-Token": h.csrf}
	api := "/" + adminPath + "/api/v1"
	id := func(n int64) string { return strconv.FormatInt(n, 10) }

	if resp, _ := h.do(http.MethodPost, api+"/pools", map[string]any{"name": " "}, csrf); resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("blank name: %d", resp.StatusCode)
	}
	var pool struct {
		ID int64 `json:"id"`
	}
	resp, body := h.do(http.MethodPost, api+"/pools", map[string]any{"name": "WL"}, csrf)
	if resp.StatusCode != http.StatusCreated || json.Unmarshal(body, &pool) != nil {
		t.Fatalf("create: %d %s", resp.StatusCode, body)
	}
	if resp, _ := h.do(http.MethodPost, api+"/pools", map[string]any{"name": "WL"}, csrf); resp.StatusCode != http.StatusConflict {
		t.Fatalf("same name: %d", resp.StatusCode)
	}
	ins, _ := h.st.Q.ListInbounds(ctx)
	wl := ins[0]
	if resp, _ := h.do(http.MethodPatch, api+"/inbounds/"+id(wl.ID), map[string]any{"pool_id": 999}, csrf); resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("unknown pool: %d", resp.StatusCode)
	}
	resp, body = h.do(http.MethodPatch, api+"/inbounds/"+id(wl.ID), map[string]any{"pool_id": pool.ID}, csrf)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"pool_id":`+id(pool.ID)) {
		t.Fatalf("inbound into the pool: %d %s", resp.StatusCode, body)
	}
	if after, _ := h.st.Q.GetInbound(ctx, wl.ID); after.UpdatedAt != wl.UpdatedAt {
		t.Fatal("a pool changes nothing clients get")
	}

	tariffs, _ := h.st.Q.ListTariffs(ctx)
	std := tariffs[1]
	const gb = 1 << 30
	resp, body = h.do(http.MethodPut, api+"/tariffs/"+id(std.ID), map[string]any{"name": std.Name, "duration_days": 30, "reset_strategy": "period",
		"pools": []map[string]any{{"pool_id": pool.ID, "traffic_limit": 100 * gb}}}, csrf)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"pools":[{"pool_id":`+id(pool.ID)) {
		t.Fatalf("tariff pools: %d %s", resp.StatusCode, body)
	}
	clock := func() time.Time { return h.now }
	u, err := domain.NewUsers(h.st, domain.NewPool(h.st, clock), noChanges{}, clock).Create(ctx, domain.CreateInput{Name: "a", TariffID: std.ID})
	if err != nil {
		t.Fatal(err)
	}
	var ups []struct {
		PoolID       int64  `json:"pool_id"`
		TrafficLimit *int64 `json:"traffic_limit"`
		Exhausted    bool   `json:"exhausted"`
	}
	resp, body = h.do(http.MethodGet, api+"/users/"+id(u.ID)+"/pools", nil, nil)
	if resp.StatusCode != http.StatusOK || json.Unmarshal(body, &ups) != nil || len(ups) != 1 || ups[0].TrafficLimit == nil || *ups[0].TrafficLimit != 100*gb {
		t.Fatalf("user pools: %d %s", resp.StatusCode, body)
	}
	// The admin gives this user 1 KB of WL; it is soon used up.
	resp, body = h.do(http.MethodPut, api+"/users/"+id(u.ID)+"/pools", map[string]any{"pools": []map[string]any{{"pool_id": pool.ID, "traffic_limit": 1024}}}, csrf)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"traffic_limit":1024`) {
		t.Fatalf("set user pools: %d %s", resp.StatusCode, body)
	}
	countLinks := func() int {
		t.Helper()
		resp, body := h.do(http.MethodGet, "/"+subPath+"/"+u.SubToken, nil, map[string]string{"User-Agent": "mihomo/1.19.31"})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("subscription: %d", resp.StatusCode)
		}
		var prof struct {
			Proxies []any `json:"proxies"`
		}
		_ = json.Unmarshal(body, &prof)
		return len(prof.Proxies)
	}
	before := countLinks()
	if _, err := h.st.DB.ExecContext(ctx, "UPDATE user_pools SET used_down = 2048 WHERE user_id = ?", u.ID); err != nil {
		t.Fatal(err)
	}

	// The subscription drops the used-up pool's inbound, keeps the others, and shows the pool.
	if after := countLinks(); before == 0 || after != before-1 {
		t.Fatalf("proxies before %d, after the pool ran out %d: want one less", before, after)
	}
	resp, body = h.do(http.MethodGet, "/"+subPath+"/"+u.SubToken+"/info", nil, nil)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"pools":[{"name":"WL","limit":1024,"used":2048}]`) {
		t.Fatalf("page info: %d %s", resp.StatusCode, body)
	}
	resp, body = h.do(http.MethodGet, api+"/users/"+id(u.ID)+"/pools", nil, nil)
	if !strings.Contains(string(body), `"exhausted":true`) {
		t.Fatalf("exhausted flag: %s", body)
	}

	// Deleting the pool puts its inbound back into the main traffic.
	if resp, _ := h.do(http.MethodDelete, api+"/pools/"+id(pool.ID), nil, csrf); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete: %d", resp.StatusCode)
	}
	if in, _ := h.st.Q.GetInbound(ctx, wl.ID); in.PoolID.Valid {
		t.Fatal("the inbound still points at a deleted pool")
	}
}
