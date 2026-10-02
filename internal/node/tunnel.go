package node

import (
	"io"
	"net"
	"sync"
	"time"

	N "github.com/metacubex/mihomo/common/net"
	C "github.com/metacubex/mihomo/constant"

	"mikan/internal/nodeapi"
)

// Tunnel sits between mihomo listeners and mihomo's own tunnel: it resolves the slot
// from metadata.InUser, enforces policy, counts bytes and keeps connections kickable.
type Tunnel struct {
	inner C.Tunnel
	reg   *Registry
}

func (t *Tunnel) HandleTCPConn(conn net.Conn, m *C.Metadata) {
	// REALITY dials its dest through this tunnel for every handshake; these carry no
	// user and must pass untouched or no client can connect (S-01a). They also bypass
	// the REJECT rules for private ranges, so a self-hosted dest keeps working.
	if m.Type == C.INNER {
		defer conn.Close()
		remote, err := net.DialTimeout("tcp", m.RemoteAddress(), 10*time.Second)
		if err != nil {
			return
		}
		defer remote.Close()
		N.Relay(conn, remote)
		return
	}
	// Another node of the panel relays its users' traffic: the listener checked its key,
	// and the source node has already counted and limited them. The rules still apply.
	if m.InName == nodeapi.RelayListener {
		t.inner.HandleTCPConn(conn, m)
		return
	}
	ip := m.SrcIP.Unmap().String()
	s, b := t.reg.admitIn(userOf(conn, m), m.InName, ip, true)
	if s == nil {
		_ = conn.Close()
		return
	}
	c := &countingConn{Conn: conn, slot: s, bucket: b, inName: m.InName, ip: ip, now: t.reg.now}
	s.addConn(c)
	defer c.Close()
	t.inner.HandleTCPConn(c, m)
}

func (t *Tunnel) HandleUDPPacket(p C.UDPPacket, m *C.Metadata) {
	if m.Type == C.INNER || m.InName == nodeapi.RelayListener {
		t.inner.HandleUDPPacket(p, m)
		return
	}
	s, b := t.reg.admitIn(m.InUser, m.InName, m.SrcIP.Unmap().String(), false)
	if s == nil {
		p.Drop()
		return
	}
	s.countIn(b, int64(len(p.Data())), 0)
	t.inner.HandleUDPPacket(&countingPacket{UDPPacket: p, slot: s, bucket: b}, m)
}

func (t *Tunnel) NatTable() C.NatTable { return t.inner.NatTable() }

// userOf is who opened the connection. mihomo 1.19.31 leaves metadata.InUser empty for
// Mieru (fixed upstream later); the Mieru connection itself still knows the user.
func userOf(conn net.Conn, m *C.Metadata) string {
	if m.InUser == "" && m.Type == C.MIERU {
		if u, ok := conn.(interface{ UserName() string }); ok {
			return u.UserName()
		}
	}
	return m.InUser
}

// countingConn: Read from the client is upload, Write to the client is download.
// UnwrapReader/UnwrapWriter must return []N.CountFunc (an alias of sing's type), so that
// sing's copy loop still counts bytes when it switches to splice (XTLS Vision).
// Upstream() is deliberately absent: exposing it would let sing bypass the counters.
type countingConn struct {
	net.Conn
	slot   *slot
	bucket *bucket // the traffic pool it counts to; nil: the main quota
	inName string
	ip     string
	now    func() time.Time
	once   sync.Once
}

func (c *countingConn) Read(b []byte) (int, error) {
	n, err := c.Conn.Read(b)
	if n > 0 {
		c.slot.countIn(c.bucket, int64(n), 0)
	}
	return n, err
}

func (c *countingConn) Write(b []byte) (int, error) {
	n, err := c.Conn.Write(b)
	if n > 0 {
		c.slot.countIn(c.bucket, 0, int64(n))
	}
	return n, err
}

func (c *countingConn) UnwrapReader() (io.Reader, []N.CountFunc) {
	return c.Conn, []N.CountFunc{func(n int64) { c.slot.countIn(c.bucket, n, 0) }}
}

func (c *countingConn) UnwrapWriter() (io.Writer, []N.CountFunc) {
	return c.Conn, []N.CountFunc{func(n int64) { c.slot.countIn(c.bucket, 0, n) }}
}

func (c *countingConn) Close() error {
	c.once.Do(func() { c.slot.closeConn(c, c.now()) })
	return c.Conn.Close()
}

type countingPacket struct {
	C.UDPPacket
	slot   *slot
	bucket *bucket
}

func (p *countingPacket) WriteBack(b []byte, addr net.Addr) (int, error) {
	n, err := p.UDPPacket.WriteBack(b, addr)
	if n > 0 {
		p.slot.countIn(p.bucket, 0, int64(n))
	}
	return n, err
}
