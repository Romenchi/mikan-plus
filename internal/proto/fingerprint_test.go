package proto

import (
	"net/url"
	"strings"
	"testing"
)

// The inbound's own fingerprint wins over the panel's default, which wins over chrome; an
// own value must have the shape of a profile name, or it never reaches apps.
func TestFingerprintPrecedence(t *testing.T) {
	priv, _ := realityKey(t)
	base := "type: vless\nreality-config:\n  dest: www.microsoft.com:443\n  private-key: " + priv + "\n  short-id: [a1b2]\n  server-names: [www.microsoft.com]\n"
	cases := []struct{ own, panel, want string }{
		{"", "", "chrome"},
		{"", "firefox", "firefox"},
		{"safari", "firefox", "safari"},
		{"chrome120", "ios", "chrome120"}, // an own value of the right shape is used
		{"Chrome 1,2", "ios", "ios"},      // a malformed one never reaches apps: the default
		{"", "bad value!", "chrome"},      // a malformed default: chrome
		{"randomized", "", "randomized"},
	}
	for _, c := range cases {
		src := base
		if c.own != "" {
			src += "mikan:\n  client:\n    fingerprint: " + c.own + "\n"
		}
		cl, err := ClientConfig(mustParse(t, src), ClientInput{Name: "X", Host: "203.0.113.7", Port: 443, Fingerprint: c.panel, Slot: slots[0]})
		if err != nil {
			t.Fatal(err)
		}
		u, _ := url.Parse(cl.URI)
		if u.Query().Get("fp") != c.want || cl.Mihomo["client-fingerprint"] != c.want {
			t.Errorf("own %q panel %q: fp=%s client-fingerprint=%v, want %s", c.own, c.panel, u.Query().Get("fp"), cl.Mihomo["client-fingerprint"], c.want)
		}
	}
}

// Node-certificate TLS gets the fingerprint too; QUIC protocols have no uTLS and no fp.
func TestFingerprintByTransport(t *testing.T) {
	in := ClientInput{Name: "X", Host: "vpn.example.com", Port: 2053, SNI: "vpn.example.com", Fingerprint: "edge", Slot: slots[0]}
	trojan := mustParse(t, "type: trojan\nws-path: /ws\nmikan:\n  tls: node\n")
	cl, err := ClientConfig(trojan, in)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(cl.URI)
	if u.Query().Get("fp") != "edge" || cl.Mihomo["client-fingerprint"] != "edge" || !UsesFingerprint(trojan) {
		t.Fatalf("trojan tls: %s %v", cl.URI, cl.Mihomo)
	}
	hy2 := mustParse(t, "type: hysteria2\n")
	cl, err = ClientConfig(hy2, in)
	if err != nil {
		t.Fatal(err)
	}
	u, _ = url.Parse(cl.URI)
	if u.Query().Has("fp") || cl.Mihomo["client-fingerprint"] != nil || UsesFingerprint(hy2) {
		t.Fatalf("hysteria2 has no uTLS: %s %v", cl.URI, cl.Mihomo)
	}
	// AnyTLS links carry no fp (the scheme has none), the mihomo profile does.
	any := mustParse(t, "type: anytls\n")
	cl, err = ClientConfig(any, in)
	if err != nil {
		t.Fatal(err)
	}
	if cl.Mihomo["client-fingerprint"] != "edge" || !UsesFingerprint(any) {
		t.Fatalf("anytls: %v", cl.Mihomo)
	}
}

func TestSetFingerprint(t *testing.T) {
	tpl := mustParse(t, "type: anytls\n")
	if err := SetFingerprint(tpl, "ios"); err != nil {
		t.Fatal(err)
	}
	if got := mustParse(t, Marshal(tpl)).Ext().Client.Fingerprint; got != "ios" {
		t.Fatalf("set: %q\n%s", got, Marshal(tpl))
	}
	for _, bad := range []string{"Chrome", "ios\nx: 1", "a,DIRECT", "chrome 120", strings.Repeat("a", 33)} {
		if code(SetFingerprint(tpl, bad)) != "config_fingerprint" {
			t.Fatalf("%q: malformed fingerprints must be refused", bad)
		}
	}
	// An own value of the right shape is the admin's call.
	if err := SetFingerprint(tpl, "randomizednoalpn"); err != nil {
		t.Fatal(err)
	}
	// Clearing drops the empty sections, other overrides stay.
	if err := SetFingerprint(tpl, ""); err != nil {
		t.Fatal(err)
	}
	if _, ok := tpl[extKey]; ok {
		t.Fatalf("empty mikan section left: %v", tpl)
	}
	tpl = mustParse(t, "type: anytls\nmikan:\n  client:\n    sni: cdn.example.com\n    fingerprint: qq\n")
	if err := SetFingerprint(tpl, ""); err != nil {
		t.Fatal(err)
	}
	if e := tpl.Ext(); e.Client.SNI != "cdn.example.com" || e.Client.Fingerprint != "" {
		t.Fatalf("clear: %+v", e)
	}
	if err := Validate(tpl, Options{}); err != nil {
		t.Fatal(err)
	}
}
