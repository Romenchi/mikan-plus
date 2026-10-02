package nodesync

import (
	"context"
	"database/sql"
	"strconv"
	"testing"

	"mikan/internal/nodeapi"
	"mikan/internal/panel/domain"
	"mikan/internal/panel/store/db"
)

// A traffic pool end to end on the panel's side: the user gets the tariff's pool limit,
// the node is told which inbound counts where and what is left, pool bytes go to the
// pool and the all-time totals but not to the main quota, and a main reset resets pools.
func TestTrafficPoolsOnThePanel(t *testing.T) {
	s, node, st, users, _ := setup(t)
	ctx := context.Background()
	q := st.Q
	pool, err := q.CreateTrafficPool(ctx, db.CreateTrafficPoolParams{Name: "WL", CreatedAt: 1})
	if err != nil {
		t.Fatal(err)
	}
	ins, _ := q.ListInbounds(ctx)
	wl := ins[0]
	if err := q.SetInboundPool(ctx, db.SetInboundPoolParams{PoolID: sql.NullInt64{Int64: pool.ID, Valid: true}, ID: wl.ID}); err != nil {
		t.Fatal(err)
	}
	tariffs, _ := q.ListTariffs(ctx)
	const limit = 1000
	if err := q.AddTariffPool(ctx, db.AddTariffPoolParams{TariffID: tariffs[1].ID, PoolID: pool.ID, TrafficLimit: limit}); err != nil {
		t.Fatal(err)
	}
	u, err := users.Create(ctx, domain.CreateInput{Name: "a", TariffID: tariffs[1].ID})
	if err != nil {
		t.Fatal(err)
	}
	ups, _ := q.ListUserPools(ctx, u.ID)
	if len(ups) != 1 || !ups[0].TrafficLimit.Valid || ups[0].TrafficLimit.Int64 != limit {
		t.Fatalf("the tariff's pool limit: %+v", ups)
	}

	key := strconv.FormatInt(pool.ID, 10)
	d, _ := s.desired(ctx)
	for _, in := range d.Inbounds {
		if (in.Name == wl.Name) != (in.Pool == key) {
			t.Fatalf("inbound %s pool %q", in.Name, in.Pool)
		}
	}
	slot, _ := q.GetSlot(ctx, u.SlotID.Int64)
	policy := func() nodeapi.Policy {
		t.Helper()
		_, ps, _, err := s.policies(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range ps {
			if p.Slot == slot.Name {
				return p
			}
		}
		t.Fatal("no policy")
		return nodeapi.Policy{}
	}
	if p := policy(); len(p.Pools) != 1 || p.Pools[0] != (nodeapi.PoolQuota{Pool: key, Remaining: limit}) {
		t.Fatalf("pool quota: %+v", p.Pools)
	}

	node.batch = nodeapi.Counters{Epoch: "e1", Seq: 1, Slots: map[string]nodeapi.Traffic{slot.Name: {Down: 70}},
		Pools: map[string]map[string]nodeapi.Traffic{slot.Name: {key: {Up: 100, Down: 900}, "999": {Down: 5}}}}
	s.pullCounters(ctx)
	s.pullCounters(ctx) // the same batch again: counted once
	got, _ := q.GetUser(ctx, u.ID)
	ups, _ = q.ListUserPools(ctx, u.ID)
	if got.UsedDown != 70 || got.UsedUp != 0 || got.TotalDown != 970 || got.TotalUp != 100 || ups[0].UsedUp != 100 || ups[0].UsedDown != 900 {
		t.Fatalf("main %d/%d total %d/%d pool %d/%d", got.UsedUp, got.UsedDown, got.TotalUp, got.TotalDown, ups[0].UsedUp, ups[0].UsedDown)
	}
	// The pool is used up: the node gets 0 left, the user may still get in elsewhere.
	if p := policy(); p.Pools[0].Remaining != 0 || !p.Allowed {
		t.Fatalf("after the pool ran out: %+v", p)
	}
	// The main quota running out keeps the user in for the pool.
	_, err = st.DB.ExecContext(ctx, "UPDATE users SET traffic_limit = 50 WHERE id = ?", u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if p := policy(); !p.Allowed || p.QuotaRemaining != 0 {
		t.Fatalf("main quota out: %+v", p)
	}
	// A reset of the main traffic resets the pools.
	if _, err := users.ResetTraffic(ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	ups, _ = q.ListUserPools(ctx, u.ID)
	if ups[0].UsedUp+ups[0].UsedDown != 0 {
		t.Fatalf("pool after the reset: %+v", ups[0])
	}
}
