package domain

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"mikan/internal/panel/store"
	"mikan/internal/panel/store/db"
)

type changes struct{ policies, slots int }

func (c *changes) PoliciesChanged() { c.policies++ }
func (c *changes) SlotsChanged()    { c.slots++ }

func setup(t *testing.T, now *time.Time) (*store.Store, *Users, *changes) {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := Seed(ctx, st, *now); err != nil {
		t.Fatal(err)
	}
	ch := &changes{}
	clock := func() time.Time { return *now }
	return st, NewUsers(st, NewPool(st, clock), ch, clock), ch
}

func TestState(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	at := func(d time.Duration) sql.NullInt64 { return sql.NullInt64{Int64: now.Add(d).Unix(), Valid: true} }
	limit := sql.NullInt64{Int64: 100, Valid: true}
	cases := []struct {
		name   string
		u      db.User
		grants int64
		want   string
	}{
		{"no limits", db.User{Status: "active"}, 0, StateActive},
		{"disabled wins", db.User{Status: "disabled", ExpiresAt: at(-time.Hour)}, 0, StateDisabled},
		{"expired", db.User{Status: "active", ExpiresAt: at(-time.Second)}, 0, StateExpired},
		{"expires exactly now", db.User{Status: "active", ExpiresAt: at(0)}, 0, StateExpired},
		{"over quota", db.User{Status: "active", TrafficLimit: limit, UsedUp: 40, UsedDown: 60}, 0, StateLimited},
		{"over quota with grants left", db.User{Status: "active", TrafficLimit: limit, UsedUp: 40, UsedDown: 70}, 1, StateActive},
		{"under quota", db.User{Status: "active", TrafficLimit: limit, UsedDown: 99}, 0, StateActive},
		{"expiring in 7d", db.User{Status: "active", ExpiresAt: at(7 * 24 * time.Hour)}, 0, StateExpiring},
		{"8 days left", db.User{Status: "active", ExpiresAt: at(8 * 24 * time.Hour)}, 0, StateActive},
	}
	for _, c := range cases {
		if got := State(c.u, c.grants, now); got != c.want {
			t.Errorf("%s: got %s, want %s", c.name, got, c.want)
		}
	}
}

func TestCreateCopiesTariffAndTakesSlot(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	st, users, ch := setup(t, &now)
	ctx := context.Background()
	tariffs, _ := st.Q.ListTariffs(ctx)
	std := tariffs[1] // Стандарт: 150 GiB, 30 days, 3 devices, period reset
	u, err := users.Create(ctx, CreateInput{Name: "  Анна ", TariffID: std.ID, Tags: []string{"tg", " "}})
	if err != nil {
		t.Fatal(err)
	}
	if u.Name != "Анна" || u.TrafficLimit != std.TrafficLimit || u.DeviceLimit != std.DeviceLimit || u.ResetStrategy != "period" {
		t.Fatalf("tariff not applied: %+v", u)
	}
	if !u.ExpiresAt.Valid || u.ExpiresAt.Int64 != now.Add(30*24*time.Hour).Unix() {
		t.Fatalf("expiry = %v", u.ExpiresAt)
	}
	if len(u.SubToken) != 24 || !u.SlotID.Valid {
		t.Fatalf("credentials: token %q slot %v", u.SubToken, u.SlotID)
	}
	if got := DecodeTags(u.Tags); len(got) != 1 || got[0] != "tg" {
		t.Fatalf("tags = %v", got)
	}
	slot, _ := st.Q.GetSlot(ctx, u.SlotID.Int64)
	if slot.State != "assigned" {
		t.Fatalf("slot state %s", slot.State)
	}
	if ch.policies != 1 || ch.slots != 0 {
		t.Fatalf("changes = %+v, want one policy push and no listener change", ch)
	}
	if _, err := users.Create(ctx, CreateInput{Name: "x", TariffID: 999}); err == nil {
		t.Fatal("unknown tariff accepted")
	}
}

func TestExtend(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	st, users, _ := setup(t, &now)
	ctx := context.Background()
	tariffs, _ := st.Q.ListTariffs(ctx)
	u, err := users.Create(ctx, CreateInput{Name: "a", TariffID: tariffs[1].ID})
	if err != nil {
		t.Fatal(err)
	}
	u, _ = users.Extend(ctx, u.ID, 30)
	if want := now.Add(60 * 24 * time.Hour).Unix(); u.ExpiresAt.Int64 != want {
		t.Fatalf("active user: expiry %d, want %d (added to the current term)", u.ExpiresAt.Int64, want)
	}
	now = now.Add(100 * 24 * time.Hour)
	u, _ = users.Extend(ctx, u.ID, 30)
	if want := now.Add(30 * 24 * time.Hour).Unix(); u.ExpiresAt.Int64 != want {
		t.Fatalf("expired user: expiry %d, want %d (counted from now)", u.ExpiresAt.Int64, want)
	}
}

func TestReissueBurnsOldSlot(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	st, users, _ := setup(t, &now)
	ctx := context.Background()
	tariffs, _ := st.Q.ListTariffs(ctx)
	u, _ := users.Create(ctx, CreateInput{Name: "a", TariffID: tariffs[0].ID})
	oldSlot, oldToken := u.SlotID.Int64, u.SubToken
	u, err := users.Reissue(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if u.SlotID.Int64 == oldSlot || u.SubToken == oldToken {
		t.Fatal("credentials not replaced")
	}
	s, _ := st.Q.GetSlot(ctx, oldSlot)
	if s.State != "burned" {
		t.Fatalf("old slot %s, want burned", s.State)
	}
	if _, err := st.Q.GetUserBySubToken(ctx, oldToken); err == nil {
		t.Fatal("old subscription token still resolves")
	}
}

func TestDeleteBurnsSlotAndPurgeKeepsAssigned(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	st, users, _ := setup(t, &now)
	ctx := context.Background()
	tariffs, _ := st.Q.ListTariffs(ctx)
	a, _ := users.Create(ctx, CreateInput{Name: "a", TariffID: tariffs[0].ID})
	b, _ := users.Create(ctx, CreateInput{Name: "b", TariffID: tariffs[0].ID})
	if err := users.Delete(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	pool := NewPool(st, func() time.Time { return now })
	if err := pool.PurgeBurned(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Q.GetSlot(ctx, a.SlotID.Int64); err == nil {
		t.Fatal("burned slot survived purge")
	}
	if s, err := st.Q.GetSlot(ctx, b.SlotID.Int64); err != nil || s.State != "assigned" {
		t.Fatalf("assigned slot damaged: %v %v", s, err)
	}
}

func TestNextReset(t *testing.T) {
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	if r, _ := NextReset(db.User{ResetStrategy: "month_start"}, now); !r.Equal(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("month_start: %v", r)
	}
	start := now.Add(-10 * 24 * time.Hour).Unix()
	if r, _ := NextReset(db.User{ResetStrategy: "period", PeriodStart: start, PeriodDays: 30}, now); r.Unix() != start+30*86400 {
		t.Fatalf("period: %v", r)
	}
	if _, ok := NextReset(db.User{ResetStrategy: "none"}, now); ok {
		t.Fatal("none must not reset")
	}
}
