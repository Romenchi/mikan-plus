package app

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"mikan/internal/panel/domain"
	"mikan/internal/panel/store/db"
)

// A tariff and its pool limits are one change: a pool list the API refuses (a pool twice,
// an unknown pool) leaves the tariff and the limits it had as they were.
func TestTariffPoolsAreWrittenAtomically(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	if err := domain.Seed(ctx, h.st, h.now); err != nil {
		t.Fatal(err)
	}
	if resp, _ := h.login(password, ""); resp.StatusCode != http.StatusOK {
		t.Fatal("login")
	}
	csrf := map[string]string{"X-CSRF-Token": h.csrf}
	api := "/" + adminPath + "/api/v1"
	id := func(n int64) string { return strconv.FormatInt(n, 10) }
	var pool struct {
		ID int64 `json:"id"`
	}
	if resp, body := h.do(http.MethodPost, api+"/pools", map[string]any{"name": "WL"}, csrf); resp.StatusCode != http.StatusCreated || json.Unmarshal(body, &pool) != nil {
		t.Fatalf("create pool: %d %s", resp.StatusCode, body)
	}
	tariffs, _ := h.st.Q.ListTariffs(ctx)
	std := tariffs[1]
	put := func(name string, pools []map[string]any) (*http.Response, []byte) {
		return h.do(http.MethodPut, api+"/tariffs/"+id(std.ID), map[string]any{"name": name, "duration_days": 30, "reset_strategy": "period", "pools": pools}, csrf)
	}
	limits := func() []db.TariffPool {
		t.Helper()
		ps, err := h.st.Q.ListTariffPools(ctx, std.ID)
		if err != nil {
			t.Fatal(err)
		}
		return ps
	}
	if resp, body := put("Std", []map[string]any{{"pool_id": pool.ID, "traffic_limit": 1 << 30}}); resp.StatusCode != http.StatusOK {
		t.Fatalf("set limits: %d %s", resp.StatusCode, body)
	}

	// The same pool twice: refused, and the old limit is still there (it used to be wiped
	// and the request ended in a 500).
	resp, body := put("Renamed", []map[string]any{{"pool_id": pool.ID, "traffic_limit": 2 << 30}, {"pool_id": pool.ID, "traffic_limit": 3 << 30}})
	if resp.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(string(body), "pool_duplicate") {
		t.Fatalf("duplicate pool: %d %s", resp.StatusCode, body)
	}
	if ps := limits(); len(ps) != 1 || ps[0].TrafficLimit != 1<<30 {
		t.Fatalf("limits after a refused duplicate: %+v", ps)
	}
	// An unknown pool after a good one: nothing of the list is applied.
	resp, body = put("Renamed", []map[string]any{{"pool_id": pool.ID, "traffic_limit": 2 << 30}, {"pool_id": 999, "traffic_limit": 3 << 30}})
	if resp.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(string(body), "pool_not_found") {
		t.Fatalf("unknown pool: %d %s", resp.StatusCode, body)
	}
	if ps := limits(); len(ps) != 1 || ps[0].TrafficLimit != 1<<30 {
		t.Fatalf("limits after a refused list: %+v", ps)
	}
	if got, _ := h.st.Q.GetTariff(ctx, std.ID); got.Name != "Std" {
		t.Fatalf("the tariff was renamed by a refused request: %q", got.Name)
	}
	// A new tariff with a bad list is not created at all.
	resp, _ = h.do(http.MethodPost, api+"/tariffs", map[string]any{"name": "Ghost", "duration_days": 30, "reset_strategy": "none",
		"pools": []map[string]any{{"pool_id": 999, "traffic_limit": 1 << 30}}}, csrf)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("create with an unknown pool: %d", resp.StatusCode)
	}
	if after, _ := h.st.Q.ListTariffs(ctx); len(after) != len(tariffs) {
		t.Fatalf("tariffs: %d, want %d: the refused one was created", len(after), len(tariffs))
	}
	if resp, _ := h.do(http.MethodPut, api+"/tariffs/9999", map[string]any{"name": "x", "duration_days": 30, "reset_strategy": "none", "pools": []map[string]any{}}, csrf); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown tariff: %d", resp.StatusCode)
	}
	// A good list replaces the limits.
	if resp, body := put("Std", []map[string]any{{"pool_id": pool.ID, "traffic_limit": 5 << 30}}); resp.StatusCode != http.StatusOK {
		t.Fatalf("replace: %d %s", resp.StatusCode, body)
	}
	if ps := limits(); len(ps) != 1 || ps[0].TrafficLimit != 5<<30 {
		t.Fatalf("limits after a good list: %+v", ps)
	}
}
