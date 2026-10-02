// Package nodesync keeps the panel's nodes in line with the database: desired state,
// access policies and traffic counters per node (Syncer), and the panel-wide upkeep of
// period resets, the slot pool and devices (Manager).
package nodesync

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"mikan/internal/nodeapi"
	"mikan/internal/panel/domain"
	"mikan/internal/panel/settings"
	"mikan/internal/panel/store/db"
	"mikan/internal/panel/warp"
	"mikan/internal/proto"
)

// Node is the subset of the node API the syncer needs.
type Node interface {
	Apply(ctx context.Context, s nodeapi.DesiredState) (nodeapi.ApplyResult, error)
	SetPolicies(ctx context.Context, epoch string, p []nodeapi.Policy) error
	Counters(ctx context.Context) (nodeapi.Counters, error)
	Ack(ctx context.Context, epoch string, seq int64) error
	Health(ctx context.Context) (nodeapi.Health, error)
}

// TLSSource returns the certificate the node uses for Hysteria2/TUIC.
type TLSSource func() (*nodeapi.TLSFiles, error)

// Syncer drives one node.
type Syncer struct {
	id    int64
	m     *Manager
	node  Node
	tls   TLSSource
	local bool
	log   *slog.Logger

	policiesDirty chan struct{}
	stateDirty    chan struct{}

	mu          sync.Mutex
	stateKey    string
	policyKey   string
	lastApplied nodeapi.ApplyResult

	health atomic.Pointer[HealthView]
	online atomic.Pointer[map[string]nodeapi.Online]
}

type HealthView struct {
	OK        bool
	Error     string
	Health    nodeapi.Health
	Listeners []nodeapi.ListenerStatus
	CheckedAt time.Time
}

func newSyncer(m *Manager, id int64, t Target) *Syncer {
	s := &Syncer{id: id, m: m, node: t.Node, tls: t.TLS, local: t.Local, log: m.log.With("node", id),
		policiesDirty: make(chan struct{}, 1), stateDirty: make(chan struct{}, 1)}
	empty := map[string]nodeapi.Online{}
	s.online.Store(&empty)
	s.health.Store(&HealthView{Error: "not checked yet"})
	return s
}

func (s *Syncer) ID() int64        { return s.id }
func (s *Syncer) Client() Node     { return s.node }
func (s *Syncer) PoliciesChanged() { signal(s.policiesDirty) }
func (s *Syncer) SlotsChanged()    { signal(s.stateDirty) }

func signal(ch chan struct{}) {
	select {
	case ch <- struct{}{}:
	default:
	}
}

func (s *Syncer) Health() HealthView { return *s.health.Load() }

// Online returns the node's live connection view keyed by slot name.
func (s *Syncer) Online() map[string]nodeapi.Online { return *s.online.Load() }

func (s *Syncer) run(ctx context.Context) {
	counters := time.NewTicker(2 * time.Second)
	health := time.NewTicker(5 * time.Second)
	defer counters.Stop()
	defer health.Stop()
	s.applyState(ctx)
	s.refreshHealth(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.stateDirty:
			s.applyState(ctx)
		case <-s.policiesDirty:
			// Coalesce bursts (bulk actions) into one push.
			time.Sleep(150 * time.Millisecond)
			s.pushPolicies(ctx, true)
		case <-counters.C:
			s.pullCounters(ctx)
		case <-health.C:
			s.refreshHealth(ctx)
		}
	}
}

// desired builds the node's full state from the database.
func (s *Syncer) desired(ctx context.Context) (nodeapi.DesiredState, error) {
	var st nodeapi.DesiredState
	q := s.m.st.Q
	n, err := q.GetNode(ctx, s.id)
	if err != nil {
		return st, err
	}
	inbounds, err := q.ListInbounds(ctx)
	if err != nil {
		return st, err
	}
	st.Inbounds = []nodeapi.Inbound{}
	if st.Warp, err = s.warp(ctx, n, inbounds); err != nil {
		return st, err
	}
	if st.Relay, err = s.relay(ctx, n, inbounds); err != nil {
		return st, err
	}
	for _, in := range inbounds {
		// A disabled node keeps running but serves nothing.
		if in.NodeID != s.id || in.Enabled == 0 || n.Enabled == 0 {
			continue
		}
		t, err := proto.Parse(in.Config)
		if err != nil {
			// Saved configs are validated; a broken one must not take the others down.
			s.log.Error("inbound config", "inbound", in.Name, "err", err)
			continue
		}
		st.Inbounds = append(st.Inbounds, nodeapi.Inbound{Name: in.Name, Port: in.Port, Config: t.JSON()})
	}
	if s.local {
		// The panel runs next to its own node, so its HTTPS port is the self-steal REALITY target.
		if st.SelfStealPort, _, err = settings.Get[int](ctx, s.m.set, settings.KeyPanelPort); err != nil {
			return st, err
		}
	}
	slots, err := q.ListSlots(ctx)
	if err != nil {
		return st, err
	}
	for _, sl := range slots {
		st.Slots = append(st.Slots, nodeapi.Slot{Name: sl.Name, UUID: sl.Uuid, Secret: sl.Secret})
	}
	if st.TLS, err = s.tls(); err != nil {
		return st, err
	}
	st.Epoch, st.Policies, _, err = s.policies(ctx)
	return st, err
}

func (s *Syncer) applyState(ctx context.Context) {
	st, err := s.desired(ctx)
	if err != nil {
		s.log.Error("build node state", "err", err)
		return
	}
	key := stateKey(st)
	s.mu.Lock()
	same := key == s.stateKey
	s.mu.Unlock()
	if same {
		s.pushPolicies(ctx, false)
		return
	}
	rev, err := s.nextRevision(ctx)
	if err != nil {
		s.log.Error("revision", "err", err)
		return
	}
	st.Revision = rev
	res, err := s.node.Apply(ctx, st)
	if err != nil {
		s.log.Warn("apply node state", "err", err)
		return
	}
	for _, l := range res.Listeners {
		if !l.OK {
			s.log.Error("listener failed", "name", l.Name, "err", l.Error)
		}
	}
	s.mu.Lock()
	s.stateKey = key
	s.policyKey = policyKey(st.Policies)
	s.lastApplied = res
	s.mu.Unlock()
	s.log.Info("node state applied", "revision", rev, "recreated", res.Recreated)
}

func (s *Syncer) pushPolicies(ctx context.Context, force bool) {
	epoch, ps, _, err := s.policies(ctx)
	if err != nil {
		s.log.Error("build policies", "err", err)
		return
	}
	key := policyKey(ps)
	s.mu.Lock()
	unchanged := key == s.policyKey && !force
	s.mu.Unlock()
	if unchanged {
		return
	}
	if err := s.node.SetPolicies(ctx, epoch, ps); err != nil {
		s.log.Warn("push policies", "err", err)
		return
	}
	s.mu.Lock()
	s.policyKey = key
	s.mu.Unlock()
}

// policies are the same slots on every node; what differs is the counter position the
// quota refers to, the inbounds that exist here and the devices seen elsewhere. A user
// has the own slot and one per bound device: all of them get the user's rules, and the
// device limit counts the user's devices under all of them.
func (s *Syncer) policies(ctx context.Context) (epoch string, out []nodeapi.Policy, slotUser map[string]int64, err error) {
	q := s.m.st.Q
	epoch, seq, err := s.countersPos(ctx)
	if err != nil {
		return "", nil, nil, err
	}
	users, err := q.ListUsers(ctx)
	if err != nil {
		return "", nil, nil, err
	}
	owners, err := q.ListSlotUsers(ctx)
	if err != nil {
		return "", nil, nil, err
	}
	inbounds, err := q.ListInbounds(ctx)
	if err != nil {
		return "", nil, nil, err
	}
	slotUser = make(map[string]int64, len(owners))
	slotsOf := map[int64][]string{}
	for _, o := range owners {
		slotUser[o.SlotName] = o.UserID
		slotsOf[o.UserID] = append(slotsOf[o.UserID], o.SlotName)
	}
	here := map[int64]string{}
	for _, in := range inbounds {
		if in.NodeID == s.id {
			here[in.ID] = in.Name
		}
	}
	others := s.m.otherIPs(s.id, slotUser)
	now := s.m.now()
	for _, u := range users {
		names := slotsOf[u.ID]
		sort.Strings(names)
		for _, name := range names {
			out = append(out, userPolicy(u, name, seq, now, here, others[name]))
		}
	}
	return epoch, out, slotUser, nil
}

// userPolicy is the user's rules for one of the user's slots.
func userPolicy(u db.User, name string, seq int64, now time.Time, here map[int64]string, otherIPs []string) nodeapi.Policy {
	p := nodeapi.Policy{Slot: name, Allowed: domain.CanConnect(domain.State(u, now)), QuotaRemaining: -1, BaseSeq: seq, OtherIPs: otherIPs}
	if u.DeviceLimit.Valid {
		p.DeviceLimit = int(u.DeviceLimit.Int64)
	}
	if u.TrafficLimit.Valid {
		p.QuotaRemaining = max(0, u.TrafficLimit.Int64-u.UsedUp-u.UsedDown)
	}
	if allowed := domain.DecodeInbounds(u.Inbounds); len(allowed) > 0 {
		for _, id := range allowed {
			if n, ok := here[id]; ok {
				p.Inbounds = append(p.Inbounds, n)
			}
		}
		// An empty list means "all": a user limited to other nodes' inbounds gets none here.
		if len(p.Inbounds) == 0 {
			p.Allowed = false
		}
	}
	return p
}

func (s *Syncer) pullCounters(ctx context.Context) {
	c, err := s.node.Counters(ctx)
	if err != nil {
		return
	}
	online := c.Online
	if online == nil {
		online = map[string]nodeapi.Online{}
	}
	s.online.Store(&online)

	epoch, seq, err := s.countersPos(ctx)
	if err != nil {
		s.log.Error("counters position", "err", err)
		return
	}
	if c.Epoch == epoch && c.Seq <= seq {
		_ = s.node.Ack(ctx, c.Epoch, c.Seq)
		return
	}
	now := s.m.now()
	hour, day := now.Unix()/3600, now.Unix()/86400
	err = s.m.st.Tx(ctx, func(q *db.Queries) error {
		rows, err := q.ListSlotUsers(ctx)
		if err != nil {
			return err
		}
		owner := make(map[string]int64, len(rows))
		for _, r := range rows {
			owner[r.SlotName] = r.UserID
		}
		for slot, t := range c.Slots {
			uid, ok := owner[slot]
			if !ok {
				continue
			}
			if err := q.AddUserTraffic(ctx, db.AddUserTrafficParams{Up: t.Up, Down: t.Down, ID: uid}); err != nil {
				return err
			}
			if err := q.AddTrafficHourly(ctx, db.AddTrafficHourlyParams{UserID: uid, Hour: hour, Up: t.Up, Down: t.Down}); err != nil {
				return err
			}
			if err := q.AddTrafficDaily(ctx, db.AddTrafficDailyParams{UserID: uid, Day: day, Up: t.Up, Down: t.Down}); err != nil {
				return err
			}
		}
		if err := q.SetNodeState(ctx, db.SetNodeStateParams{Key: stateKeyOf("counters_epoch", s.id), Value: c.Epoch}); err != nil {
			return err
		}
		return q.SetNodeState(ctx, db.SetNodeStateParams{Key: stateKeyOf("counters_seq", s.id), Value: strconv.FormatInt(c.Seq, 10)})
	})
	if err != nil {
		s.log.Error("store counters", "err", err)
		return
	}
	if err := s.node.Ack(ctx, c.Epoch, c.Seq); err != nil {
		s.log.Warn("ack counters", "err", err)
	}
	if c.Epoch != epoch {
		// The node started a new counter epoch (fresh volume): re-base its quotas.
		s.pushPolicies(ctx, true)
	}
}

func (s *Syncer) countersPos(ctx context.Context) (string, int64, error) {
	epoch, err := s.m.st.Q.GetNodeState(ctx, stateKeyOf("counters_epoch", s.id))
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", 0, err
	}
	raw, err := s.m.st.Q.GetNodeState(ctx, stateKeyOf("counters_seq", s.id))
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", 0, err
	}
	seq, _ := strconv.ParseInt(raw, 10, 64)
	return epoch, seq, nil
}

func (s *Syncer) nextRevision(ctx context.Context) (int64, error) {
	key := stateKeyOf("revision", s.id)
	raw, err := s.m.st.Q.GetNodeState(ctx, key)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	rev, _ := strconv.ParseInt(raw, 10, 64)
	rev++
	return rev, s.m.st.Q.SetNodeState(ctx, db.SetNodeStateParams{Key: key, Value: strconv.FormatInt(rev, 10)})
}

func (s *Syncer) refreshHealth(ctx context.Context) {
	h, err := s.node.Health(ctx)
	view := &HealthView{CheckedAt: s.m.now()}
	if err != nil {
		view.Error = err.Error()
		s.health.Store(view)
		return
	}
	view.OK, view.Health, view.Listeners = true, h, h.Listeners
	s.health.Store(view)
	s.mu.Lock()
	applied := s.lastApplied.Revision
	s.mu.Unlock()
	// A node that lost its state (new volume, crash before saving) reports an older revision.
	if h.Revision < applied || (applied == 0 && h.Revision == 0) {
		s.mu.Lock()
		s.stateKey = ""
		s.mu.Unlock()
		signal(s.stateDirty)
	}
}

func stateKey(st nodeapi.DesiredState) string {
	raw, _ := json.Marshal(struct {
		I []nodeapi.Inbound
		S []nodeapi.Slot
		T *nodeapi.TLSFiles
		P int
		W *nodeapi.Warp
		R *nodeapi.Relay
	}{st.Inbounds, st.Slots, st.TLS, st.SelfStealPort, st.Warp, st.Relay})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// policyKey ignores QuotaRemaining/BaseSeq: they change with every byte and the node
// tracks consumption itself between pushes.
func policyKey(ps []nodeapi.Policy) string {
	h := sha256.New()
	for _, p := range ps {
		raw, _ := json.Marshal([]any{p.Slot, p.Allowed, p.Inbounds, p.DeviceLimit, p.QuotaRemaining < 0, p.OtherIPs})
		h.Write(raw)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// warp is the node's WARP outbound, nil when it has none or it is off. Inbounds set to
// WARP on a node without it simply leave directly.
func (s *Syncer) warp(ctx context.Context, n db.Node, inbounds []db.Inbound) (*nodeapi.Warp, error) {
	w, err := s.m.st.Q.GetNodeWarp(ctx, n.ID)
	if errors.Is(err, sql.ErrNoRows) || err == nil && w.Enabled == 0 {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	out := &nodeapi.Warp{PrivateKey: w.PrivateKey, PeerPublicKey: w.PeerPublicKey, Endpoint: w.Endpoint, IPv4: w.Ipv4, IPv6: w.Ipv6,
		MTU: int(w.Mtu), Inbounds: []string{}}
	if r, err := base64.StdEncoding.DecodeString(w.Reserved); err == nil && len(r) == 3 {
		out.Reserved = r
	}
	var routes []string
	_ = json.Unmarshal([]byte(w.Routes), &routes)
	rs, _ := warp.ParseRoutes(routes)
	out.Domains, out.CIDRs = rs.Domains, rs.CIDRs
	for _, in := range inbounds {
		if in.NodeID == n.ID && in.Enabled != 0 && in.Outbound == "warp" {
			out.Inbounds = append(out.Inbounds, in.Name)
		}
	}
	return out, nil
}

// relay is the node's upstream relay outbound (e.g. VLESS Reality to Germany), nil when disabled.
func (s *Syncer) relay(ctx context.Context, n db.Node, inbounds []db.Inbound) (*nodeapi.Relay, error) {
	r, err := s.m.st.Q.GetNodeRelay(ctx, n.ID)
	if errors.Is(err, sql.ErrNoRows) || err == nil && r.Enabled == 0 {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var inNames []string
	if r.Inbounds != "" {
		_ = json.Unmarshal([]byte(r.Inbounds), &inNames)
	}
	return &nodeapi.Relay{
		Enabled:     r.Enabled != 0,
		Protocol:    r.Protocol,
		Server:      r.Server,
		Port:        int(r.Port),
		UUID:        r.Uuid,
		Flow:        r.Flow,
		TLS:         r.Tls != 0,
		SNI:         r.Sni,
		PublicKey:   r.PublicKey,
		ShortID:     r.ShortID,
		SpiderX:     r.SpiderX,
		Fingerprint: r.Fingerprint,
		Inbounds:    inNames,
	}, nil
}

// Warp asks the node how it reaches the internet through WARP.
func (m *Manager) Warp(ctx context.Context, id int64) (nodeapi.WarpStatus, error) {
	s, ok := m.Syncer(id)
	if !ok {
		return nodeapi.WarpStatus{}, nodeapi.ErrUnavailable
	}
	c, ok := s.node.(interface {
		Warp(ctx context.Context) (nodeapi.WarpStatus, error)
	})
	if !ok {
		return nodeapi.WarpStatus{}, nodeapi.ErrUnavailable
	}
	return c.Warp(ctx)
}
