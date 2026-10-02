package proto

import "testing"

func TestPublicHost(t *testing.T) {
	for host, want := range map[string]bool{
		"www.example.org":     true,
		"www.example.org.":    true,
		"203.0.113.20":        true,
		"2001:db8::1":         true,
		"localhost":           false,
		"localhost.":          false,
		"LOCALHOST":           false,
		"LocalHost.":          false,
		"app.localhost":       false,
		"app.localhost.":      false,
		"intranet":            false,
		"127.0.0.1":           false,
		"[::1]":               false,
		"::ffff:127.0.0.1":    false,
		"10.1.2.3":            false,
		"192.168.0.1":         false,
		"172.16.0.1":          false,
		"169.254.169.254":     false,
		"100.64.0.1":          false,
		"0.0.0.0":             false,
		"224.0.0.1":           false,
		"fe80::1":             false,
		"fd00::1":             false,
		"ff02::1":             false,
		"::ffff:169.254.1.1":  false,
		"Example.COM.":        true,
		"8.8.8.8":             true,
		"[2606:4700::1111]":   true,
		"127.0.0.1.":          false,
		"::1":                 false,
		"":                    false,
		"2001:db8::1%25eth0x": true, // not an address: a name, which the dialer resolves and checks
	} {
		if got := PublicHost(host); got != want {
			t.Errorf("PublicHost(%q) = %v, want %v", host, got, want)
		}
	}
}
