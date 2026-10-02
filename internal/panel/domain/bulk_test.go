package domain

import (
	"context"
	"strconv"
	"testing"
	"time"
)

// A bulk action is one transaction: when it fails on a user, the users before it are as
// they were (a loop of single changes left them changed and unrecorded).
func TestBulkIsAllOrNothing(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	st, users, ch := setup(t, &now)
	ctx := context.Background()
	tariffs, _ := st.Q.ListTariffs(ctx)
	var ids []int64
	for _, name := range []string{"a", "b", "c"} {
		u, err := users.Create(ctx, CreateInput{Name: name, TariffID: tariffs[1].ID})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, u.ID)
	}
	expiry := func(id int64) int64 {
		u, _ := st.Q.GetUser(ctx, id)
		return u.ExpiresAt.Int64
	}
	before := []int64{expiry(ids[0]), expiry(ids[1]), expiry(ids[2])}
	pushes := ch.policies

	// The third user cannot be written: the first two must not stay extended.
	if _, err := st.DB.ExecContext(ctx, "CREATE TRIGGER no_third BEFORE UPDATE ON users WHEN NEW.id = "+strconv.FormatInt(ids[2], 10)+" BEGIN SELECT RAISE(ABORT, 'refused'); END"); err != nil {
		t.Fatal(err)
	}
	if _, err := users.Bulk(ctx, ids, BulkExtend, 10); err == nil {
		t.Fatal("the bulk change did not fail")
	}
	for i, id := range ids {
		if got := expiry(id); got != before[i] {
			t.Fatalf("user %d was changed by a bulk that failed: %d, was %d", i, got, before[i])
		}
	}
	if ch.policies != pushes {
		t.Fatal("nodes were told about a change that did not happen")
	}
	if _, err := st.DB.ExecContext(ctx, "DROP TRIGGER no_third"); err != nil {
		t.Fatal(err)
	}

	// Gone users are skipped, a user named twice is changed once.
	n, err := users.Bulk(ctx, []int64{ids[0], 9999, ids[0], ids[1]}, BulkExtend, 10)
	if err != nil || n != 2 {
		t.Fatalf("bulk: %d changed, %v, want 2", n, err)
	}
	if got, want := expiry(ids[0]), before[0]+10*86400; got != want {
		t.Fatalf("user listed twice: expiry %d, want %d (once)", got, want)
	}
	if ch.policies != pushes+1 {
		t.Fatalf("policy pushes %d, want one for the whole list", ch.policies-pushes)
	}
	if n, err := users.Bulk(ctx, ids, BulkDelete, 0); err != nil || n != 3 {
		t.Fatalf("delete: %d %v", n, err)
	}
	if _, err := st.Q.GetUser(ctx, ids[1]); err == nil {
		t.Fatal("a deleted user is still there")
	}
	if _, err := users.Bulk(ctx, ids, "explode", 0); err == nil {
		t.Fatal("an unknown action was accepted")
	}
}
