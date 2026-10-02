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
)

// Traffic packages over HTTP: the catalog with its checks, the admin's grant that brings
// a used-up user back, the grants list, and what the subscription page shows.
func TestTrafficPackagesOverHTTP(t *testing.T) {
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
	const gb = int64(1) << 30

	// Validation: the schema and the domain refuse bad input, field by field.
	ok := map[string]any{"name": "+50 GB", "bytes": 50 * gb, "lifetime": "used", "price_stars": 75, "on_sale": true}
	with := func(k string, v any) map[string]any {
		m := map[string]any{}
		for kk, vv := range ok {
			m[kk] = vv
		}
		if v == nil {
			delete(m, k)
		} else {
			m[k] = v
		}
		return m
	}
	for _, c := range []struct {
		body  map[string]any
		field string
	}{
		{with("bytes", gb-1), "body.bytes"},
		{with("bytes", 101*1024*gb), "body.bytes"},
		{with("lifetime", "forever"), "body.lifetime"},
		{with("lifetime", "days"), "body.days"},
		{with("days", 3651), "body.days"},
		{with("price_stars", nil), "body.on_sale"},
		{with("price_rub", 99), "body.price_rub"},
		{with("pool_id", 999), "body.pool_id"},
		{with("name", " "), "body.name"},
	} {
		resp, body := h.do(http.MethodPost, api+"/packages", c.body, csrf)
		if resp.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(string(body), c.field) {
			t.Errorf("%v: %d %s, want 422 at %s", c.body, resp.StatusCode, body, c.field)
		}
	}
	var pk struct {
		ID       int64  `json:"id"`
		Lifetime string `json:"lifetime"`
		Days     int64  `json:"days"`
	}
	resp, body := h.do(http.MethodPost, api+"/packages", ok, csrf)
	if resp.StatusCode != http.StatusCreated || json.Unmarshal(body, &pk) != nil {
		t.Fatalf("create: %d %s", resp.StatusCode, body)
	}
	resp, body = h.do(http.MethodPut, api+"/packages/"+id(pk.ID), with("lifetime", "days"), csrf)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("update without days: %d %s", resp.StatusCode, body)
	}
	upd := with("lifetime", "days")
	upd["days"] = 30
	resp, body = h.do(http.MethodPut, api+"/packages/"+id(pk.ID), upd, csrf)
	if resp.StatusCode != http.StatusOK || json.Unmarshal(body, &pk) != nil || pk.Lifetime != "days" || pk.Days != 30 {
		t.Fatalf("update: %d %s", resp.StatusCode, body)
	}
	if resp, _ := h.do(http.MethodPut, api+"/packages/999", ok, csrf); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("update unknown: %d", resp.StatusCode)
	}
	resp, body = h.do(http.MethodGet, api+"/packages", nil, nil)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"name":"+50 GB"`) {
		t.Fatalf("list: %d %s", resp.StatusCode, body)
	}
	if resp, _ := h.do(http.MethodDelete, api+"/packages/"+id(pk.ID), nil, csrf); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("archive: %d", resp.StatusCode)
	}
	if resp, _ := h.do(http.MethodDelete, api+"/packages/"+id(pk.ID), nil, csrf); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("archive twice: %d", resp.StatusCode)
	}
	if _, body := h.do(http.MethodGet, api+"/packages", nil, nil); string(body) != "[]\n" && string(body) != "[]" {
		t.Fatalf("archived package listed: %s", body)
	}

	// A user who used up the tariff's traffic gets 5 GB from the admin.
	tariffs, _ := h.st.Q.ListTariffs(ctx)
	clock := func() time.Time { return h.now }
	u, err := domain.NewUsers(h.st, domain.NewPool(h.st, clock), noChanges{}, clock).Create(ctx, domain.CreateInput{Name: "a", TariffID: tariffs[1].ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.st.DB.ExecContext(ctx, "UPDATE users SET used_down = traffic_limit + 1 WHERE id = ?", u.ID); err != nil {
		t.Fatal(err)
	}
	state := func() (string, int64) {
		t.Helper()
		var v struct {
			State        string `json:"state"`
			TrafficExtra int64  `json:"traffic_extra"`
		}
		resp, body := h.do(http.MethodGet, api+"/users/"+id(u.ID), nil, nil)
		if resp.StatusCode != http.StatusOK || json.Unmarshal(body, &v) != nil {
			t.Fatalf("user: %d %s", resp.StatusCode, body)
		}
		return v.State, v.TrafficExtra
	}
	if st, _ := state(); st != "limited" {
		t.Fatalf("setup: %s", st)
	}
	grants := api + "/users/" + id(u.ID) + "/grants"
	for _, bad := range []map[string]any{{"bytes": 5 * gb, "lifetime": "days"}, {"bytes": 1, "lifetime": "used"}, {"bytes": 5 * gb, "lifetime": "used", "pool_id": 999}} {
		if resp, body := h.do(http.MethodPost, grants, bad, csrf); resp.StatusCode != http.StatusUnprocessableEntity {
			t.Errorf("grant %v: %d %s", bad, resp.StatusCode, body)
		}
	}
	if resp, _ := h.do(http.MethodPost, api+"/users/999/grants", map[string]any{"bytes": 5 * gb, "lifetime": "used"}, csrf); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("grant to nobody: %d", resp.StatusCode)
	}
	resp, body = h.do(http.MethodPost, grants, map[string]any{"bytes": 5 * gb, "lifetime": "used", "note": "компенсация"}, csrf)
	if resp.StatusCode != http.StatusCreated || !strings.Contains(string(body), `"source":"admin"`) || !strings.Contains(string(body), `"note":"компенсация"`) {
		t.Fatalf("grant: %d %s", resp.StatusCode, body)
	}
	if st, extra := state(); st != "active" || extra != 5*gb {
		t.Fatalf("after the grant: %s, extra %d", st, extra)
	}
	resp, body = h.do(http.MethodGet, grants, nil, nil)
	var list []struct {
		Bytes     int64 `json:"bytes"`
		Remaining int64 `json:"remaining"`
		Active    bool  `json:"active"`
	}
	if resp.StatusCode != http.StatusOK || json.Unmarshal(body, &list) != nil || len(list) != 1 || !list[0].Active || list[0].Remaining != 5*gb {
		t.Fatalf("grants: %d %s", resp.StatusCode, body)
	}
	resp, body = h.do(http.MethodGet, "/"+subPath+"/"+u.SubToken+"/info", nil, nil)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"state":"active"`) || !strings.Contains(string(body), `"extra":`+strconv.FormatInt(5*gb, 10)) {
		t.Fatalf("page info: %d %s", resp.StatusCode, body)
	}
}
