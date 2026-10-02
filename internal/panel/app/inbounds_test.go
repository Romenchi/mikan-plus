package app

import (
	"context"
	"crypto/x509"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"mikan/internal/nodetls"
	"mikan/internal/panel/domain"
	"mikan/internal/panel/settings"
	"mikan/internal/panel/store/db"
)

// An inbound PATCH refused on any field changes nothing: the pool, the way out, the
// listen address and the automatic switches stay as they were with the port and the
// template, and the exit node gets no relay.
func TestRefusedInboundPatchChangesNothing(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	if err := domain.Seed(ctx, h.st, h.now); err != nil {
		t.Fatal(err)
	}
	if err := settings.Set(ctx, settings.New(h.st.Q), settings.KeyPanelPort, 21355); err != nil {
		t.Fatal(err)
	}
	panel, _ := nodetls.Generate("mikan-panel", x509.ExtKeyUsageClientAuth, time.Now())
	b, _, err := domain.AddNode(ctx, h.st, panel, domain.NodeInput{Name: "B", Host: "198.51.100.20", APIPort: 40000}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if resp, _ := h.login(password, ""); resp.StatusCode != http.StatusOK {
		t.Fatal("login")
	}
	csrf := map[string]string{"X-CSRF-Token": h.csrf}
	api := "/" + adminPath + "/api/v1"
	var pool struct {
		ID int64 `json:"id"`
	}
	if resp, body := h.do(http.MethodPost, api+"/pools", map[string]any{"name": "WL"}, csrf); resp.StatusCode != http.StatusCreated || json.Unmarshal(body, &pool) != nil {
		t.Fatalf("pool: %d %s", resp.StatusCode, body)
	}
	byName := map[string]db.Inbound{}
	ins, _ := h.st.Q.ListInbounds(ctx)
	for _, in := range ins {
		if in.NodeID == 1 {
			byName[in.Name] = in
		}
	}
	x, vision := byName["vless-xhttp"], byName["vless-vision"] // 443/tcp and 8443/tcp
	url := api + "/inbounds/" + strconv.FormatInt(x.ID, 10)

	for name, c := range map[string]struct {
		body   map[string]any
		status int
		code   string
	}{
		"busy port with a pool":            {map[string]any{"pool_id": pool.ID, "port": vision.Port}, http.StatusConflict, "port_in_use"},
		"busy port with an exit":           {map[string]any{"outbound": "node", "exit_node_id": b.ID, "port": vision.Port}, http.StatusConflict, "port_in_use"},
		"busy port with listen and auto":   {map[string]any{"listen": "127.0.0.1", "auto_sni": false, "port": vision.Port}, http.StatusConflict, "port_in_use"},
		"the panel's port with a pool":     {map[string]any{"pool_id": pool.ID, "port": "21355"}, http.StatusConflict, "port_panel"},
		"bad template with a pool":         {map[string]any{"pool_id": pool.ID, "config": "type: nope\n"}, http.StatusUnprocessableEntity, "invalid_config"},
		"bad target with an exit":          {map[string]any{"outbound": "node", "exit_node_id": b.ID, "dest": "nope"}, http.StatusUnprocessableEntity, "bad_dest"},
		"taken name with a pool":           {map[string]any{"pool_id": pool.ID, "display_name": domain.ProxyName(vision)}, http.StatusConflict, "name_in_use"},
		"unknown pool with a port":         {map[string]any{"pool_id": 999, "port": "2443"}, http.StatusUnprocessableEntity, "pool_not_found"},
		"itself as the exit with a port":   {map[string]any{"outbound": "node", "exit_node_id": 1, "port": "2443"}, http.StatusUnprocessableEntity, "exit_self"},
		"auto port on an address and pool": {map[string]any{"pool_id": pool.ID, "listen": "127.0.0.1", "auto_port": true}, http.StatusUnprocessableEntity, "auto_port_listen"},
	} {
		resp, body := h.do(http.MethodPatch, url, c.body, csrf)
		if resp.StatusCode != c.status || !strings.Contains(string(body), c.code) {
			t.Errorf("%s: %d %s", name, resp.StatusCode, body)
		}
		if after, err := h.st.Q.GetInbound(ctx, x.ID); err != nil || after != x {
			t.Errorf("%s changed the inbound:\n%+v\n%+v", name, x, after)
		}
		if _, err := h.st.Q.GetNodeRelay(ctx, b.ID); !errors.Is(err, sql.ErrNoRows) {
			t.Errorf("%s gave node B a relay: %v", name, err)
		}
	}

	// All of it together, with a free port, goes through.
	resp, body := h.do(http.MethodPatch, url, map[string]any{"pool_id": pool.ID, "outbound": "node", "exit_node_id": b.ID, "port": "2443", "auto_sni": false}, csrf)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("everything at once: %d %s", resp.StatusCode, body)
	}
	after, err := h.st.Q.GetInbound(ctx, x.ID)
	if err != nil || after.Port != "2443" || after.PoolID.Int64 != pool.ID || after.ExitNodeID.Int64 != b.ID || after.AutoSni != 0 {
		t.Fatalf("everything at once: %+v %v", after, err)
	}
	if _, err := h.st.Q.GetNodeRelay(ctx, b.ID); err != nil {
		t.Fatalf("node B's relay: %v", err)
	}
}
