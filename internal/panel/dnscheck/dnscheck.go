// Package dnscheck asks public resolvers, over HTTPS, where a domain points. The server's
// own resolver and /etc/hosts may say otherwise, and a domain is taken for the panel or a
// node only when the world sends its clients there: a domain of someone else's server
// would put its address into every subscription.
package dnscheck

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"slices"
	"strings"
	"time"
)

// Resolvers are the public DNS-over-HTTPS resolvers asked in turn (JSON API).
var Resolvers = []string{"https://cloudflare-dns.com/dns-query", "https://dns.google/resolve"}

var (
	// ErrNotFound: the domain has no A or AAAA record.
	ErrNotFound = errors.New("domain_not_found")
	// ErrUnknownServer: the server's own public address is not known, so nothing can be
	// compared.
	ErrUnknownServer = errors.New("server_ip_unknown")
)

// Elsewhere is a domain with addresses that are not the server's.
type Elsewhere struct{ Foreign []netip.Addr }

func (e *Elsewhere) Error() string { return "domain_elsewhere: " + Join(e.Foreign) }

// Join lists addresses for a message: "198.51.100.7, 2001:db8::1".
func Join(as []netip.Addr) string {
	s := make([]string, len(as))
	for i, a := range as {
		s[i] = a.String()
	}
	return strings.Join(s, ", ")
}

type Checker struct {
	Resolvers []string
	HTTP      *http.Client
}

func New() *Checker {
	return &Checker{Resolvers: Resolvers, HTTP: &http.Client{Timeout: 8 * time.Second}}
}

// resolverTimeout is how long one public resolver gets: where DoH is blocked it is the
// time a lookup waits for it, so it is short and the resolvers are asked at once.
const resolverTimeout = 3 * time.Second

// Lookup is the domain's A and AAAA records as the first public resolver that answers
// sees them. When none answers (an outbound filter), the system resolver is asked.
func (c *Checker) Lookup(ctx context.Context, name string) ([]netip.Addr, error) {
	type answer struct {
		out []netip.Addr
		err error
	}
	rctx, cancel := context.WithTimeout(ctx, resolverTimeout)
	defer cancel()
	ch := make(chan answer, len(c.Resolvers))
	for _, base := range c.Resolvers {
		go func() {
			var out []netip.Addr
			for _, typ := range []string{"A", "AAAA"} {
				as, err := c.doh(rctx, base, name, typ)
				if err != nil {
					ch <- answer{err: err}
					return
				}
				out = append(out, as...)
			}
			ch <- answer{out: out}
		}()
	}
	var last error
	for range c.Resolvers {
		a := <-ch
		if a.err != nil {
			last = a.err
			continue
		}
		if len(a.out) == 0 {
			return nil, ErrNotFound
		}
		return a.out, nil
	}
	ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", name)
	var dnsErr *net.DNSError
	switch {
	case errors.As(err, &dnsErr) && dnsErr.IsNotFound:
		return nil, ErrNotFound
	case err != nil:
		return nil, fmt.Errorf("dnscheck: %v; system resolver: %w", last, err)
	}
	for i := range ips {
		ips[i] = ips[i].Unmap()
	}
	return ips, nil
}

func (c *Checker) doh(ctx context.Context, base, name, typ string) ([]netip.Addr, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"?name="+url.QueryEscape(name)+"&type="+typ, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/dns-json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: HTTP %d", base, resp.StatusCode)
	}
	var ans struct {
		Status int `json:"Status"`
		Answer []struct {
			Type int    `json:"type"`
			Data string `json:"data"`
		} `json:"Answer"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&ans); err != nil {
		return nil, fmt.Errorf("%s: %w", base, err)
	}
	// 0: found, 3: no such name; anything else (SERVFAIL…) says nothing.
	if ans.Status != 0 && ans.Status != 3 {
		return nil, fmt.Errorf("%s: DNS status %d", base, ans.Status)
	}
	want := map[string]int{"A": 1, "AAAA": 28}[typ]
	var out []netip.Addr
	for _, a := range ans.Answer {
		if ip, err := netip.ParseAddr(a.Data); a.Type == want && err == nil {
			out = append(out, ip.Unmap())
		}
	}
	return out, nil
}

// PointsTo checks that every address of the domain is one of the server's: one address
// elsewhere already sends some clients to another machine.
func (c *Checker) PointsTo(ctx context.Context, name string, own []netip.Addr) error {
	if len(own) == 0 {
		return ErrUnknownServer
	}
	found, err := c.Lookup(ctx, name)
	if err != nil {
		return err
	}
	var foreign []netip.Addr
	for _, a := range found {
		if !slices.Contains(own, a) {
			foreign = append(foreign, a)
		}
	}
	if len(foreign) > 0 {
		return &Elsewhere{Foreign: foreign}
	}
	return nil
}

// Own is the server's public addresses: host when it is an IP literal, and the global
// unicast, non-private addresses of its interfaces (the panel runs on the host network).
func Own(host string) []netip.Addr {
	var out []netip.Addr
	if ip, err := netip.ParseAddr(strings.Trim(host, "[]")); err == nil {
		out = append(out, ip.Unmap())
	}
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		p, err := netip.ParsePrefix(a.String())
		if err != nil {
			continue
		}
		if ip := p.Addr().Unmap(); ip.IsGlobalUnicast() && !ip.IsPrivate() && !slices.Contains(out, ip) {
			out = append(out, ip)
		}
	}
	return out
}
