package nodesync

import (
	"context"
	"testing"

	"mikan/internal/panel/store/db"
)

// An inbound behind a TCP proxy listens on its own address on the node (GitHub issue
// #11); the others keep every address, and the change reaches the node.
func TestListenReachesTheNode(t *testing.T) {
	s, node, st, _, _ := setup(t)
	ctx := context.Background()
	s.applyState(ctx)
	ins, _ := st.Q.ListInbounds(ctx)
	inside := ins[0]
	if err := st.Q.SetInboundListen(ctx, db.SetInboundListenParams{Listen: "127.0.0.1", ID: inside.ID}); err != nil {
		t.Fatal(err)
	}
	s.applyState(ctx)
	if len(node.applied) != 2 {
		t.Fatalf("the new address must be applied: %d states", len(node.applied))
	}
	for _, in := range node.applied[1].Inbounds {
		want := ""
		if in.Name == inside.Name {
			want = "127.0.0.1"
		}
		if in.Listen != want {
			t.Fatalf("inbound %s listens on %q, want %q", in.Name, in.Listen, want)
		}
	}
}
