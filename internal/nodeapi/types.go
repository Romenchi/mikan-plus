// Package nodeapi is the contract between the panel and a node. It is shared by both
// binaries and must not import mihomo.
package nodeapi

import (
	"encoding/json"
	"time"

	"mikan/internal/proto"
	"mikan/internal/scan"
)

const (
	PresetVlessVision = "vless_reality_vision"
	PresetVlessXHTTP  = "vless_reality_xhttp"
	PresetHysteria2   = "hysteria2"
	PresetTUIC        = "tuic_v5"
)

// DesiredState is the complete configuration of a node. Applying the same state twice
// is a no-op; listeners are recreated only when their own part changed.
type DesiredState struct {
	Revision int64     `json:"revision"`
	Epoch    string    `json:"epoch"` // counters epoch the policies' BaseSeq refers to
	Inbounds []Inbound `json:"inbounds"`
	Slots    []Slot    `json:"slots"`
	Policies []Policy  `json:"policies"`
	TLS      *TLSFiles `json:"tls,omitempty"`
	// SelfStealPort allows REALITY dest 127.0.0.1:<port> (the panel's own HTTPS).
	SelfStealPort int `json:"self_steal_port,omitempty"`
	// Warp is Cloudflare WARP as an outbound; nil: everything leaves directly.
	Warp *Warp `json:"warp,omitempty"`
	// Relay is an upstream proxy (e.g. VLESS Reality to Germany); nil: direct exit.
	Relay *Relay `json:"relay,omitempty"`
}

type Inbound struct {
	Name   string          `json:"name"`
	Listen string          `json:"listen"`
	Port   string          `json:"port"`             // "443" or a range "20000-20100"
	Config json.RawMessage `json:"config,omitempty"` // proto.Template as JSON
	// Preset and Settings are the format of mikan ≤ 0.1.2; the node still reads them
	// from a saved state, the panel no longer sends them.
	Preset   string          `json:"preset,omitempty"`
	Settings json.RawMessage `json:"settings,omitempty"`
}

type Slot = proto.Slot

// ValidateRequest asks the node to parse one inbound with mihomo without applying it.
type ValidateRequest struct {
	Inbound       Inbound `json:"inbound"`
	SelfStealPort int     `json:"self_steal_port,omitempty"`
}

type Policy struct {
	Slot           string   `json:"slot"`
	Allowed        bool     `json:"allowed"`
	Inbounds       []string `json:"inbounds,omitempty"` // allowed inbound names; empty = all
	DeviceLimit    int      `json:"device_limit"`       // 0 = unlimited
	QuotaRemaining int64    `json:"quota_remaining"`    // bytes left as of BaseSeq; -1 = unlimited
	BaseSeq        int64    `json:"base_seq"`
	// OtherIPs are the slot's devices on the panel's other nodes: they count against
	// DeviceLimit here too, and may connect here without taking another device.
	OtherIPs []string `json:"other_ips,omitempty"`
}

type PoliciesRequest struct {
	Epoch    string   `json:"epoch"`
	Policies []Policy `json:"policies"`
}

type KickRequest struct {
	Slots []string `json:"slots"`
}

type AckRequest struct {
	Epoch string `json:"epoch"`
	Seq   int64  `json:"seq"`
}

type TLSFiles struct {
	CertPEM string `json:"cert_pem"`
	KeyPEM  string `json:"key_pem"`
}

// Counters is a batch of traffic deltas. The node returns the same batch until it is
// acknowledged, so the panel can apply it idempotently by (Epoch, Seq).
type Counters struct {
	Epoch  string             `json:"epoch"`
	Seq    int64              `json:"seq"`
	Slots  map[string]Traffic `json:"slots"`
	Online map[string]Online  `json:"online"` // live view, not part of the batch
}

type Traffic struct {
	Up   int64 `json:"up"`
	Down int64 `json:"down"`
}

type Online struct {
	IPs   []string `json:"ips"`
	Conns int      `json:"conns"`
}

type Health struct {
	Version   string           `json:"version"`
	Core      string           `json:"core"`
	Revision  int64            `json:"revision"`
	StartedAt time.Time        `json:"started_at"`
	Listeners []ListenerStatus `json:"listeners"`
	Conns     int              `json:"conns"`
	System    System           `json:"system"`
}

type System struct {
	CPUPercent float64 `json:"cpu_percent"`
	MemTotal   uint64  `json:"mem_total"`
	MemUsed    uint64  `json:"mem_used"`
	ProcRSS    uint64  `json:"proc_rss"`
	NetRxBps   uint64  `json:"net_rx_bps"`
	NetTxBps   uint64  `json:"net_tx_bps"`
}

type ListenerStatus struct {
	Name  string `json:"name"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// Activity tells the panel which inbounds each device reached lately. A device that
// keeps reaching the node's other inbounds but never one of them is cut off from that
// one on the way, e.g. its port is blocked by DPI.
type Activity struct {
	Clients []ClientActivity `json:"clients"`
}

type ClientActivity struct {
	Slot string           `json:"slot"`
	IP   string           `json:"ip"`
	Seen map[string]int64 `json:"seen"` // inbound name → unix time of the last admitted connection
}

// TargetCheckRequest asks the node to test a REALITY target from its own network: the
// node is the one that dials it for every client handshake.
type TargetCheckRequest struct {
	Dest string `json:"dest"`
	SNI  string `json:"sni,omitempty"`
}

// TargetScanRequest asks the node for REALITY targets in the /24 around IP, its public
// address.
type TargetScanRequest struct {
	IP    string `json:"ip"`
	Limit int    `json:"limit,omitempty"`
}

type TargetResult = scan.Result

type TargetScan struct {
	Scanned int            `json:"scanned"`
	Results []TargetResult `json:"results"`
}

type ApplyResult struct {
	Revision  int64            `json:"revision"`
	Recreated []string         `json:"recreated"`
	Listeners []ListenerStatus `json:"listeners"`
}

type LogLine struct {
	Time    time.Time `json:"time"`
	Level   string    `json:"level"`
	Message string    `json:"message"`
}

type Error struct {
	Code    string `json:"code"` // invalid_state | apply_failed | not_ready | bad_request
	Message string `json:"message"`
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

// Preset settings stored in inbounds.settings and passed to the node as-is.

type RealitySettings struct {
	PrivateKey  string   `json:"private_key"`
	PublicKey   string   `json:"public_key"`
	ShortIDs    []string `json:"short_ids"`
	Dest        string   `json:"dest"`
	ServerNames []string `json:"server_names"`
}

type VlessVisionSettings struct {
	Reality RealitySettings `json:"reality"`
}

type VlessXHTTPSettings struct {
	Reality RealitySettings `json:"reality"`
	Path    string          `json:"path"`
	Mode    string          `json:"mode"` // "stream-one": "auto" hangs on mihomo v1.19.31 (S-01a)
}

type Hysteria2Settings struct {
	ObfsPassword string `json:"obfs_password,omitempty"`
	UpMbps       int    `json:"up_mbps,omitempty"`
	DownMbps     int    `json:"down_mbps,omitempty"`
	Masquerade   string `json:"masquerade,omitempty"`
}

type TUICSettings struct {
	CongestionControl string `json:"congestion_control"`
}

// Warp is a WireGuard tunnel to Cloudflare WARP on the node. The listed inbounds leave
// through it whole, and so do the listed domains and networks for every inbound; the
// rest goes out directly. When WARP is down its traffic fails instead of leaving from
// the server's own address.
type Warp struct {
	PrivateKey    string   `json:"private_key"`
	PeerPublicKey string   `json:"peer_public_key"`
	Endpoint      string   `json:"endpoint"`       // host:port
	IPv4          string   `json:"ipv4"`           // the tunnel's address, without a mask
	IPv6          string   `json:"ipv6,omitempty"` // likewise
	Reserved      []uint8  `json:"reserved,omitempty"`
	MTU           int      `json:"mtu,omitempty"`
	Inbounds      []string `json:"inbounds"`
	Domains       []string `json:"domains,omitempty"` // suffixes: example.com covers its subdomains
	CIDRs         []string `json:"cidrs,omitempty"`
}

// WarpStatus is the node's last look at the internet through WARP.
type WarpStatus struct {
	Configured bool      `json:"configured"`
	OK         bool      `json:"ok"`
	IP         string    `json:"ip,omitempty"`   // the address sites see
	Warp       string    `json:"warp,omitempty"` // on | plus | off, as Cloudflare says
	Colo       string    `json:"colo,omitempty"` // Cloudflare's data center
	Error      string    `json:"error,omitempty"`
	CheckedAt  time.Time `json:"checked_at"`
}

// Relay routes node traffic through an upstream proxy (e.g. VLESS Reality to Germany).
type Relay struct {
	Enabled     bool     `json:"enabled"`
	Protocol    string   `json:"protocol"` // "vless", "socks5", "shadowsocks"
	Server      string   `json:"server"`
	Port        int      `json:"port"`
	UUID        string   `json:"uuid,omitempty"`
	Flow        string   `json:"flow,omitempty"`
	TLS         bool     `json:"tls,omitempty"`
	SNI         string   `json:"sni,omitempty"`
	PublicKey   string   `json:"public_key,omitempty"`
	ShortID     string   `json:"short_id,omitempty"`
	SpiderX     string   `json:"spider_x,omitempty"`
	Fingerprint string   `json:"fingerprint,omitempty"`
	Inbounds    []string `json:"inbounds,omitempty"`
}
