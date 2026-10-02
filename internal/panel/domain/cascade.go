package domain

import (
	"context"
	"database/sql"
	"errors"
	"math/rand/v2"
	"strconv"
	"time"

	"mikan/internal/panel/presets"
	"mikan/internal/panel/store/db"
	"mikan/internal/proto"
)

// Cascades: an inbound of node A may leave the internet through node B. B runs a
// hidden relay listener (VLESS REALITY) with a key per source node; the relay's own
// traffic leaves B directly, through B's WARP, or goes on to a further node.

var (
	ErrExitSelf  = errors.New("exit_self")     // a node cannot be its own exit
	ErrExitCycle = errors.New("exit_cycle")    // the chain would come back to where it started
	ErrExitLong  = errors.New("exit_too_long") // the chain has more than maxChain hops
	ErrExitOff   = errors.New("exit_off")      // the exit node is disabled
	ErrNoPort    = errors.New("relay_no_port")
)

// maxChain bounds a cascade: more hops than this only add delay.
const maxChain = 8

// CheckExit says whether traffic of node from may leave through node to: to exists, is
// on, is not from, and the relays after it never lead back to from.
func CheckExit(ctx context.Context, q *db.Queries, from, to int64) error {
	if from == to {
		return ErrExitSelf
	}
	n, err := q.GetNode(ctx, to)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if n.Enabled == 0 {
		return ErrExitOff
	}
	seen := map[int64]bool{from: true}
	cur := to
	for range maxChain {
		if seen[cur] {
			return ErrExitCycle
		}
		seen[cur] = true
		r, err := q.GetNodeRelay(ctx, cur)
		if errors.Is(err, sql.ErrNoRows) || err == nil && !r.ExitNodeID.Valid {
			return nil
		}
		if err != nil {
			return err
		}
		cur = r.ExitNodeID.Int64
	}
	return ErrExitLong
}

// EnsureRelay gives node its relay listener when it has none: a free TCP port, the
// first free one of PortPool (installers open those in the firewall), else a high one.
func EnsureRelay(ctx context.Context, q *db.Queries, node db.Node, now time.Time) (db.NodeRelay, error) {
	if r, err := q.GetNodeRelay(ctx, node.ID); err == nil {
		return r, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return db.NodeRelay{}, err
	}
	ports, err := NodePorts(ctx, q, node)
	if err != nil {
		return db.NodeRelay{}, err
	}
	port := 0
	for _, p := range PortPool {
		if ports.Free(p, "tcp") {
			port = p
			break
		}
	}
	for i := 0; port == 0 && i < 64; i++ {
		if p := 30000 + rand.IntN(30000); ports.Free(p, "tcp") {
			port = p
		}
	}
	if port == 0 {
		return db.NodeRelay{}, ErrNoPort
	}
	reality, err := presets.NewReality(presets.DefaultDest)
	if err != nil {
		return db.NodeRelay{}, err
	}
	t := proto.Template{"type": "vless", "reality-config": reality}
	if err := proto.Validate(t, proto.Options{}); err != nil {
		return db.NodeRelay{}, err
	}
	return q.CreateNodeRelay(ctx, db.CreateNodeRelayParams{NodeID: node.ID, Port: strconv.Itoa(port), Config: proto.Marshal(t), CreatedAt: now.Unix()})
}

// UseExit lets node src leave through node exit: the chain is checked, exit gets its
// relay and src a key there. It runs on the caller's transaction.
func UseExit(ctx context.Context, q *db.Queries, src, exit int64, now time.Time) error {
	if err := CheckExit(ctx, q, src, exit); err != nil {
		return err
	}
	x, err := q.GetNode(ctx, exit)
	if err != nil {
		return err
	}
	if _, err := EnsureRelay(ctx, q, x, now); err != nil {
		return err
	}
	_, err = RelayUser(ctx, q, exit, src)
	return err
}

// ExitUse is what leaves the internet through a node: inbounds of other nodes, enabled
// or not, and nodes whose relay goes on through it. Deleting the node would quietly send
// their traffic straight out.
type ExitUse struct {
	Inbounds []db.Inbound
	Relays   []int64 // the nodes whose relay leaves through it
}

// ExitUsesOf lists what leaves through node id.
func ExitUsesOf(ctx context.Context, q *db.Queries, id int64) (ExitUse, error) {
	var u ExitUse
	ins, err := q.ListInbounds(ctx)
	if err != nil {
		return u, err
	}
	for _, in := range ins {
		if in.ExitNodeID.Valid && in.ExitNodeID.Int64 == id {
			u.Inbounds = append(u.Inbounds, in)
		}
	}
	relays, err := q.ListNodeRelays(ctx)
	if err != nil {
		return u, err
	}
	for _, r := range relays {
		if r.ExitNodeID.Valid && r.ExitNodeID.Int64 == id {
			u.Relays = append(u.Relays, r.NodeID)
		}
	}
	return u, nil
}

// RelayUser is the key node src uses at exit's relay, made on first use.
func RelayUser(ctx context.Context, q *db.Queries, exit, src int64) (string, error) {
	id, err := q.GetRelayUser(ctx, db.GetRelayUserParams{ExitNodeID: exit, SrcNodeID: src})
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	id = newUUID()
	if err := q.AddRelayUser(ctx, db.AddRelayUserParams{ExitNodeID: exit, SrcNodeID: src, Uuid: id}); err != nil {
		return "", err
	}
	return q.GetRelayUser(ctx, db.GetRelayUserParams{ExitNodeID: exit, SrcNodeID: src})
}

// RelayUserName is how a source node shows in the relay's users.
func RelayUserName(src int64) string { return "relay-" + strconv.FormatInt(src, 10) }
