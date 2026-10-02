package autotune

import (
	"testing"

	"mikan/internal/nodeapi"
	"mikan/internal/panel/presets"
	"mikan/internal/panel/store/db"
)

// Replacing a target takes a scan that lasts for a while. What the admin changed in the
// inbound meanwhile (it is off, renamed, on another port) stays: only the target is new.
func TestReplacingATargetKeepsTheAdminsEditsMadeDuringTheScan(t *testing.T) {
	e := setup(t)
	every := DefaultOptions().CheckEvery
	xhttp := e.inbound(t, "vless-xhttp")
	dead := presets.DefaultDest
	e.nodes.targets[dead] = nodeapi.TargetResult{Dest: dead, Error: "timeout"}
	e.nodes.found = []nodeapi.TargetResult{good("203.0.113.44:443", "shop.example.org")}
	e.nodes.onScan = func() {
		cur, err := e.st.Q.GetInbound(e.ctx, xhttp.ID)
		if err != nil {
			t.Error(err)
			return
		}
		if _, err := e.st.Q.UpdateInbound(e.ctx, db.UpdateInboundParams{Port: "2443", Enabled: 0, Config: cur.Config, DisplayName: "My XHTTP",
			UpdatedAt: cur.UpdatedAt + 1, ID: cur.ID}); err != nil {
			t.Error(err)
		}
	}

	e.step(t, 0)
	e.step(t, every)
	e.step(t, every)
	got := e.inbound(t, "vless-xhttp")
	if sniOf(t, got) != "shop.example.org" {
		t.Fatalf("the target was not replaced: %s", sniOf(t, got))
	}
	if got.Port != "2443" || got.Enabled != 0 || got.DisplayName != "My XHTTP" {
		t.Fatalf("the admin's edits are gone: port %s enabled %d name %q", got.Port, got.Enabled, got.DisplayName)
	}
}
