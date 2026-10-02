package domain

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"mikan/internal/panel/store"
	"mikan/internal/panel/store/db"
)

// grantsEnv is a user with a small base quota (bytes) and a helper to give it grants.
type grantsEnv struct {
	t     *testing.T
	ctx   context.Context
	st    *store.Store
	users *Users
	ch    *changes
	now   *time.Time
	u     db.User
}

func newGrantsEnv(t *testing.T, limit int64) *grantsEnv {
	t.Helper()
	now := time.Unix(1_800_000_000, 0)
	st, users, ch := setup(t, &now)
	ctx := context.Background()
	tariffs, _ := st.Q.ListTariffs(ctx)
	u, err := users.Create(ctx, CreateInput{Name: "a", TariffID: tariffs[1].ID}) // period reset every 30 days
	if err != nil {
		t.Fatal(err)
	}
	if limit > 0 {
		if u, err = users.Update(ctx, u.ID, Patch{TrafficLimit: &limit}); err != nil {
			t.Fatal(err)
		}
	} else if u, err = users.Update(ctx, u.ID, Patch{ClearTrafficLimit: true}); err != nil {
		t.Fatal(err)
	}
	return &grantsEnv{t: t, ctx: ctx, st: st, users: users, ch: ch, now: &now, u: u}
}

// grant gives bytes directly (no size check: the tests count in bytes).
func (e *grantsEnv) grant(pool, bytes int64, lifetime string, days int64) db.TrafficGrant {
	e.t.Helper()
	u, _ := e.st.Q.GetUser(e.ctx, e.u.ID)
	g, err := GrantTx(e.ctx, e.st.Q, u, GrantSpec{PoolID: pool, Bytes: bytes, Lifetime: lifetime, Days: days, Source: SourceAdmin}, *e.now)
	if err != nil {
		e.t.Fatal(err)
	}
	*e.now = e.now.Add(time.Second) // a distinct creation time: "oldest first" is defined
	return g
}

func (e *grantsEnv) count(n int64) {
	e.t.Helper()
	if err := e.st.Tx(e.ctx, func(q *db.Queries) error { return CountUserTraffic(e.ctx, q, e.u.ID, 0, n, *e.now) }); err != nil {
		e.t.Fatal(err)
	}
}

func (e *grantsEnv) countPool(pool, n int64) {
	e.t.Helper()
	if err := e.st.Tx(e.ctx, func(q *db.Queries) error { return CountPoolTraffic(e.ctx, q, e.u.ID, pool, n, 0, *e.now) }); err != nil {
		e.t.Fatal(err)
	}
}

// left reads what remains of each grant, by id.
func (e *grantsEnv) left(gs ...db.TrafficGrant) []int64 {
	e.t.Helper()
	all, err := e.st.Q.ListUserGrants(e.ctx, e.u.ID)
	if err != nil {
		e.t.Fatal(err)
	}
	by := map[int64]int64{}
	for _, g := range all {
		by[g.ID] = g.Remaining
	}
	out := make([]int64, len(gs))
	for i, g := range gs {
		out[i] = by[g.ID]
	}
	return out
}

func (e *grantsEnv) grantsLeft() GrantsLeft {
	e.t.Helper()
	g, err := UserGrantsLeft(e.ctx, e.st.Q, e.u.ID, *e.now)
	if err != nil {
		e.t.Fatal(err)
	}
	return g
}

func eq(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestOverflow(t *testing.T) {
	limit := sql.NullInt64{Int64: 100, Valid: true}
	cases := []struct{ used, n, want int64 }{
		{0, 50, 0},      // inside the base
		{90, 20, 10},    // straddles the boundary
		{100, 5, 5},     // all past it
		{120, 5, 5},     // already past it
		{100, 0, 0},     // nothing new
		{0, 100, 0},     // exactly the base
		{99, 1000, 999}, // one byte of base left
	}
	for _, c := range cases {
		if got := Overflow(limit, c.used, c.n); got != c.want {
			t.Errorf("Overflow(100, %d, %d) = %d, want %d", c.used, c.n, got, c.want)
		}
	}
	if got := Overflow(sql.NullInt64{}, 1<<40, 1<<40); got != 0 {
		t.Errorf("unlimited overflow = %d", got)
	}
}

// The base quota goes first, then the grants: the soonest to expire first, then the
// oldest; traffic straddling the base boundary is split.
func TestGrantsSpendingOrder(t *testing.T) {
	e := newGrantsEnv(t, 100)
	first := e.grant(0, 50, LifetimeUsed, 0)   // no end: after those that end, the oldest of them
	second := e.grant(0, 20, LifetimeUsed, 0)  // no end, newer: last
	month := e.grant(0, 30, LifetimePeriod, 0) // ends with the period (30 days)
	week := e.grant(0, 40, LifetimeDays, 7)    // ends in 7 days: first
	all := []db.TrafficGrant{first, second, month, week}
	if got := e.grantsLeft().Main(e.u.ID); got != 140 {
		t.Fatalf("grants left = %d, want 140", got)
	}
	e.count(90)
	if got := e.left(all...); !eq(got, []int64{50, 20, 30, 40}) {
		t.Fatalf("inside the base: %v", got)
	}
	e.count(30) // 10 of base, 20 from the week grant
	if got := e.left(all...); !eq(got, []int64{50, 20, 30, 20}) {
		t.Fatalf("straddling the base: %v", got)
	}
	e.count(45) // week 20, then month 25
	if got := e.left(all...); !eq(got, []int64{50, 20, 5, 0}) {
		t.Fatalf("soonest to expire first: %v", got)
	}
	e.count(60) // month 5, then the older endless one 50, then 5 of the newer
	if got := e.left(all...); !eq(got, []int64{0, 15, 0, 0}) {
		t.Fatalf("oldest next: %v", got)
	}
	e.count(100) // 15 covered, the rest is not owed later
	if got := e.left(all...); !eq(got, []int64{0, 0, 0, 0}) {
		t.Fatalf("all spent: %v", got)
	}
	u, _ := e.st.Q.GetUser(e.ctx, e.u.ID)
	if State(u, e.grantsLeft().Main(u.ID), *e.now) != StateLimited {
		t.Fatal("no base and no grants left: limited")
	}
	// A new grant is whole: past overshoot is not taken from it.
	fresh := e.grant(0, 10, LifetimeUsed, 0)
	if got := e.left(fresh); got[0] != 10 {
		t.Fatalf("fresh grant %d", got[0])
	}
}

// Pool traffic spends the pool's grants only; main traffic only the main ones.
func TestGrantsPoolAndMainApart(t *testing.T) {
	e := newGrantsEnv(t, 100)
	p, err := e.st.Q.CreateTrafficPool(e.ctx, db.CreateTrafficPoolParams{Name: "WL", CreatedAt: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.st.Q.SetUserPoolLimit(e.ctx, db.SetUserPoolLimitParams{UserID: e.u.ID, PoolID: p.ID, TrafficLimit: sql.NullInt64{Int64: 10, Valid: true}}); err != nil {
		t.Fatal(err)
	}
	main := e.grant(0, 50, LifetimeUsed, 0)
	wl := e.grant(p.ID, 50, LifetimeUsed, 0)
	e.countPool(p.ID, 30) // 10 base of the pool, 20 from its grant
	if got := e.left(main, wl); !eq(got, []int64{50, 30}) {
		t.Fatalf("pool traffic: %v", got)
	}
	e.count(120) // 100 base, 20 from the main grant
	if got := e.left(main, wl); !eq(got, []int64{30, 30}) {
		t.Fatalf("main traffic: %v", got)
	}
	g := e.grantsLeft()
	if g.Main(e.u.ID) != 30 || g.Pool(e.u.ID, p.ID) != 30 {
		t.Fatalf("left: %v", g)
	}
	up, _ := e.st.Q.GetUserPool(e.ctx, db.GetUserPoolParams{UserID: e.u.ID, PoolID: p.ID})
	if PoolExhausted(up, g.Pool(e.u.ID, p.ID)) || !PoolExhausted(up, 0) {
		t.Fatal("a pool past its base is exhausted only without grants")
	}
	// A pool without a limit never touches its grants.
	if err := e.st.Q.SetUserPoolLimit(e.ctx, db.SetUserPoolLimitParams{UserID: e.u.ID, PoolID: p.ID}); err != nil {
		t.Fatal(err)
	}
	e.countPool(p.ID, 1000)
	if got := e.left(wl); got[0] != 30 {
		t.Fatalf("unlimited pool spent its grant: %d", got[0])
	}
}

// Unlimited main traffic never touches grants.
func TestGrantsUnlimitedBase(t *testing.T) {
	e := newGrantsEnv(t, 0)
	g := e.grant(0, 50, LifetimeUsed, 0)
	e.count(1 << 40)
	if got := e.left(g); got[0] != 50 {
		t.Fatalf("unlimited user spent a grant: %d", got[0])
	}
	if TrafficLeft(sql.NullInt64{}, 1<<40, 50) != -1 {
		t.Fatal("unlimited quota must stay unlimited")
	}
}

// A new period ends the grants that last one period; the others keep their remainder.
func TestGrantsAtPeriodReset(t *testing.T) {
	e := newGrantsEnv(t, 100)
	keep := e.grant(0, 50, LifetimeUsed, 0)
	once := e.grant(0, 50, LifetimePeriod, 0)
	if !once.ExpiresAt.Valid {
		t.Fatal("a period grant ends with the period")
	}
	if r, _ := NextReset(e.u, *e.now); once.ExpiresAt.Int64 != r.Unix() {
		t.Fatalf("period grant ends %d, the period %d", once.ExpiresAt.Int64, r.Unix())
	}
	e.count(160) // 100 base, then once (sooner) 50, keep 10
	if got := e.left(keep, once); !eq(got, []int64{40, 0}) {
		t.Fatalf("before the reset: %v", got)
	}
	once2 := e.grant(0, 30, LifetimePeriod, 0)
	if _, err := e.users.ResetTraffic(e.ctx, e.u.ID); err != nil {
		t.Fatal(err)
	}
	g := e.grantsLeft()
	if g.Main(e.u.ID) != 40 {
		t.Fatalf("after the reset %d left, want the used-up grant's 40", g.Main(e.u.ID))
	}
	all, _ := e.st.Q.ListUserGrants(e.ctx, e.u.ID)
	for _, x := range all {
		if x.ID == once2.ID && (GrantActive(x, *e.now) || x.Remaining != 30) {
			t.Fatalf("period grant after the reset: %+v", x)
		}
	}
	// The maintenance reset ends them the same way.
	once3 := e.grant(0, 30, LifetimePeriod, 0)
	if err := e.st.Tx(e.ctx, func(q *db.Queries) error { return StartPeriod(e.ctx, q, e.u.ID, e.now.Unix(), *e.now) }); err != nil {
		t.Fatal(err)
	}
	if e.grantsLeft().Main(e.u.ID) != 40 {
		t.Fatalf("period grant %d survived StartPeriod", once3.ID)
	}
}

// A grant for N days stops counting when they are over, also before any upkeep runs.
func TestGrantsDaysExpire(t *testing.T) {
	e := newGrantsEnv(t, 100)
	short := e.grant(0, 50, LifetimeDays, 3)
	if want := e.now.Add(-time.Second).Add(3 * 24 * time.Hour).Unix(); short.ExpiresAt.Int64 != want {
		t.Fatalf("expires %d, want %d", short.ExpiresAt.Int64, want)
	}
	e.count(110)
	if got := e.left(short); got[0] != 40 {
		t.Fatalf("before expiry: %d", got[0])
	}
	*e.now = time.Unix(short.ExpiresAt.Int64, 0)
	if e.grantsLeft().Main(e.u.ID) != 0 {
		t.Fatal("an expired grant still counts")
	}
	e.count(10)
	if got := e.left(short); got[0] != 40 {
		t.Fatalf("an expired grant was spent: %d", got[0])
	}
	u, _ := e.st.Q.GetUser(e.ctx, e.u.ID)
	if State(u, e.grantsLeft().Main(u.ID), *e.now) != StateLimited {
		t.Fatal("past the base with an expired grant: limited")
	}
}

// The admin's grant brings a limited user back at once and tells the nodes.
func TestAdminGrant(t *testing.T) {
	e := newGrantsEnv(t, 100)
	e.count(150)
	u, _ := e.st.Q.GetUser(e.ctx, e.u.ID)
	if State(u, 0, *e.now) != StateLimited {
		t.Fatal("setup: limited")
	}
	before := e.ch.policies
	g, err := e.users.Grant(e.ctx, e.u.ID, GrantInput{Bytes: 2 * GiB, Lifetime: LifetimeUsed, Note: " bonus "})
	if err != nil {
		t.Fatal(err)
	}
	if g.Source != SourceAdmin || g.Note != "bonus" || g.Remaining != 2*GiB || g.ExpiresAt.Valid || g.PoolID.Valid {
		t.Fatalf("grant %+v", g)
	}
	if e.ch.policies != before+1 {
		t.Fatal("the nodes must hear about the grant")
	}
	left := e.grantsLeft().Main(u.ID)
	if State(u, left, *e.now) != StateActive || TrafficLeft(u.TrafficLimit, u.UsedUp+u.UsedDown, left) != 2*GiB {
		t.Fatalf("after the grant: %s, left %d", State(u, left, *e.now), left)
	}
	bad := []struct {
		in    GrantInput
		field string
	}{
		{GrantInput{Bytes: GiB - 1, Lifetime: LifetimeUsed}, "bytes"},
		{GrantInput{Bytes: MaxGrantBytes + 1, Lifetime: LifetimeUsed}, "bytes"},
		{GrantInput{Bytes: GiB, Lifetime: "forever"}, "lifetime"},
		{GrantInput{Bytes: GiB, Lifetime: LifetimeDays}, "days"},
		{GrantInput{Bytes: GiB, Lifetime: LifetimeDays, Days: MaxGrantDays + 1}, "days"},
		{GrantInput{Bytes: GiB, Lifetime: LifetimeUsed, PoolID: 999}, "pool_id"},
	}
	for _, c := range bad {
		var fe *FieldError
		if _, err := e.users.Grant(e.ctx, e.u.ID, c.in); !errors.As(err, &fe) || fe.Field != c.field {
			t.Errorf("%+v: %v, want a %s error", c.in, err, c.field)
		}
	}
	if _, err := e.users.Grant(e.ctx, 999, GrantInput{Bytes: GiB, Lifetime: LifetimeUsed}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown user: %v", err)
	}
}
