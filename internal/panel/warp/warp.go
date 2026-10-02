// Package warp gets a node a Cloudflare WARP account: registered through Cloudflare's
// client API the way the WARP app and wgcf do it, or imported from a WireGuard config
// the admin already has. The node then runs it as a WireGuard outbound.
package warp

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// DefaultAPI is Cloudflare's WARP client API.
const DefaultAPI = "https://api.cloudflareclient.com/v0a2158"

// DefaultEndpoint is where WARP listens when the registration names no usable address.
const DefaultEndpoint = "162.159.192.1:2408"

// Account is what the node needs, plus the registration for a WARP+ key.
type Account struct {
	PrivateKey, PeerPublicKey string
	Endpoint                  string // host:port
	IPv4, IPv6                string // without masks
	Reserved                  []byte // WARP's 3-byte client id; may be empty for imports
	MTU                       int
	ID, Token                 string // the registration; empty for imports
	Plus                      bool
}

// Error is a short code for the admin panel; Status is Cloudflare's HTTP status when it
// refused (warp_refused).
type Error struct {
	Code   string
	Status int
}

func (e *Error) Error() string { return e.Code }

type Client struct {
	API string
	HC  *http.Client
}

func (c Client) do(ctx context.Context, method, path, token string, in, out any) error {
	var body io.Reader
	if in != nil {
		raw, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(raw)
	}
	api := c.API
	if api == "" {
		api = DefaultAPI
	}
	req, err := http.NewRequestWithContext(ctx, method, api+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "okhttp/3.12.1")
	req.Header.Set("CF-Client-Version", "a-6.30-3596")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	hc := c.HC
	if hc == nil {
		hc = &http.Client{Timeout: 20 * time.Second}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return &Error{Code: "warp_unreachable"}
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode/100 != 2 {
		// Too many registrations has advice of its own: wait, or bring a config.
		if resp.StatusCode == http.StatusTooManyRequests {
			return &Error{Code: "warp_refused_429"}
		}
		return &Error{Code: "warp_refused", Status: resp.StatusCode}
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return &Error{Code: "warp_bad_answer"}
	}
	return nil
}

type registration struct {
	ID      string `json:"id"`
	Token   string `json:"token"`
	Account struct {
		WarpPlus    bool   `json:"warp_plus"`
		AccountType string `json:"account_type"`
	} `json:"account"`
	Config struct {
		ClientID string `json:"client_id"`
		Peers    []struct {
			PublicKey string `json:"public_key"`
			Endpoint  struct {
				V4   string `json:"v4"`
				Host string `json:"host"`
			} `json:"endpoint"`
		} `json:"peers"`
		Interface struct {
			Addresses struct {
				V4 string `json:"v4"`
				V6 string `json:"v6"`
			} `json:"addresses"`
		} `json:"interface"`
	} `json:"config"`
}

// Register makes a new WARP device with a fresh key pair; license, when set, turns it
// into WARP+.
func (c Client) Register(ctx context.Context, license string) (Account, error) {
	priv, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return Account{}, err
	}
	var reg registration
	err = c.do(ctx, http.MethodPost, "/reg", "", map[string]any{
		"key": base64.StdEncoding.EncodeToString(priv.PublicKey().Bytes()), "install_id": "", "fcm_token": "",
		"tos": time.Now().UTC().Format(time.RFC3339), "model": "PC", "serial_number": "", "locale": "en_US",
	}, &reg)
	if err != nil {
		return Account{}, err
	}
	if reg.ID == "" || reg.Token == "" || len(reg.Config.Peers) == 0 || reg.Config.Interface.Addresses.V4 == "" {
		return Account{}, &Error{Code: "warp_bad_answer"}
	}
	a := Account{
		PrivateKey: base64.StdEncoding.EncodeToString(priv.Bytes()), PeerPublicKey: reg.Config.Peers[0].PublicKey,
		Endpoint: endpoint(reg.Config.Peers[0].Endpoint.V4, reg.Config.Peers[0].Endpoint.Host),
		IPv4:     reg.Config.Interface.Addresses.V4, IPv6: reg.Config.Interface.Addresses.V6,
		MTU: 1280, ID: reg.ID, Token: reg.Token, Plus: reg.Account.WarpPlus,
	}
	if id, err := base64.StdEncoding.DecodeString(reg.Config.ClientID); err == nil && len(id) == 3 {
		a.Reserved = id
	}
	if err := a.Validate(); err != nil {
		return Account{}, err
	}
	if license != "" {
		plus, err := c.SetLicense(ctx, a.ID, a.Token, license)
		if err != nil {
			return a, err
		}
		a.Plus = plus
	}
	return a, nil
}

var licensePattern = regexp.MustCompile(`^[A-Za-z0-9]{8}-[A-Za-z0-9]{8}-[A-Za-z0-9]{8}$`)

// SetLicense applies a WARP+ key to a registration and says whether it is WARP+ now.
func (c Client) SetLicense(ctx context.Context, id, token, license string) (bool, error) {
	if !licensePattern.MatchString(license) {
		return false, &Error{Code: "warp_license_format"}
	}
	if id == "" || token == "" || strings.ContainsAny(id, "/?#") {
		return false, &Error{Code: "warp_not_registered"}
	}
	if err := c.do(ctx, http.MethodPut, "/reg/"+id+"/account", token, map[string]string{"license": license}, nil); err != nil {
		return false, err
	}
	var reg registration
	if err := c.do(ctx, http.MethodGet, "/reg/"+id, token, nil, &reg); err != nil {
		return false, err
	}
	return reg.Account.WarpPlus, nil
}

// endpoint prefers WARP's IP (a DNS answer for its host may be filtered) on port 2408.
func endpoint(v4, host string) string {
	if h, _, err := net.SplitHostPort(v4); err == nil {
		if a, err := netip.ParseAddr(h); err == nil && a.Is4() {
			return net.JoinHostPort(h, "2408")
		}
	}
	if _, port, err := net.SplitHostPort(host); err == nil && port != "0" {
		return host
	}
	return DefaultEndpoint
}

// Validate checks what the node will be given.
func (a Account) Validate() error {
	for _, k := range []string{a.PrivateKey, a.PeerPublicKey} {
		if raw, err := base64.StdEncoding.DecodeString(k); err != nil || len(raw) != 32 {
			return &Error{Code: "warp_bad_key"}
		}
	}
	host, port, err := net.SplitHostPort(a.Endpoint)
	if p, perr := strconv.Atoi(port); err != nil || perr != nil || host == "" || p <= 0 || p > 65535 || strings.ContainsAny(host, ", ") {
		return &Error{Code: "warp_bad_endpoint"}
	}
	if v4, err := netip.ParseAddr(a.IPv4); err != nil || !v4.Is4() {
		return &Error{Code: "warp_bad_address"}
	}
	if a.IPv6 != "" {
		if v6, err := netip.ParseAddr(a.IPv6); err != nil || !v6.Is6() {
			return &Error{Code: "warp_bad_address"}
		}
	}
	if len(a.Reserved) != 0 && len(a.Reserved) != 3 {
		return &Error{Code: "warp_bad_reserved"}
	}
	if a.MTU != 0 && (a.MTU < 1100 || a.MTU > 1500) {
		return &Error{Code: "warp_bad_mtu"}
	}
	return nil
}

// ParseConf reads a WireGuard config for WARP (wgcf, warp-plus or the WARP app's
// export): [Interface] PrivateKey, Address, MTU; [Peer] PublicKey, Endpoint; and an
// optional "Reserved = a, b, c".
func ParseConf(text string) (Account, error) {
	a := Account{MTU: 1280}
	sc := bufio.NewScanner(strings.NewReader(text))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") || strings.HasPrefix(line, "[") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k, v = strings.ToLower(strings.TrimSpace(k)), strings.TrimSpace(v)
		switch k {
		case "privatekey":
			a.PrivateKey = v
		case "publickey":
			a.PeerPublicKey = v
		case "endpoint":
			a.Endpoint = v
		case "mtu":
			a.MTU, _ = strconv.Atoi(v)
		case "address":
			for _, part := range strings.Split(v, ",") {
				pfx, err := netip.ParsePrefix(strings.TrimSpace(part))
				if err != nil {
					addr, err2 := netip.ParseAddr(strings.TrimSpace(part))
					if err2 != nil {
						return Account{}, &Error{Code: "warp_bad_address"}
					}
					pfx = netip.PrefixFrom(addr, addr.BitLen())
				}
				if pfx.Addr().Is4() {
					a.IPv4 = pfx.Addr().String()
				} else {
					a.IPv6 = pfx.Addr().String()
				}
			}
		case "reserved":
			var r []byte
			for _, part := range strings.Split(v, ",") {
				n, err := strconv.Atoi(strings.TrimSpace(part))
				if err != nil || n < 0 || n > 255 {
					return Account{}, &Error{Code: "warp_bad_reserved"}
				}
				r = append(r, byte(n))
			}
			a.Reserved = r
		}
	}
	if a.PrivateKey == "" || a.PeerPublicKey == "" || a.IPv4 == "" {
		return Account{}, &Error{Code: "warp_conf_incomplete"}
	}
	if a.Endpoint == "" {
		a.Endpoint = DefaultEndpoint
	}
	if err := a.Validate(); err != nil {
		return Account{}, err
	}
	return a, nil
}

// Routes are the domain suffixes and networks that go through WARP for every inbound.
type Routes struct {
	Domains []string
	CIDRs   []string
}

const maxRoutes = 1000

var domainPattern = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)*[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// ParseRoutes reads one entry per line (commas and spaces work too): example.com covers
// its subdomains, "*.example.com" and ".example.com" mean the same; 104.16.0.0/13 or a
// bare address is a network. bad lists what was not understood.
func ParseRoutes(lines []string) (r Routes, bad []string) {
	seen := map[string]bool{}
	for _, raw := range lines {
		for _, item := range strings.FieldsFunc(raw, func(c rune) bool { return c == ',' || c == ' ' || c == '\t' || c == '\n' || c == '\r' }) {
			item = strings.ToLower(strings.TrimSpace(item))
			if item == "" || seen[item] {
				continue
			}
			seen[item] = true
			if p, err := netip.ParsePrefix(item); err == nil {
				r.CIDRs = append(r.CIDRs, p.Masked().String())
				continue
			}
			if a, err := netip.ParseAddr(item); err == nil {
				r.CIDRs = append(r.CIDRs, netip.PrefixFrom(a, a.BitLen()).String())
				continue
			}
			d := strings.TrimPrefix(strings.TrimPrefix(item, "*."), ".")
			if len(d) <= 253 && strings.Contains(d, ".") && domainPattern.MatchString(d) {
				r.Domains = append(r.Domains, d)
				continue
			}
			bad = append(bad, item)
		}
	}
	return r, bad
}

// ErrTooManyRoutes: more entries than the node is given.
var ErrTooManyRoutes = errors.New("too_many_routes")

// Check limits the count.
func (r Routes) Check() error {
	if len(r.Domains)+len(r.CIDRs) > maxRoutes {
		return ErrTooManyRoutes
	}
	return nil
}

// All is the list as the admin sees it: domains, then networks.
func (r Routes) All() []string { return append(append([]string{}, r.Domains...), r.CIDRs...) }

func (r Routes) String() string {
	return fmt.Sprintf("%d domains, %d networks", len(r.Domains), len(r.CIDRs))
}
