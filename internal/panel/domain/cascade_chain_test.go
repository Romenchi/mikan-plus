package domain

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	"mikan/internal/panel/store/db"
)

// A chain longer than maxChain is refused as too long, not as a loop: the admin who sees
// "loop" looks for a cycle that is not there.
func TestLongChainIsNotACycle(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	st, _, _ := setup(t, &now)
	ctx := context.Background()
	q := st.Q
	var nodes []db.Node
	for i := 0; i < maxChain+2; i++ {
		n, err := q.CreateNode(ctx, db.CreateNodeParams{Name: fmt.Sprintf("N%d", i), Address: fmt.Sprintf("203.0.113.%d:25305", i+10), PublicHost: fmt.Sprintf("203.0.113.%d", i+10),
			CreatedAt: now.Unix(), UpdatedAt: now.Unix()})
		if err != nil {
			t.Fatal(err)
		}
		nodes = append(nodes, n)
	}
	// N0 → N1 → … → N9, each node's relay going on to the next.
	for i, n := range nodes {
		if _, err := EnsureRelay(ctx, q, n, now); err != nil {
			t.Fatal(err)
		}
		if i+1 < len(nodes) {
			if err := q.SetNodeRelayRoute(ctx, db.SetNodeRelayRouteParams{Outbound: "direct", ExitNodeID: sql.NullInt64{Int64: nodes[i+1].ID, Valid: true}, NodeID: n.ID}); err != nil {
				t.Fatal(err)
			}
		}
	}
	// The panel's own node going out through N0 would make a chain of eleven hops.
	err := CheckExit(ctx, q, 1, nodes[0].ID)
	if !errors.Is(err, ErrExitLong) || errors.Is(err, ErrExitCycle) {
		t.Fatalf("a chain of %d hops: %v, want exit_too_long", len(nodes)+1, err)
	}
	// A chain within the limit is fine.
	if err := CheckExit(ctx, q, 1, nodes[len(nodes)-3].ID); err != nil {
		t.Fatalf("a short chain: %v", err)
	}
	// And a real cycle still is one.
	if err := CheckExit(ctx, q, nodes[len(nodes)-1].ID, nodes[len(nodes)-3].ID); !errors.Is(err, ErrExitCycle) {
		t.Fatalf("a cycle: %v", err)
	}
}
