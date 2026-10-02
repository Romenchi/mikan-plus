// Package scan checks sites as REALITY targets. The node borrows the target's TLS
// handshake for every probe of its port, so a usable target speaks TLS 1.3 with X25519
// and HTTP/2 and has a valid certificate for the name clients send. Neighbors looks for
// such sites next to the server's IP (the XTLS RealiTLScanner approach): the node's
// traffic then looks like traffic to a site in the same network, not like a Microsoft
// server in a random data center.
package scan

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/netip"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"mikan/internal/proto"
)

type Result struct {
	Dest      string `json:"dest" doc:"host:port, который будет набирать нода"`
	SNI       string `json:"sni" doc:"Имя сервера, которое отправят клиенты"`
	IP        string `json:"ip"`
	RTTms     int    `json:"rtt_ms"`
	TLS13     bool   `json:"tls13"`
	H2        bool   `json:"h2"`
	X25519    bool   `json:"x25519"`
	CertValid bool   `json:"cert_valid"`
	Issuer    string `json:"issuer,omitempty"`
	Error     string `json:"error,omitempty"`
	DNSMatch  bool   `json:"dns_match" doc:"Имя резолвится в этот IP: сайт настоящий, а не чужая маскировка"`
	OK        bool   `json:"ok" doc:"Подходит как цель REALITY"`
}

const (
	dialTimeout      = 3 * time.Second
	handshakeTimeout = 5 * time.Second
)

// Options say where a check may connect. The server dials what the admin names, so a
// name that leads to this host or its network (127.0.0.1.nip.io, a rebinding name) must
// not be dialed, whatever its text looks like.
type Options struct {
	// LoopbackPort allows 127.0.0.1:<port> alone: the panel's own HTTPS (self-steal).
	LoopbackPort int
	// Any allows every address: tests and test setups.
	Any bool
	// Resolve looks a name up; nil asks the system's resolver for IPv4.
	Resolve func(ctx context.Context, host string) ([]netip.Addr, error)
}

func (o Options) lookup(ctx context.Context, host string) ([]netip.Addr, error) {
	if o.Resolve != nil {
		return o.Resolve(ctx, host)
	}
	return net.DefaultResolver.LookupNetIP(ctx, "ip4", host)
}

// allowed: may the check connect to ip on port?
func (o Options) allowed(ip netip.Addr, port string) bool {
	if o.Any || proto.PublicAddr(ip) {
		return true
	}
	return o.LoopbackPort > 0 && ip == netip.MustParseAddr("127.0.0.1") && port == strconv.Itoa(o.LoopbackPort)
}

// Check tests dest as a REALITY target for clients sending sni ("" = the host of dest). It
// connects only to addresses o allows, and to the very address it checked: the name is
// resolved once.
func Check(ctx context.Context, dest, sni string, o Options) Result {
	r := Result{Dest: dest, SNI: sni}
	host, port, err := net.SplitHostPort(dest)
	if err != nil {
		r.Error = "bad_dest"
		return r
	}
	if r.SNI == "" {
		if _, err := netip.ParseAddr(host); err == nil {
			r.Error = "sni_required"
			return r
		}
		r.SNI = host
	}
	var ip netip.Addr
	if a, err := netip.ParseAddr(host); err == nil {
		ip = a
	} else {
		addrs, err := o.lookup(ctx, host)
		if err != nil || len(addrs) == 0 {
			r.Error = "dns"
			return r
		}
		// The first address the check may use: a name with a private address among its
		// answers is still dialed on a public one, never on the private one.
		// A name never leads to loopback, not even to the panel's own port: that is for the literal address.
		i := slices.IndexFunc(addrs, func(a netip.Addr) bool { return o.Any || proto.PublicAddr(a) })
		if i < 0 {
			r.Error = "private"
			return r
		}
		ip = addrs[i]
	}
	if !o.allowed(ip, port) {
		r.Error = "private"
		return r
	}
	r.IP = ip.String()
	addr := net.JoinHostPort(ip.String(), port)

	// First with X25519 only: REALITY clients offer it, a target without it breaks them.
	cs, rtt, err := handshake(ctx, addr, r.SNI, []tls.CurveID{tls.X25519})
	if err == nil {
		r.X25519 = true
	} else if cs, rtt, err = handshake(ctx, addr, r.SNI, nil); err != nil {
		r.Error = classify(err)
		return r
	}
	r.RTTms = int(rtt.Milliseconds())
	r.TLS13 = cs.Version == tls.VersionTLS13
	r.H2 = cs.NegotiatedProtocol == "h2"
	if len(cs.PeerCertificates) > 0 {
		leaf := cs.PeerCertificates[0]
		r.Issuer = leaf.Issuer.CommonName
		pool := x509.NewCertPool()
		for _, c := range cs.PeerCertificates[1:] {
			pool.AddCert(c)
		}
		_, verr := leaf.Verify(x509.VerifyOptions{DNSName: r.SNI, Intermediates: pool})
		r.CertValid = verr == nil
	}
	r.DNSMatch = resolvesTo(ctx, r.SNI, r.IP, o)
	r.OK = r.TLS13 && r.H2 && r.X25519 && r.CertValid
	return r
}

// handshake returns the negotiated state and the TCP connect time. Certificates are
// checked by the caller: a target with a bad certificate still tells what it supports.
func handshake(ctx context.Context, addr, sni string, curves []tls.CurveID) (tls.ConnectionState, time.Duration, error) {
	start := time.Now()
	d := net.Dialer{Timeout: dialTimeout}
	c, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return tls.ConnectionState{}, 0, err
	}
	rtt := time.Since(start)
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(handshakeTimeout))
	t := tls.Client(c, &tls.Config{ServerName: sni, InsecureSkipVerify: true, NextProtos: []string{"h2", "http/1.1"}, CurvePreferences: curves})
	if err := t.HandshakeContext(ctx); err != nil {
		return tls.ConnectionState{}, rtt, err
	}
	return t.ConnectionState(), rtt, nil
}

func resolvesTo(ctx context.Context, name, ip string, o Options) bool {
	if _, err := netip.ParseAddr(name); err == nil {
		return name == ip
	}
	addrs, err := o.lookup(ctx, name)
	if err != nil {
		return false
	}
	for _, a := range addrs {
		if a.String() == ip {
			return true
		}
	}
	return false
}

func classify(err error) string {
	var ne net.Error
	switch {
	case errors.As(err, &ne) && ne.Timeout():
		return "timeout"
	case strings.Contains(err.Error(), "refused"):
		return "refused"
	case strings.Contains(err.Error(), "protocol version"):
		return "no_tls13"
	}
	return "handshake"
}

// Neighbors scans the /24 around ip for usable targets and returns the best ones by RTT.
func Neighbors(ctx context.Context, ip string, limit int, o Options) (found []Result, scanned int, err error) {
	self, err := netip.ParseAddr(ip)
	if err != nil || !self.Is4() {
		return nil, 0, errors.New("need an IPv4 address")
	}
	// It connects to a /24 around ip: never the neighborhood of this host's own network.
	if !o.Any && !proto.PublicAddr(self) {
		return nil, 0, errors.New("need a public IPv4 address")
	}
	prefix, _ := self.Prefix(24)
	var (
		mu   sync.Mutex
		wg   sync.WaitGroup
		sem  = make(chan struct{}, 64)
		seen = map[string]bool{}
	)
	for a := prefix.Addr(); prefix.Contains(a); a = a.Next() {
		last := a.As4()[3]
		if a == self || last == 0 || last == 255 {
			continue
		}
		scanned++
		wg.Add(1)
		go func(a netip.Addr) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-sem }()
			name := certName(ctx, a.String())
			if name == "" {
				return
			}
			r := Check(ctx, net.JoinHostPort(a.String(), "443"), name, Options{Any: o.Any})
			// Shared VPS networks are full of other people's REALITY servers: probed without
			// a valid client they relay www.samsung.com or yahoo.com. Such a name does not
			// resolve to that address; a real neighbor site does.
			if !r.OK || !r.DNSMatch {
				return
			}
			mu.Lock()
			if !seen[r.SNI] {
				seen[r.SNI] = true
				found = append(found, r)
			}
			mu.Unlock()
		}(a)
	}
	wg.Wait()
	sort.Slice(found, func(i, j int) bool { return found[i].RTTms < found[j].RTTms })
	if limit > 0 && len(found) > limit {
		found = found[:limit]
	}
	return found, scanned, ctx.Err()
}

// certName asks an IP for its default certificate (no SNI) and returns a concrete name
// from it; wildcards cannot be sent as SNI.
func certName(ctx context.Context, ip string) string {
	d := net.Dialer{Timeout: 2 * time.Second}
	c, err := d.DialContext(ctx, "tcp", net.JoinHostPort(ip, "443"))
	if err != nil {
		return ""
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(3 * time.Second))
	t := tls.Client(c, &tls.Config{InsecureSkipVerify: true, NextProtos: []string{"h2", "http/1.1"}})
	if t.HandshakeContext(ctx) != nil {
		return ""
	}
	certs := t.ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return ""
	}
	names := append([]string{}, certs[0].DNSNames...)
	if cn := certs[0].Subject.CommonName; cn != "" {
		names = append(names, cn)
	}
	for _, n := range names {
		if !strings.Contains(n, "*") && strings.Contains(n, ".") && net.ParseIP(n) == nil {
			return strings.ToLower(n)
		}
	}
	return ""
}
