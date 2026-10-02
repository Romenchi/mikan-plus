package domain

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

func newDevices(t *testing.T, now *time.Time) (*Devices, *Users, func(name string) int64) {
	t.Helper()
	st, users, ch := setup(t, now)
	ctx := context.Background()
	clock := func() time.Time { return *now }
	tariffs, _ := st.Q.ListTariffs(ctx)
	newUser := func(name string) int64 {
		u, err := users.Create(ctx, CreateInput{Name: name, TariffID: tariffs[1].ID})
		if err != nil {
			t.Fatal(err)
		}
		return u.ID
	}
	return NewDevices(st, NewPool(st, clock), ch, clock), users, newUser
}

// "No device limit" is not unlimited: the id is whatever a client sends and each new one
// takes a slot for good, so a holder of the link could use the pool up.
func TestUnlimitedUserStillHasACeilingOfDevices(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	devs, users, newUser := newDevices(t, &now)
	ctx := context.Background()
	id := newUser("a")
	if _, err := users.Update(ctx, id, Patch{ClearDeviceLimit: true}); err != nil {
		t.Fatal(err)
	}
	u, _ := users.Get(ctx, id)
	if u.DeviceLimit.Valid {
		t.Fatal("the test needs a user without a device limit")
	}
	for i := 0; i < MaxDevices; i++ {
		if _, err := devs.Bind(ctx, u, DeviceInfo{HWID: fmt.Sprintf("device-%010d", i)}, false); err != nil {
			t.Fatalf("device %d: %v", i, err)
		}
	}
	if _, err := devs.Bind(ctx, u, DeviceInfo{HWID: "one-too-many-0123"}, false); !errors.Is(err, ErrDeviceLimit) {
		t.Fatalf("device %d of a user without a limit: %v", MaxDevices+1, err)
	}
	// A device that is bound keeps working at the ceiling.
	if _, err := devs.Bind(ctx, u, DeviceInfo{HWID: "device-0000000000"}, false); err != nil {
		t.Fatalf("a bound device at the ceiling: %v", err)
	}
}

// A turned-off or expired user takes no new place: whoever holds the link can ask with any
// id. The answer is the user's own keys (the node refuses them), no slot is spent.
func TestNewDevicesOfUsersWhoCannotConnectTakeNothing(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	devs, users, newUser := newDevices(t, &now)
	ctx := context.Background()
	off := newUser("off")
	gone := newUser("expired")
	yes := true
	if _, err := users.Update(ctx, off, Patch{Disabled: &yes}); err != nil {
		t.Fatal(err)
	}
	past := now.Add(-time.Hour)
	if _, err := users.Update(ctx, gone, Patch{ExpiresAt: &past}); err != nil {
		t.Fatal(err)
	}
	for name, id := range map[string]int64{"disabled": off, "expired": gone} {
		u, _ := users.Get(ctx, id)
		before, _ := devs.st.Q.CountSlotsByState(ctx)
		for i := 0; i < 5; i++ {
			s, err := devs.Bind(ctx, u, DeviceInfo{HWID: fmt.Sprintf("%s-device-%03d", name, i)}, false)
			if err != nil || s.ID != u.SlotID.Int64 {
				t.Fatalf("%s: bind %d: slot %d (own %d) %v", name, i, s.ID, u.SlotID.Int64, err)
			}
		}
		if n, _ := devs.st.Q.CountBoundDevices(ctx, id); n != 0 {
			t.Fatalf("%s: %d devices were recorded", name, n)
		}
		after, _ := devs.st.Q.CountSlotsByState(ctx)
		if fmt.Sprint(before) != fmt.Sprint(after) {
			t.Fatalf("%s: the slot pool changed: %v → %v", name, before, after)
		}
	}
}

// Devices that were sold, lost or reinstalled are forgotten after 90 days and their keys
// burn; the shared place is the user's own slot, which stays.
func TestIdleDevicesAreForgotten(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	devs, users, newUser := newDevices(t, &now)
	ctx := context.Background()
	id := newUser("a")
	if _, err := users.Update(ctx, id, Patch{ClearExpiry: true}); err != nil { // the term must outlast the test
		t.Fatal(err)
	}
	u, _ := users.Get(ctx, id)
	old, err := devs.Bind(ctx, u, DeviceInfo{HWID: "old-phone-0123456"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := devs.Bind(ctx, u, DeviceInfo{App: "clash-verge"}, false); err != nil { // the shared place
		t.Fatal(err)
	}
	now = now.Add(DeviceIdle - 24*time.Hour)
	fresh, err := devs.Bind(ctx, u, DeviceInfo{HWID: "fresh-phone-012345"}, false)
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(48 * time.Hour) // the first two are idle for more than 90 days now
	n, err := devs.ForgetIdle(ctx)
	if err != nil || n != 2 {
		t.Fatalf("forgotten: %d %v, want the phone and the shared place", n, err)
	}
	list, _ := devs.st.Q.ListBoundDevices(ctx, id)
	if len(list) != 1 || list[0].SlotID != fresh.ID {
		t.Fatalf("devices left: %+v", list)
	}
	if s, _ := devs.st.Q.GetSlot(ctx, old.ID); s.State != "burned" {
		t.Fatalf("the idle device's keys: %s, want burned", s.State)
	}
	if s, _ := devs.st.Q.GetSlot(ctx, u.SlotID.Int64); s.State != "assigned" {
		t.Fatalf("the user's own slot: %s, want it kept", s.State)
	}
	if n, _ := devs.ForgetIdle(ctx); n != 0 {
		t.Fatalf("a second run forgot %d", n)
	}
}
