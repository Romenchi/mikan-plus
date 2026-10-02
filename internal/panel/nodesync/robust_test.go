package nodesync

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	"mikan/internal/nodeapi"
	"mikan/internal/panel/domain"
	"mikan/internal/panel/store/db"
	"mikan/internal/proto"
)

// flaky is a node that can be made to fail.
type flaky struct {
	*fakeNode
	applyErr, countersErr, policiesErr error
	applyCalls                         int
}

func (f *flaky) Apply(ctx context.Context, s nodeapi.DesiredState) (nodeapi.ApplyResult, error) {
	f.applyCalls++
	if f.applyErr != nil {
		return nodeapi.ApplyResult{}, f.applyErr
	}
	return f.fakeNode.Apply(ctx, s)
}

func (f *flaky) Counters(ctx context.Context) (nodeapi.Counters, error) {
	if f.countersErr != nil {
		return nodeapi.Counters{}, f.countersErr
	}
	return f.fakeNode.Counters(ctx)
}

func (f *flaky) SetPolicies(ctx context.Context, e string, p []nodeapi.Policy) error {
	if f.policiesErr != nil {
		return f.policiesErr
	}
	return f.fakeNode.SetPolicies(ctx, e, p)
}

func revision(t *testing.T, s *Syncer) string {
	t.Helper()
	v, err := s.m.st.Q.GetNodeState(context.Background(), stateKeyOf("revision", s.id))
	if errors.Is(err, sql.ErrNoRows) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return v
}

var errRefused = &nodeapi.Error{Code: "invalid_state", Message: "no"}

// A node that refuses a state is not asked again at once, and the failed tries do not
// each use a revision or write a line to the log.
func TestFailingApplyBacksOffAndKeepsRevisions(t *testing.T) {
	s, fake, _, _, now := setup(t)
	ctx := context.Background()
	n := &flaky{fakeNode: fake, applyErr: errRefused}
	s.node = n

	for range 5 {
		s.applyState(ctx)
	}
	if n.applyCalls != 1 {
		t.Fatalf("a refused state was sent %d times at once", n.applyCalls)
	}
	if rev := revision(t, s); rev != "" {
		t.Fatalf("a refused state used revision %s", rev)
	}
	*now = now.Add(6 * time.Second)
	s.applyState(ctx)
	if n.applyCalls != 2 {
		t.Fatalf("after the pause the node is tried again: %d calls", n.applyCalls)
	}
	// The pause doubles.
	*now = now.Add(6 * time.Second)
	s.applyState(ctx)
	if n.applyCalls != 2 {
		t.Fatalf("the second pause is longer than the first: %d calls", n.applyCalls)
	}

	n.applyErr = nil
	*now = now.Add(time.Minute)
	s.applyState(ctx)
	if len(fake.applied) != 1 || fake.applied[0].Revision != 1 || revision(t, s) != "1" {
		t.Fatalf("revision 1 goes to the node and is kept once it took it: %+v %q", fake.applied, revision(t, s))
	}
	s.mu.Lock()
	left := s.retry.n
	s.mu.Unlock()
	if left != 0 {
		t.Fatalf("a success ends the backoff: %d", left)
	}
}

// A different state is not held back by the pause for the one that failed.
func TestNewStateIsTriedAtOnceAfterAFailure(t *testing.T) {
	s, fake, st, _, _ := setup(t)
	ctx := context.Background()
	n := &flaky{fakeNode: fake, applyErr: errRefused}
	s.node = n
	s.applyState(ctx)
	if _, err := domain.NewInbounds(st, nil, time.Now).Create(ctx, domain.NewInbound{NodeID: LocalNode, Preset: "trojan_reality"}); err != nil {
		t.Fatal(err)
	}
	s.applyState(ctx)
	if n.applyCalls != 2 {
		t.Fatalf("an admin's change waits for nobody: %d calls", n.applyCalls)
	}
}

// While a node does not answer at all, nothing else is pushed at it; once its health
// check passes again everything that was held back goes out.
func TestUnreachableNodeIsLeftAloneUntilItIsBack(t *testing.T) {
	s, fake, _, _, _ := setup(t)
	ctx := context.Background()
	n := &flaky{fakeNode: fake, applyErr: fmt.Errorf("%w: dial tcp: refused", nodeapi.ErrUnavailable)}
	s.node = n
	s.applyState(ctx)
	s.pushPolicies(ctx, true)
	if len(fake.policies) != 0 {
		t.Fatalf("policies were pushed at a node that is down: %d", len(fake.policies))
	}
	n.applyErr = nil
	s.refreshHealth(ctx)
	select {
	case <-s.stateDirty:
	default:
		t.Fatal("a node that answers again must be given its state")
	}
	select {
	case <-s.policiesDirty:
	default:
		t.Fatal("and its policies")
	}
	s.applyState(ctx)
	if len(fake.applied) != 1 {
		t.Fatalf("applied %d times", len(fake.applied))
	}
}

// Policies that did not change are not sent on every tick of the upkeep; they are sent
// again when they are old, because the node counts only its own traffic against a quota
// that the user's traffic elsewhere uses up too.
func TestUnchangedPoliciesAreNotPushedEveryTick(t *testing.T) {
	s, fake, _, _, now := setup(t)
	ctx := context.Background()
	s.applyState(ctx) // the state carries the policies
	for range 4 {
		*now = now.Add(30 * time.Second)
		s.applyState(ctx)
	}
	if len(fake.applied) != 1 || len(fake.policies) != 0 {
		t.Fatalf("unchanged: %d states, %d policy pushes", len(fake.applied), len(fake.policies))
	}
	*now = now.Add(5 * time.Minute)
	s.applyState(ctx)
	if len(fake.policies) != 1 {
		t.Fatalf("old policies go out again: %d pushes", len(fake.policies))
	}
}

// Two nodes share each user's quota, and each counts only its own traffic: they are
// refreshed sooner than a single node is.
func TestPoliciesRefreshSoonerWithSeveralNodes(t *testing.T) {
	s, fake, st, _, now := setup(t)
	ctx := context.Background()
	s.applyState(ctx)
	addRemote(t, s, st)
	s.applyState(ctx)
	before := len(fake.applied) + len(fake.policies)
	*now = now.Add(2 * time.Minute)
	s.applyState(ctx)
	if len(fake.applied)+len(fake.policies) == before {
		t.Fatal("with two nodes the policies are refreshed after a minute")
	}
}

// What a node reported about its devices is forgotten once it stops answering: they would
// otherwise hold places in the device limit on the other nodes for ever.
func TestOnlineViewOfADeadNodeIsForgotten(t *testing.T) {
	s, fake, _, _, _ := setup(t)
	ctx := context.Background()
	n := &flaky{fakeNode: fake}
	s.node = n
	fake.batch = nodeapi.Counters{Epoch: "e", Seq: 1, Online: map[string]nodeapi.Online{"s1": {IPs: []string{"203.0.113.9"}, Conns: 1}}}
	s.pullCounters(ctx)
	if len(s.Online()) != 1 {
		t.Fatalf("the live view: %+v", s.Online())
	}
	n.countersErr = nodeapi.ErrUnavailable
	for range failedPullsBlank - 1 {
		s.pullCounters(ctx)
	}
	if len(s.Online()) != 1 {
		t.Fatal("one or two failed pulls are not the end of a node")
	}
	s.pullCounters(ctx)
	if len(s.Online()) != 0 {
		t.Fatalf("a dead node's devices are still online: %+v", s.Online())
	}
	n.countersErr = nil
	s.pullCounters(ctx)
	if len(s.Online()) != 1 {
		t.Fatal("and they are back with the node")
	}
}

// A node is a server somebody else may run: it cannot take traffic off a user, nor
// charge a user with more than its link can carry.
func TestCountersFromANodeAreVetted(t *testing.T) {
	s, fake, st, users, _ := setup(t)
	ctx := context.Background()
	tariffs, _ := st.Q.ListTariffs(ctx)
	mk := func(name string) (db.User, string) {
		u, err := users.Create(ctx, domain.CreateInput{Name: name, TariffID: tariffs[1].ID})
		if err != nil {
			t.Fatal(err)
		}
		slot, _ := st.Q.GetSlot(ctx, u.SlotID.Int64)
		return u, slot.Name
	}
	victim, victimSlot := mk("victim")
	friend, friendSlot := mk("friend")
	honest, honestSlot := mk("honest")
	fake.batch = nodeapi.Counters{Epoch: "e", Seq: 1, Slots: map[string]nodeapi.Traffic{
		victimSlot: {Up: 1 << 60, Down: 5}, // more than any link carries
		friendSlot: {Up: -9_000_000_000_000, Down: 1},
		honestSlot: {Up: 10, Down: 20},
	}, Pools: map[string]map[string]nodeapi.Traffic{honestSlot: {"1": {Up: -1}}}}
	s.pullCounters(ctx)
	for _, c := range []struct {
		u        db.User
		up, down int64
	}{{victim, 0, 0}, {friend, 0, 0}, {honest, 10, 20}} {
		got, _ := st.Q.GetUser(ctx, c.u.ID)
		if got.UsedUp != c.up || got.UsedDown != c.down {
			t.Fatalf("user %d: up %d down %d, want %d %d", c.u.ID, got.UsedUp, got.UsedDown, c.up, c.down)
		}
	}
	if len(fake.acked) != 1 || fake.acked[0] != 1 {
		t.Fatalf("the batch is still acknowledged, or the node offers it for ever: %v", fake.acked)
	}
}

// With no traffic the node says Idle: there is no batch, so nothing is stored or acked,
// unless it is the first thing the panel hears of a new epoch.
func TestIdleReplyStoresNothing(t *testing.T) {
	s, fake, _, _, _ := setup(t)
	ctx := context.Background()
	fake.batch = nodeapi.Counters{Epoch: "e1", Seq: 4, Idle: true, Online: map[string]nodeapi.Online{"s1": {IPs: []string{"203.0.113.9"}}}}
	s.pullCounters(ctx) // a new epoch: remembered
	if e, seq, _ := s.countersPos(ctx); e != "e1" || seq != 4 {
		t.Fatalf("position %s/%d", e, seq)
	}
	if len(fake.acked) != 0 {
		t.Fatalf("nothing was cut, so nothing is acknowledged: %v", fake.acked)
	}
	acks := len(fake.acked)
	fake.batch.Seq = 7 // the node restarted and skipped ahead
	s.pullCounters(ctx)
	if len(fake.acked) != acks {
		t.Fatal("an idle reply is not acknowledged")
	}
	if _, seq, _ := s.countersPos(ctx); seq != 4 {
		t.Fatalf("an idle reply wrote the position: %d", seq)
	}
	if len(s.Online()) != 1 {
		t.Fatal("the live view still comes through")
	}
}

// An inbound the node would refuse must stay out of the state, or the whole state is
// refused and no other change reaches the node.
func TestInvalidInboundStaysOutOfTheNodesState(t *testing.T) {
	s, fake, st, _, _ := setup(t)
	ctx := context.Background()
	in, err := domain.NewInbounds(st, nil, time.Now).Create(ctx, domain.NewInbound{NodeID: LocalNode, Preset: "trojan_reality"})
	if err != nil {
		t.Fatal(err)
	}
	row, _ := st.Q.GetInbound(ctx, in.ID)
	tpl, err := proto.Parse(row.Config)
	if err != nil {
		t.Fatal(err)
	}
	// The panel's port moved, say, so REALITY's target is now a private address.
	tpl["reality-config"].(map[string]any)["dest"] = "10.0.0.5:443"
	if _, err := st.Q.UpdateInbound(ctx, db.UpdateInboundParams{Port: row.Port, Enabled: 1, Config: string(tpl.JSON()), DisplayName: row.DisplayName, UpdatedAt: row.UpdatedAt, ID: row.ID}); err != nil {
		t.Fatal(err)
	}
	s.applyState(ctx)
	if len(fake.applied) != 1 {
		t.Fatalf("the state is still applied: %d", len(fake.applied))
	}
	var names []string
	for _, i := range fake.applied[0].Inbounds {
		names = append(names, i.Name)
		if i.Name == row.Name {
			t.Fatalf("the invalid inbound went to the node: %v", names)
		}
	}
	if len(names) != 4 {
		t.Fatalf("the others are there: %v", names)
	}
}

// Old traffic by the hour and quiet devices are deleted once an hour, not at every tick
// of the upkeep: the delete scans a big table while it holds the one writer.
func TestOldRowsArePrunedHourly(t *testing.T) {
	s, _, st, users, now := setup(t)
	ctx := context.Background()
	tariffs, _ := st.Q.ListTariffs(ctx)
	u, _ := users.Create(ctx, domain.CreateInput{Name: "a", TariffID: tariffs[1].ID})
	old := now.Add(-70*24*time.Hour).Unix() / 3600
	count := func() int {
		rows, err := st.Q.UserTrafficHourly(ctx, db.UserTrafficHourlyParams{UserID: u.ID, Hour: 0})
		if err != nil {
			t.Fatal(err)
		}
		return len(rows)
	}
	put := func() {
		if err := st.Q.AddTrafficHourly(ctx, db.AddTrafficHourlyParams{UserID: u.ID, Hour: old, Up: 1, Down: 1}); err != nil {
			t.Fatal(err)
		}
	}
	put()
	s.m.maintain(ctx)
	if count() != 0 {
		t.Fatal("the first upkeep prunes")
	}
	put()
	*now = now.Add(30 * time.Second)
	s.m.maintain(ctx)
	if count() != 1 {
		t.Fatal("the next upkeep, half a minute later, must not scan the table again")
	}
	*now = now.Add(time.Hour)
	s.m.maintain(ctx)
	if count() != 0 {
		t.Fatal("an hour later it does")
	}
}

// Recording the devices seen online no longer deletes old ones: that is the hourly prune.
func TestRecordDevicesDoesNotPrune(t *testing.T) {
	s, fake, st, users, now := setup(t)
	ctx := context.Background()
	tariffs, _ := st.Q.ListTariffs(ctx)
	u, _ := users.Create(ctx, domain.CreateInput{Name: "a", TariffID: tariffs[1].ID})
	slot, _ := st.Q.GetSlot(ctx, u.SlotID.Int64)
	if err := st.Q.UpsertDevice(ctx, db.UpsertDeviceParams{UserID: u.ID, Ip: "198.51.100.1", FirstSeen: 1, LastSeen: 1}); err != nil {
		t.Fatal(err)
	}
	fake.batch = nodeapi.Counters{Epoch: "e", Seq: 1, Online: map[string]nodeapi.Online{slot.Name: {IPs: []string{"203.0.113.9"}}}}
	s.pullCounters(ctx)
	if err := s.m.recordDevices(ctx, *now); err != nil {
		t.Fatal(err)
	}
	devices, _ := st.Q.ListUserDevices(ctx, u.ID)
	if len(devices) != 2 {
		t.Fatalf("devices: %+v", devices)
	}
}
