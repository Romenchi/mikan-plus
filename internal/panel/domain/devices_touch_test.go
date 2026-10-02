package domain

import (
	"context"
	"testing"
	"time"

	"mikan/internal/panel/store/db"
)

// A known device asking again with nothing new is not written down each time; something
// new about it, or enough time, is.
func TestKnownDeviceIsTouchedOnlyWhenItSaysSomethingNew(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	devs, users, newUser := newDevices(t, &now)
	ctx := context.Background()
	u, _ := users.Get(ctx, newUser("a"))
	in := DeviceInfo{HWID: "phone-0123456789", OS: "iOS", App: "Happ/1", IP: "203.0.113.5"}
	bind := func(in DeviceInfo) db.BoundDevice {
		t.Helper()
		if _, err := devs.Bind(ctx, u, in, false); err != nil {
			t.Fatal(err)
		}
		d, err := devs.st.Q.GetBoundDevice(ctx, db.GetBoundDeviceParams{UserID: u.ID, Hwid: in.HWID})
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	first := bind(in)
	now = now.Add(time.Minute)
	if d := bind(DeviceInfo{HWID: in.HWID, App: in.App}); d.LastSeen != first.LastSeen {
		t.Fatal("nothing new, yet written")
	}
	if d := bind(DeviceInfo{HWID: in.HWID, IP: "203.0.113.9"}); d.LastIp != "203.0.113.9" || d.Os != "iOS" {
		t.Fatalf("a new address: %+v", d)
	}
	seen := bind(in).LastSeen
	now = now.Add(touchEvery)
	if d := bind(in); d.LastSeen == seen {
		t.Fatal("last_seen never moves")
	}
}
