package scan

import (
	"context"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"testing"
)

func resolver(m map[string][]string) func(context.Context, string) ([]netip.Addr, error) {
	return func(_ context.Context, host string) ([]netip.Addr, error) {
		var out []netip.Addr
		for _, a := range m[host] {
			out = append(out, netip.MustParseAddr(a))
		}
		return out, nil
	}
}

// A name that leads to this host or its network is not dialed, whatever its text looks
// like: the syntax check of the host (proto.PublicHost) lets every DNS name through.
func TestCheckRefusesNamesThatLeadInside(t *testing.T) {
	dial := make(chan string, 4)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			dial <- c.RemoteAddr().String()
			c.Close()
		}
	}()
	port := ln.Addr().(*net.TCPAddr).Port
	dest := func(host string) string { return net.JoinHostPort(host, strconv.Itoa(port)) }
	o := Options{Resolve: resolver(map[string][]string{
		"127.0.0.1.nip.io":  {"127.0.0.1"},
		"metadata.attacker": {"169.254.169.254"},
		"inner.attacker":    {"10.1.2.3"},
		"mixed.attacker":    {"10.1.2.3", "127.0.0.1"},
	})}
	ctx := context.Background()
	for _, host := range []string{"127.0.0.1.nip.io", "metadata.attacker", "inner.attacker", "mixed.attacker", "127.0.0.1", "169.254.169.254", "10.0.0.1"} {
		r := Check(ctx, dest(host), "site.example", o)
		if r.Error != "private" || r.IP != "" {
			t.Errorf("%s: %+v, want the check refused as private before any connection", host, r)
		}
	}
	select {
	case from := <-dial:
		t.Fatalf("a connection reached the listener on loopback from %s", from)
	default:
	}

	// The panel's own HTTPS (self-steal) is the one loopback address allowed, as a literal
	// and only on its port.
	self := Options{LoopbackPort: port, Resolve: o.Resolve}
	if r := Check(ctx, dest("127.0.0.1"), "site.example", self); r.Error == "private" {
		t.Fatalf("self-steal refused: %+v", r)
	}
	if r := Check(ctx, dest("127.0.0.1.nip.io"), "site.example", self); r.Error != "private" {
		t.Fatalf("a name leading to loopback passed as self-steal: %+v", r)
	}
	if r := Check(ctx, "127.0.0.1:1", "site.example", self); r.Error != "private" {
		t.Fatalf("another loopback port passed as self-steal: %+v", r)
	}
}

// A name with a public and a private address is checked on the public one, and that very
// address is dialed: the name is not resolved a second time.
func TestCheckPinsTheAddressItChecked(t *testing.T) {
	o := Options{Resolve: func(context.Context, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("10.9.9.9"), netip.MustParseAddr("198.51.100.7")}, nil
	}}
	// The context is over: nothing is dialed, the address is chosen before the dial.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if r := Check(ctx, "mixed.example:443", "mixed.example", o); r.IP != "198.51.100.7" {
		t.Fatalf("checked %q, want the public address", r.IP)
	}
}

func TestNeighborsRefusesPrivateNetworks(t *testing.T) {
	for _, ip := range []string{"10.0.0.5", "192.168.1.5", "127.0.0.1", "169.254.169.254", "100.64.1.1"} {
		if _, _, err := Neighbors(context.Background(), ip, 5, Options{}); err == nil || !strings.Contains(err.Error(), "public") {
			t.Errorf("%s: %v, want a refusal", ip, err)
		}
	}
}
