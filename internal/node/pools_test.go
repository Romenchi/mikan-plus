package node

import (
	"testing"
	"time"

	"mikan/internal/nodeapi"
)

// A traffic pool has its own counter and quota: running it out closes only its own
// inbounds, the main quota does the same the other way round, and the batch reports
// both apart.
func TestTrafficPools(t *testing.T) {
	r := NewRegistry("e1", 0, time.Minute, time.Now)
	r.SetSlots([]nodeapi.Slot{{Name: "u1", UUID: "uuid-1"}})
	r.SetPools(map[string]string{"in-wl": "7"})
	r.SetPolicies("e1", []nodeapi.Policy{{Slot: "u1", Allowed: true, QuotaRemaining: 1000, Pools: []nodeapi.PoolQuota{{Pool: "7", Remaining: 500}}}})

	s, wl := r.admitIn("u1", "in-wl", "203.0.113.1", false)
	if s == nil || wl == nil {
		t.Fatal("the pool's inbound must be let in, into the pool")
	}
	if _, main := r.admitIn("u1", "in-de", "203.0.113.1", false); main != nil {
		t.Fatal("an inbound outside every pool counts to the main quota")
	}
	s.countIn(wl, 300, 300) // 600 > 500: the pool runs out
	if s2, _ := r.admitIn("u1", "in-wl", "203.0.113.1", false); s2 != nil {
		t.Fatal("an exhausted pool still lets its inbound in")
	}
	if s2, _ := r.admitIn("u1", "in-de", "203.0.113.1", false); s2 == nil {
		t.Fatal("the pool running out cut the main quota's inbounds too")
	}
	// The pool's bytes did not touch the main quota.
	if s.remaining.Load() != 1000 {
		t.Fatalf("main quota left %d, want 1000", s.remaining.Load())
	}
	s.countIn(nil, 1000, 0)
	if s2, _ := r.admitIn("u1", "in-de", "203.0.113.1", false); s2 != nil {
		t.Fatal("an exhausted main quota still lets its inbounds in")
	}

	c := r.Counters()
	if c.Slots["u1"] != (nodeapi.Traffic{Up: 1000}) || c.Pools["u1"]["7"] != (nodeapi.Traffic{Up: 300, Down: 300}) {
		t.Fatalf("batch: slots %v pools %v", c.Slots, c.Pools)
	}
	if !r.Ack(c.Epoch, c.Seq) {
		t.Fatal("ack")
	}
	// A new period: the panel sends fresh quotas, both open again.
	r.SetPolicies("e1", []nodeapi.Policy{{Slot: "u1", Allowed: true, QuotaRemaining: 1000, BaseSeq: c.Seq, Pools: []nodeapi.PoolQuota{{Pool: "7", Remaining: 500}}}})
	if s2, _ := r.admitIn("u1", "in-wl", "203.0.113.1", false); s2 == nil {
		t.Fatal("a renewed pool stays closed")
	}
	// A pool the policy does not name has no limit.
	r.SetPolicies("e1", []nodeapi.Policy{{Slot: "u1", Allowed: true, QuotaRemaining: -1, BaseSeq: c.Seq}})
	s.countIn(wl, 1<<40, 0)
	if s2, _ := r.admitIn("u1", "in-wl", "203.0.113.1", false); s2 == nil {
		t.Fatal("a pool without a quota was cut")
	}

	// The counters survive a restart, pools included.
	st := r.snapshot()
	r2 := NewRegistry("e1", 0, time.Minute, time.Now)
	r2.SetSlots([]nodeapi.Slot{{Name: "u1", UUID: "uuid-1"}})
	r2.restore(st)
	if c := r2.Counters(); c.Pools["u1"]["7"].Up != 1<<40 {
		t.Fatalf("pool counters after restart: %v", c.Pools)
	}
}
