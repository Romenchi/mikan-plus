package scan

import (
	"context"
	"errors"
	"net"
	"net/netip"
)

var (
	// ErrNotIPv4: the host is an IP address, but not an IPv4 one.
	ErrNotIPv4 = errors.New("not an IPv4 address")
	// ErrNoIPv4: the name has no IPv4 address.
	ErrNoIPv4 = errors.New("no IPv4 address")
)

// ResolveIPv4 is the IPv4 address of host, an address or a name: the one whose /24 is
// scanned for REALITY targets (Neighbors).
func ResolveIPv4(ctx context.Context, host string) (string, error) {
	if a, err := netip.ParseAddr(host); err == nil {
		if !a.Unmap().Is4() {
			return "", ErrNotIPv4
		}
		return a.Unmap().String(), nil
	}
	addrs, err := net.DefaultResolver.LookupNetIP(ctx, "ip4", host)
	if err != nil || len(addrs) == 0 {
		return "", ErrNoIPv4
	}
	return addrs[0].String(), nil
}

// Problem says why a result does not suit REALITY, as a code: the check's own error
// (timeout, refused, dns…), no_tls13, no_x25519, no_h2, or cert; "" when it suits.
func Problem(r Result) string {
	switch {
	case r.OK:
		return ""
	case r.Error != "":
		return r.Error
	case !r.TLS13:
		return "no_tls13"
	case !r.X25519:
		return "no_x25519"
	case !r.H2:
		return "no_h2"
	default:
		return "cert"
	}
}
