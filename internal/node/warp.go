package node

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"

	C "github.com/metacubex/mihomo/constant"
	"github.com/metacubex/mihomo/tunnel"

	"mikan/internal/nodeapi"
)

// warpProxy is the WARP outbound's name in mihomo.
const warpProxy = "WARP"

// warpProxyConfig is the WireGuard outbound to Cloudflare WARP.
func warpProxyConfig(w *nodeapi.Warp) (map[string]any, error) {
	host, portStr, err := net.SplitHostPort(w.Endpoint)
	port, perr := strconv.Atoi(portStr)
	if err != nil || perr != nil || host == "" || port <= 0 || port > 65535 {
		return nil, fmt.Errorf("warp endpoint %q", w.Endpoint)
	}
	if _, err := netip.ParseAddr(w.IPv4); err != nil {
		return nil, fmt.Errorf("warp ipv4 %q", w.IPv4)
	}
	mtu := w.MTU
	if mtu <= 0 {
		mtu = 1280
	}
	p := map[string]any{
		"name": warpProxy, "type": "wireguard", "server": host, "port": port,
		"ip": w.IPv4, "private-key": w.PrivateKey, "public-key": w.PeerPublicKey,
		"allowed-ips": []string{"0.0.0.0/0", "::/0"}, "udp": true, "mtu": mtu,
		// Names are resolved inside WARP: the server's resolver is not asked about them.
		"remote-dns-resolve": true, "dns": []string{"1.1.1.1", "1.0.0.1"},
	}
	if w.IPv6 != "" {
		if _, err := netip.ParseAddr(w.IPv6); err != nil {
			return nil, fmt.Errorf("warp ipv6 %q", w.IPv6)
		}
		p["ipv6"] = w.IPv6
	}
	if len(w.Reserved) == 3 {
		p["reserved"] = []int{int(w.Reserved[0]), int(w.Reserved[1]), int(w.Reserved[2])}
	}
	return p, nil
}

// safeRuleValue keeps a value from splitting a rule line.
func safeRuleValue(s string) bool {
	return s != "" && !strings.ContainsAny(s, ", \t\r\n")
}

// warpRules send the listed inbounds, domains and networks to WARP. They come after the
// REJECT rules, so WARP never reaches what a direct connection may not.
func warpRules(st nodeapi.DesiredState) []string {
	w := st.Warp
	if w == nil {
		return nil
	}
	present := listenerNames(st)
	viaExit := map[string]bool{} // an inbound sent to another node does not use WARP here
	for _, e := range st.Exits {
		for _, n := range e.Inbounds {
			viaExit[n] = true
		}
	}
	var r []string
	for _, name := range w.Inbounds {
		if present[name] && !viaExit[name] && safeRuleValue(name) {
			r = append(r, "IN-NAME,"+name+","+warpProxy)
		}
	}
	for _, d := range w.Domains {
		if safeRuleValue(d) {
			r = append(r, "DOMAIN-SUFFIX,"+strings.ToLower(d)+","+warpProxy)
		}
	}
	for _, c := range w.CIDRs {
		p, err := netip.ParsePrefix(c)
		if err != nil {
			continue
		}
		kind := "IP-CIDR"
		if p.Addr().Is6() {
			kind = "IP-CIDR6"
		}
		r = append(r, kind+","+p.Masked().String()+","+warpProxy+",no-resolve")
	}
	return r
}

// probe asks Cloudflare's trace page through an outbound (WARP, NODE-<id>) what it sees.
func probe(ctx context.Context, proxy string) nodeapi.WarpStatus {
	st := nodeapi.WarpStatus{Configured: true, CheckedAt: time.Now().UTC()}
	p, ok := tunnel.Proxies()[proxy]
	if !ok {
		st.Error = "not_loaded"
		return st
	}
	hc := &http.Client{Timeout: 15 * time.Second, Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, addr string) (net.Conn, error) {
			host, portStr, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			port, _ := strconv.Atoi(portStr)
			return p.DialContext(ctx, &C.Metadata{NetWork: C.TCP, Host: host, DstPort: uint16(port)})
		},
		TLSClientConfig:   &tls.Config{MinVersion: tls.VersionTLS12},
		DisableKeepAlives: true,
	}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://www.cloudflare.com/cdn-cgi/trace", nil)
	if err != nil {
		st.Error = err.Error()
		return st
	}
	resp, err := hc.Do(req)
	if err != nil {
		st.Error = "unreachable"
		return st
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	for _, line := range strings.Split(string(body), "\n") {
		k, v, _ := strings.Cut(strings.TrimSpace(line), "=")
		switch k {
		case "ip":
			st.IP = v
		case "warp":
			st.Warp = v
		case "colo":
			st.Colo = v
		}
	}
	st.OK = resp.StatusCode == http.StatusOK && st.IP != ""
	if !st.OK {
		st.Error = "bad_answer"
	}
	return st
}
