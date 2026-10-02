package subs

import (
	"reflect"
	"testing"

	"mikan/internal/panel/domain"
	"mikan/internal/panel/store/db"
	"mikan/internal/proto"
)

func TestDetectApp(t *testing.T) {
	for ua, want := range map[string]App{
		"koala-clash/1.4.1":                {Family: FamilyMihomo, Core: Version{1, 19, 30}},
		"koala-clash/1.3.1":                {Family: FamilyMihomo, Core: Version{1, 19, 23}},
		"mihomo/1.19.31":                   {Family: FamilyMihomo, Core: Version{1, 19, 31}},
		"clash.meta/v1.19.12":              {Family: FamilyMihomo, Core: Version{1, 19, 12}},
		"clash-verge/v2.2.3":               {Family: FamilyMihomo},
		"FlClashX/v0.9.1 Platform/android": {Family: FamilyMihomo},
		"ClashMetaForAndroid/2.11.19.Meta": {Family: FamilyMihomo},
		"Happ/3.4.1":                       {Family: FamilyXray},
		"v2rayNG/1.10.20":                  {Family: FamilyXray},
		"INCY/2.1.0":                       {Family: FamilyXray},
		"HiddifyNext/2.5.7 (android) like ClashMeta v2ray sing-box": {Family: FamilySingBox, Legacy: true},
		"karing/1.1.4.920":                       {Family: FamilySingBox},
		"SFA/1.12.4":                             {Family: FamilySingBox},
		"Stash/2.8.0 Clash/1.11.0":               {Family: FamilyStash},
		"Shadowrocket/2592 CFNetwork/1568.100.1": {Family: FamilyOther},
		"Streisand/1.6.44":                       {Family: FamilyOther},
		"":                                       {Family: FamilyOther},
	} {
		if got := DetectApp(ua); got != want {
			t.Errorf("%q: %+v, want %+v", ua, got, want)
		}
	}
}

func TestSupports(t *testing.T) {
	xhttp := proto.Needs{Type: "vless", Transport: "xhttp"}
	pq := proto.Needs{Type: "vless", Transport: "xhttp", Encryption: true}
	vision := proto.Needs{Type: "vless", Transport: "tcp"}
	tuic, anytls := proto.Needs{Type: "tuic"}, proto.Needs{Type: "anytls"}
	tt, sq := proto.Needs{Type: "trusttunnel"}, proto.Needs{Type: "shadowquic"}
	ss, snell := proto.Needs{Type: "shadowsocks"}, proto.Needs{Type: "snell"}
	cases := []struct {
		ua   string
		yes  []proto.Needs
		no   []proto.Needs
		note string
	}{
		{"mihomo/1.19.31", []proto.Needs{xhttp, pq, vision, tuic, anytls, tt, sq, ss, snell}, nil, "a current core gets everything"},
		{"koala-clash/1.3.1", []proto.Needs{xhttp, pq, tt}, []proto.Needs{sq}, "ShadowQUIC came with mihomo 1.19.29"},
		{"clash.meta/v1.19.12", []proto.Needs{vision, tuic, anytls}, []proto.Needs{xhttp, pq, tt}, "an old core: no XHTTP, PQ or newer types"},
		{"clash-verge/v2.2.3", []proto.Needs{xhttp, pq, tuic, anytls, ss, snell}, []proto.Needs{tt, sq}, "an unknown core: the newer types would break the profile"},
		{"Happ/3.4.1", []proto.Needs{xhttp, pq, vision, ss, {Type: "hysteria2"}}, []proto.Needs{tuic, anytls, tt, snell}, "Xray has no TUIC or AnyTLS"},
		{"karing/1.1.4", []proto.Needs{vision, tuic, anytls, ss}, []proto.Needs{xhttp, pq, tt}, "sing-box has no XHTTP or VLESS Encryption"},
		{"HiddifyNext/2.5.7 like ClashMeta", []proto.Needs{vision, tuic}, []proto.Needs{anytls, xhttp}, "Hiddify's sing-box predates AnyTLS"},
		{"Stash/2.8.0", []proto.Needs{vision, tuic, snell}, []proto.Needs{tt, sq}, "Stash reads its own flavor of the newer types"},
		{"Shadowrocket/2592", []proto.Needs{xhttp, tuic, anytls, ss}, []proto.Needs{tt, snell}, "links only"},
	}
	for _, c := range cases {
		app := DetectApp(c.ua)
		for _, n := range c.yes {
			if !app.Supports(n) {
				t.Errorf("%s (%s): must get %+v", c.ua, c.note, n)
			}
		}
		for _, n := range c.no {
			if app.Supports(n) {
				t.Errorf("%s (%s): must not get %+v", c.ua, c.note, n)
			}
		}
	}
	if got := AppsFor(tt); !reflect.DeepEqual(got, []Family{FamilyMihomo}) {
		t.Errorf("TrustTunnel apps: %v", got)
	}
	if got := AppsFor(ss); !reflect.DeepEqual(got, []Family{FamilyMihomo, FamilyXray, FamilySingBox, FamilyStash, FamilyOther}) {
		t.Errorf("Shadowsocks apps: %v", got)
	}
}

// A key shared by everyone reaches only users whose access is on: the node cannot cut
// anyone off on such an inbound.
func TestForApp(t *testing.T) {
	ins := []db.Inbound{
		{ID: 1, Config: "type: tuic\n"},
		{ID: 2, Config: "type: shadowsocks\ncipher: 2022-blake3-aes-128-gcm\npassword: AAAAAAAAAAAAAAAAAAAAAA==\n"},
		{ID: 3, Config: "type: trusttunnel\n"},
	}
	ids := func(list []db.Inbound) []int64 {
		out := []int64{}
		for _, in := range list {
			out = append(out, in.ID)
		}
		return out
	}
	for _, c := range []struct {
		ua, state string
		want      []int64
	}{
		{"mihomo/1.19.31", domain.StateActive, []int64{1, 2, 3}},
		{"mihomo/1.19.31", domain.StateExpired, []int64{1, 3}},
		{"Happ/3.4.1", domain.StateExpiring, []int64{2}},
		{"Happ/3.4.1", domain.StateDisabled, []int64{}},
	} {
		if got := ids(forApp(ins, DetectApp(c.ua), c.state)); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s, %s: %v, want %v", c.ua, c.state, got, c.want)
		}
	}
}

// Gecko goes only to mihomo apps that name a core of 1.19.26 or later: an older core
// fails the whole profile on it, and the other cores got it too late to tell apart.
func TestGeckoOnlyForNewMihomo(t *testing.T) {
	g := proto.Needs{Type: "hysteria2", Gecko: true}
	for ua, want := range map[string]bool{
		"mihomo/1.19.26":          true,
		"clash.meta/v1.19.31":     true,
		"koala-clash/1.4.2":       true,
		"mihomo/1.19.25":          false,
		"koala-clash/1.2.0":       false,
		"clash-verge/v2.4.0":      false, // the core is not named
		"Happ/3.4.1":              false,
		"Hiddify/2.5.7":           false,
		"Karing/1.1":              false,
		"Stash/3.0":               false,
		"Shadowrocket/2070 CFNet": false,
	} {
		if got := DetectApp(ua).Supports(g); got != want {
			t.Errorf("%s: %v, want %v", ua, got, want)
		}
	}
	if !DetectApp("Happ/3.4.1").Supports(proto.Needs{Type: "hysteria2"}) {
		t.Error("plain Hysteria2 must still reach Xray apps")
	}
	if fams := AppsFor(g); len(fams) != 1 || fams[0] != FamilyMihomo {
		t.Errorf("apps for Gecko: %v", fams)
	}
}
