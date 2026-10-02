package domain

import (
	"context"
	"errors"
	"testing"
	"time"

	"mikan/internal/nodeapi"
	"mikan/internal/panel/presets"
	"mikan/internal/panel/store"
	"mikan/internal/panel/store/db"
	"mikan/internal/proto"
)

// meddler is a node that, while it checks a template, lets someone else change the inbound
// (the automatic moves, the CLI): the network sits between Update's read and its write.
type meddler struct {
	st    *store.Store
	id    int64
	calls int
	times int // changes to make; the check calls beyond it leave the row alone
	port  func(call int) string
}

func (m *meddler) Validate(ctx context.Context, _ int64, _ nodeapi.ValidateRequest) error {
	m.calls++
	if m.calls > m.times {
		return nil
	}
	in, err := m.st.Q.GetInbound(ctx, m.id)
	if err != nil {
		return err
	}
	_, err = m.st.Q.UpdateInbound(ctx, db.UpdateInboundParams{Port: m.port(m.calls), Enabled: in.Enabled, Config: in.Config, DisplayName: in.DisplayName,
		UpdatedAt: in.UpdatedAt + 1, ID: m.id})
	return err
}

// A change made while Update checks the template on the node is not overwritten with the
// old row: the checks run again on the row as it is now, and the patch lands on top of it.
func TestUpdateKeepsAChangeMadeDuringTheChecks(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	m := &meddler{times: 1, port: func(int) string { return "2999" }}
	st, s := inbounds(t, &now, m)
	ctx := context.Background()
	x := find(t, s, 1, "vless-xhttp")
	m.st, m.id = st, x.ID

	// The admin retargets the inbound; autotune moves its port in the meantime.
	prev, next, err := s.Update(ctx, x.ID, InboundPatch{Dest: ptr("203.0.113.20:443"), ServerName: ptr("www.example.org")})
	if err != nil {
		t.Fatal(err)
	}
	tpl, _ := proto.Parse(next.Config)
	if dest, _ := presets.Dest(tpl); dest != "203.0.113.20:443" {
		t.Fatalf("the target did not land: %s", dest)
	}
	if next.Port != "2999" || prev.Port != "2999" {
		t.Fatalf("the port moved meanwhile is back to %q (prev %q): the old row overwrote it", next.Port, prev.Port)
	}
	if m.calls != 2 {
		t.Fatalf("the node checked %d times, want 2 (once more on the new row)", m.calls)
	}
	if row, _ := st.Q.GetInbound(ctx, x.ID); row.Port != "2999" || row != next {
		t.Fatalf("stored %+v, returned %+v", row, next)
	}
}

// An inbound that keeps changing under the checks is refused, not written over.
func TestUpdateGivesUpOnAnInboundThatKeepsChanging(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	m := &meddler{times: 100, port: func(call int) string { return string(rune('3'+call)) + "000" }}
	st, s := inbounds(t, &now, m)
	ctx := context.Background()
	x := find(t, s, 1, "vless-xhttp")
	m.st, m.id = st, x.ID

	_, _, err := s.Update(ctx, x.ID, InboundPatch{Dest: ptr("203.0.113.20:443"), ServerName: ptr("www.example.org")})
	if !errors.Is(err, ErrInboundChanged) {
		t.Fatalf("got %v, want ErrInboundChanged", err)
	}
	if m.calls != updateTries {
		t.Fatalf("checked %d times, want %d", m.calls, updateTries)
	}
	row, _ := st.Q.GetInbound(ctx, x.ID)
	if tpl, _ := proto.Parse(row.Config); tpl == nil {
		t.Fatal("the config is gone")
	} else if dest, _ := presets.Dest(tpl); dest == "203.0.113.20:443" {
		t.Fatal("a refused update was written")
	}
}
