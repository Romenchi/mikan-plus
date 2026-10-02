package domain

import (
	"context"
	"database/sql"
	"errors"
	"net"
	"strconv"
	"strings"

	"mikan/internal/panel/settings"
	"mikan/internal/panel/store/db"
)

// Who holds a port on a node's server. One map answers every "is this port free"
// question: inbounds added and moved in the admin, the CLI and by the automatic moves,
// the cascade relay's port and the subscription port.

// Kinds of PortHolder.
const (
	PortInbound = "inbound"  // an inbound of the node
	PortRelay   = "relay"    // the cascade relay other nodes leave through
	PortPanel   = "panel"    // the panel's own HTTPS, on the panel's node
	PortSub     = "sub"      // the subscription port, on the panel's node
	PortNodeAPI = "node_api" // the node API the panel connects to, on a remote node
)

// sshPort is never picked automatically: a relay or a moved inbound there would take
// the admin's way in. An admin may still choose it.
const sshPort = 22

// PortPool is where the panel puts what it places on its own: a blocked inbound's new
// port, a cascade relay. HTTPS runs on these ports too (the alternative ports of CDNs),
// so the traffic still looks like a web server's. The installer opens them in the
// firewall.
var PortPool = []int{2053, 2083, 2087, 2096, 2443, 3443, 4443, 5443, 6443, 7443, 8443, 9443}

// PortHolder is what holds a port. Name and ID are an inbound's; the other kinds are one
// per node.
type PortHolder struct {
	Kind string
	Name string
	ID   int64
}

// InboundHolder is in as the holder of its port, to leave it out of its own check.
func InboundHolder(in db.Inbound) PortHolder {
	return PortHolder{Kind: PortInbound, Name: in.Name, ID: in.ID}
}

// PortInUseError names what already listens on the port.
type PortInUseError struct{ PortHolder }

func (e *PortInUseError) Error() string {
	if e.Kind == PortInbound {
		return "port_in_use: " + e.Name
	}
	return "port_in_use: " + e.Kind
}

type portUse struct {
	holder  PortHolder
	lo, hi  int
	network string
	off     bool // a disabled inbound: holds nothing now, but automatic picks keep off its port
}

// PortMap is every port taken on one node's server, over TCP and UDP. A Hysteria2
// hopping range takes every port in it.
type PortMap struct{ uses []portUse }

// NodePorts maps the ports of node: its inbounds, its cascade relay, and the panel's
// port and the subscription port on the panel's own node or the node API's port on a
// remote one. A read error is returned: a port is never free because the map is short.
func NodePorts(ctx context.Context, q *db.Queries, node db.Node) (PortMap, error) {
	var m PortMap
	if node.Address == "" {
		set := settings.New(q)
		for _, s := range []struct{ kind, key string }{{PortPanel, settings.KeyPanelPort}, {PortSub, settings.KeySubPort}} {
			p, _, err := settings.Get[int](ctx, set, s.key)
			if err != nil {
				return PortMap{}, err
			}
			if p > 0 {
				m.add(PortHolder{Kind: s.kind}, strconv.Itoa(p), "tcp", false)
			}
		}
	} else if _, p, err := net.SplitHostPort(node.Address); err == nil {
		m.add(PortHolder{Kind: PortNodeAPI}, p, "tcp", false)
	}
	r, err := q.GetNodeRelay(ctx, node.ID)
	switch {
	case err == nil:
		m.add(PortHolder{Kind: PortRelay}, r.Port, "tcp", false)
	case !errors.Is(err, sql.ErrNoRows):
		return PortMap{}, err
	}
	inbounds, err := q.ListNodeInbounds(ctx, node.ID)
	if err != nil {
		return PortMap{}, err
	}
	for _, in := range inbounds {
		m.add(InboundHolder(in), in.Port, InboundNetwork(in), in.Enabled == 0)
	}
	return m, nil
}

func (m *PortMap) add(h PortHolder, spec, network string, off bool) {
	if lo, hi, ok := parsePort(spec); ok {
		m.uses = append(m.uses, portUse{holder: h, lo: lo, hi: hi, network: network, off: off})
	}
}

// Busy returns what other than self listens on spec, a port or a range, over network.
// A disabled inbound holds nothing: it is checked when it comes back on.
func (m PortMap) Busy(spec, network string, self PortHolder) (PortHolder, bool) {
	lo, hi, ok := parsePort(spec)
	if !ok {
		return PortHolder{}, false
	}
	for _, u := range m.uses {
		if !u.off && u.network == network && !(u.holder.Kind == self.Kind && u.holder.ID == self.ID) && lo <= u.hi && u.lo <= hi {
			return u.holder, true
		}
	}
	return PortHolder{}, false
}

// Free says whether an automatic pick may take port over network: nothing holds it, not
// even a disabled inbound that may come back on, and it is not SSH's.
func (m PortMap) Free(port int, network string) bool {
	if port == sshPort {
		return false
	}
	for _, u := range m.uses {
		if u.network == network && u.lo <= port && port <= u.hi {
			return false
		}
	}
	return true
}

// CheckPort refuses spec over network on node with a *PortInUseError when something
// other than self holds it.
func CheckPort(ctx context.Context, q *db.Queries, node db.Node, spec, network string, self PortHolder) error {
	m, err := NodePorts(ctx, q, node)
	if err != nil {
		return err
	}
	if h, busy := m.Busy(spec, network, self); busy {
		return &PortInUseError{h}
	}
	return nil
}

// parsePort reads a port ("443") or a Hysteria2 hopping range ("20000-30000").
func parsePort(spec string) (lo, hi int, ok bool) {
	a, b, isRange := strings.Cut(spec, "-")
	lo, err := strconv.Atoi(a)
	if err != nil || lo < 1 || lo > 65535 {
		return 0, 0, false
	}
	if !isRange {
		return lo, lo, true
	}
	hi, err = strconv.Atoi(b)
	if err != nil || hi <= lo || hi > 65535 {
		return 0, 0, false
	}
	return lo, hi, true
}
