package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"mikan/internal/panel/store/db"
)

// A panic inside a transaction (it holds the write lock from BEGIN) rolls it back before
// the panic goes on: the next writer must not wait for a context that never ends.
func TestTxRollsBackOnPanic(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	func() {
		defer func() {
			if recover() == nil {
				t.Error("the panic was swallowed")
			}
		}()
		_ = st.Tx(ctx, func(q *db.Queries) error {
			if err := q.SetSetting(ctx, db.SetSettingParams{Key: "k", Value: "from the panicking transaction"}); err != nil {
				return err
			}
			panic("boom")
		})
	}()
	done := make(chan error, 1)
	go func() {
		done <- st.Tx(ctx, func(q *db.Queries) error {
			return q.SetSetting(ctx, db.SetSettingParams{Key: "other", Value: "v"})
		})
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("the write after the panic: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the write lock was not released by the panicking transaction")
	}
	if _, err := st.Q.GetSetting(ctx, "k"); err == nil {
		t.Fatal("what the panicking transaction wrote was committed")
	}
}

func TestTxReturnsFnsError(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	boom := errors.New("boom")
	if err := st.Tx(ctx, func(q *db.Queries) error { return boom }); !errors.Is(err, boom) {
		t.Fatalf("got %v", err)
	}
}

// A duplicate is told by the driver's code, not by the words of its message.
func TestIsUnique(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.Q.CreateTrafficPool(ctx, db.CreateTrafficPoolParams{Name: "WL", CreatedAt: 1}); err != nil {
		t.Fatal(err)
	}
	_, err = st.Q.CreateTrafficPool(ctx, db.CreateTrafficPoolParams{Name: "WL", CreatedAt: 1})
	if !IsUnique(err) {
		t.Fatalf("a duplicate pool name: %v", err)
	}
	if IsUnique(nil) || IsUnique(errors.New("UNIQUE constraint failed: made up")) {
		t.Fatal("something that is not a constraint violation was taken for one")
	}
	if _, err := st.DB.ExecContext(ctx, "INSERT INTO traffic_packages (name, bytes, lifetime, created_at) VALUES ('p', 0, 'used', 1)"); err == nil || IsUnique(err) {
		t.Fatalf("a CHECK violation is not a duplicate: %v", err)
	}
}
