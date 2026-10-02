package nodesync

import (
	"context"
	"crypto/x509"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"mikan/internal/nodeapi"
	"mikan/internal/nodetls"
	"mikan/internal/panel/domain"
	"mikan/internal/panel/settings"
	"mikan/internal/panel/store/db"
)

// A chain client → A (local) → B → C → internet: A sends one inbound to B, B's relay
// goes on to C, C's relay leaves directly. Each hop gets only its own part.
func TestCascadeChain(t *testing.T) {
	a, _, st, _, _ := setup(t)
	ctx := context.Background()
	q := st.Q
	if err := settings.Set(ctx, settings.New(q), settings.KeyPublicHost, "203.0.113.10"); err != nil {
		t.Fatal(err)
	}
	panel, _ := nodetls.Generate("mikan-panel", x509.ExtKeyUsageClientAuth, time.Now())
	nb, _, err := domain.AddNode(ctx, st, panel, domain.NodeInput{Name: "B", Host: "198.51.100.20", APIPort: 40000}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	nc, _, err := domain.AddNode(ctx, st, panel, domain.NodeInput{Name: "C", Host: "198.51.100.30", APIPort: 40000}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	b := a.m.attach(t, nb.ID, Target{Node: &fakeNode{}, TLS: fakeTLS})
	c := a.m.attach(t, nc.ID, Target{Node: &fakeNode{}, TLS: fakeTLS})
	use := func(src, exit int64) {
		t.Helper()
		if err := domain.CheckExit(ctx, q, src, exit); err != nil {
			t.Fatal(err)
		}
		x, err := q.GetNode(ctx, exit)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := domain.EnsureRelay(ctx, q, x, time.Now()); err != nil {
			t.Fatal(err)
		}
		if _, err := domain.RelayUser(ctx, q, exit, src); err != nil {
			t.Fatal(err)
		}
	}
	ins, _ := q.ListInbounds(ctx)
	var mine db.Inbound
	for _, in := range ins {
		if in.NodeID == a.id {
			mine = in
			break
		}
	}
	use(a.id, nb.ID)
	if err := q.SetInboundExit(ctx, db.SetInboundExitParams{ExitNodeID: nullID(nb.ID), Outbound: "direct", ID: mine.ID}); err != nil {
		t.Fatal(err)
	}
	use(nb.ID, nc.ID)
	if err := q.SetNodeRelayRoute(ctx, db.SetNodeRelayRouteParams{Outbound: "direct", ExitNodeID: nullID(nc.ID), NodeID: nb.ID}); err != nil {
		t.Fatal(err)
	}
	// C sending its relay to B would loop B → C → B.
	if err := domain.CheckExit(ctx, q, nc.ID, nb.ID); !errors.Is(err, domain.ErrExitCycle) {
		t.Fatalf("cycle: %v", err)
	}
	if err := domain.CheckExit(ctx, q, a.id, a.id); !errors.Is(err, domain.ErrExitSelf) {
		t.Fatalf("self: %v", err)
	}

	da, _ := a.desired(ctx)
	if da.Relay != nil || len(da.Exits) != 1 || da.Exits[0].Name != nodeapi.ExitName(nb.ID) || len(da.Exits[0].Inbounds) != 1 || da.Exits[0].Inbounds[0] != mine.Name {
		t.Fatalf("A: relay %v exits %+v", da.Relay, da.Exits)
	}
	var proxy map[string]any
	_ = json.Unmarshal(da.Exits[0].Proxy, &proxy)
	if proxy["type"] != "vless" || proxy["server"] != "198.51.100.20" || proxy["reality-opts"] == nil {
		t.Fatalf("A's way to B: %v", proxy)
	}
	db_, _ := b.desired(ctx)
	if db_.Relay == nil || len(db_.Relay.Users) != 1 || db_.Relay.Users[0].Name != domain.RelayUserName(a.id) ||
		len(db_.Exits) != 1 || db_.Exits[0].Inbounds[0] != nodeapi.RelayListener {
		t.Fatalf("B: relay %+v exits %+v", db_.Relay, db_.Exits)
	}
	dc, _ := c.desired(ctx)
	if dc.Relay == nil || len(dc.Relay.Users) != 1 || dc.Relay.Users[0].Name != domain.RelayUserName(nb.ID) || len(dc.Exits) != 0 {
		t.Fatalf("C: relay %+v exits %+v", dc.Relay, dc.Exits)
	}
	// The keys differ per hop.
	if db_.Relay.Users[0].UUID == dc.Relay.Users[0].UUID {
		t.Fatal("one key for two hops")
	}

	// C switched off: B's relay still goes to "C", which cannot be reached — no leak.
	n, _ := q.GetNode(ctx, nc.ID)
	if _, err := q.UpdateNode(ctx, db.UpdateNodeParams{Name: n.Name, Address: n.Address, PublicHost: n.PublicHost, Domain: n.Domain, Enabled: 0, ID: n.ID}); err != nil {
		t.Fatal(err)
	}
	db_, _ = b.desired(ctx)
	_ = json.Unmarshal(db_.Exits[0].Proxy, &proxy)
	if len(db_.Exits) != 1 || proxy["server"] != "192.0.2.1" {
		t.Fatalf("an exit that is off must fail closed: %v", proxy)
	}
}

func nullID(id int64) sql.NullInt64 { return sql.NullInt64{Int64: id, Valid: true} }
