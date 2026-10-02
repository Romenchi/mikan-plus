package proto

import (
	"net/url"
	"testing"
)

// Behind a TCP proxy (GitHub issue #11) clients get the proxy's address, port and TLS
// name; the node keeps listening where it does.
func TestSetClientEndpoint(t *testing.T) {
	tpl := mustParse(t, "type: trojan\nws-path: /ws\nmikan:\n  tls: node\n  client:\n    fingerprint: ios\n")
	if err := SetClientEndpoint(tpl, "vpn.example.com", 443, "trojan.example.com"); err != nil {
		t.Fatal(err)
	}
	got := mustParse(t, Marshal(tpl))
	if c := got.Ext().Client; c.Server != "vpn.example.com" || c.Port != 443 || c.SNI != "trojan.example.com" || c.Fingerprint != "ios" {
		t.Fatalf("set: %+v\n%s", c, Marshal(tpl))
	}
	if err := Validate(got, Options{}); err != nil {
		t.Fatal(err)
	}
	cl, err := ClientConfig(got, ClientInput{Name: "X", Host: "203.0.113.7", Port: 8444, SNI: "node.example.com", Slot: slots[0]})
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(cl.URI)
	if u.Host != "vpn.example.com:443" || u.Query().Get("sni") != "trojan.example.com" {
		t.Fatalf("link: %s", cl.URI)
	}
	if cl.Mihomo["server"] != "vpn.example.com" || cl.Mihomo["port"] != 443 || cl.Mihomo["sni"] != "trojan.example.com" {
		t.Fatalf("profile: %v", cl.Mihomo)
	}

	// Clearing all three keeps the other overrides.
	if err := SetClientEndpoint(tpl, "", 0, ""); err != nil {
		t.Fatal(err)
	}
	if c := tpl.Ext().Client; c != (ClientOverrides{Fingerprint: "ios"}) {
		t.Fatalf("clear: %+v", c)
	}
	if err := SetFingerprint(tpl, ""); err != nil {
		t.Fatal(err)
	}
	if e, ok := tpl[extKey].(map[string]any); !ok || e["client"] != nil || e["tls"] != "node" {
		t.Fatalf("empty client section left: %v", tpl[extKey])
	}

	for _, c := range []struct {
		server, sni string
		port        int
		code        string
	}{
		{"vpn example.com", "", 0, "config_client_server"},
		{"localhost", "", 0, "config_client_server"},
		{"a,DIRECT", "", 0, "config_client_server"},
		{"", "", 65536, "config_client_port"},
		{"", "", -1, "config_client_port"},
		{"", "203.0.113.9", 0, "config_client_sni"}, // SNI is a name, never an address
		{"", "bad_name.example.com", 0, "config_client_sni"},
	} {
		if got := code(SetClientEndpoint(tpl, c.server, c.port, c.sni)); got != c.code {
			t.Errorf("%+v: %q, want %q", c, got, c.code)
		}
	}
	// An IPv6 proxy address is bracketed in links.
	if err := SetClientEndpoint(tpl, "2001:db8::1", 443, ""); err != nil {
		t.Fatal(err)
	}
	cl, _ = ClientConfig(tpl, ClientInput{Name: "X", Host: "203.0.113.7", Port: 8444, Slot: slots[0]})
	if u, _ := url.Parse(cl.URI); u.Host != "[2001:db8::1]:443" {
		t.Fatalf("ipv6: %s", cl.URI)
	}
}

// A REALITY client sends the target's name and the node checks it, so the SNI there is
// not the client side's to change; QUIC protocols on the node certificate take one.
func TestClientSNIByProtocol(t *testing.T) {
	priv, _ := realityKey(t)
	reality := mustParse(t, "type: vless\nreality-config:\n  dest: www.microsoft.com:443\n  private-key: "+priv+"\n  short-id: [a1b2]\n  server-names: [www.microsoft.com]\n")
	if ClientSNI(reality) || code(SetClientEndpoint(reality, "vpn.example.com", 443, "other.example.com")) != "config_client_sni" {
		t.Fatal("REALITY must refuse an SNI of the client side's own")
	}
	if err := SetClientEndpoint(reality, "vpn.example.com", 443, ""); err != nil {
		t.Fatalf("REALITY still takes the address and port: %v", err)
	}
	for src, want := range map[string]bool{"type: hysteria2\n": true, "type: anytls\n": true, "type: mieru\ntransport: TCP\n": false} {
		if got := ClientSNI(mustParse(t, src)); got != want {
			t.Errorf("%q: %v, want %v", src, got, want)
		}
	}
}

// A port of the client side's own is the one clients dial: the node's hopping range would
// point past the proxy.
func TestClientPortDropsHopping(t *testing.T) {
	in := ClientInput{Name: "X", Host: "203.0.113.7", Port: 20000, PortSpec: "20000-20100", SNI: "vpn.example.com", Slot: slots[0]}
	hy2 := mustParse(t, "type: hysteria2\n")
	cl, err := ClientConfig(hy2, in)
	if err != nil {
		t.Fatal(err)
	}
	if cl.Mihomo["ports"] != "20000-20100" {
		t.Fatalf("hopping without an override: %v", cl.Mihomo)
	}
	if err := SetClientEndpoint(hy2, "", 443, ""); err != nil {
		t.Fatal(err)
	}
	cl, err = ClientConfig(hy2, in)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(cl.URI)
	if cl.Mihomo["ports"] != nil || u.Query().Has("mport") || cl.Mihomo["port"] != 443 {
		t.Fatalf("override: %s %v", cl.URI, cl.Mihomo)
	}
}
