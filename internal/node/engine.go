package node

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	C "github.com/metacubex/mihomo/constant"
	"github.com/metacubex/mihomo/hub/executor"
	"github.com/metacubex/mihomo/listener"
	mlog "github.com/metacubex/mihomo/log"
	"github.com/metacubex/mihomo/tunnel"

	"mikan/internal/nodeapi"
	"mikan/internal/proto"
)

const (
	stateFile    = "node-state.json"
	countersFile = "node-counters.json"
)

type Options struct {
	DataDir string
	Version string
	Log     *slog.Logger
	// A device (source IP) keeps its place for this long after its last connection closed.
	DeviceRelease time.Duration
	// AllowPrivate lets VPN users reach private and loopback networks of the server.
	AllowPrivate bool
}

type Engine struct {
	dataDir      string
	home         string
	log          *slog.Logger
	version      string
	started      time.Time
	allowPrivate bool

	Reg *Registry
	tun *Tunnel

	mu        sync.Mutex // serializes Apply
	applied   nodeapi.DesiredState
	cert      proto.Cert // node certificate files written by the last Apply
	listeners map[string]nodeapi.ListenerStatus

	logs   *logRing
	errsMu sync.Mutex
	errs   map[string]string // listener name → last listen error
	marker chan string

	sys *sysSampler

	routes string // routesKey of what tunnel's proxies and rules hold now
	warpMu sync.Mutex
	warp   nodeapi.WarpStatus // the last check, kept for a minute
}

// routesKey covers what the outbound side of the config depends on.
func routesKey(st nodeapi.DesiredState, allowPrivate bool) string {
	raw, _ := json.Marshal(struct {
		W     *nodeapi.Warp
		Relay *nodeapi.Relay
		R     []string
	}{st.Warp, st.Relay, rules(st, allowPrivate)})
	return string(raw)
}

// WarpStatus checks the internet through WARP, at most once a minute.
func (e *Engine) WarpStatus(ctx context.Context) nodeapi.WarpStatus {
	e.mu.Lock()
	configured := e.applied.Warp != nil
	e.mu.Unlock()
	if !configured {
		return nodeapi.WarpStatus{}
	}
	e.warpMu.Lock()
	defer e.warpMu.Unlock()
	if !e.warp.CheckedAt.IsZero() && time.Since(e.warp.CheckedAt) < time.Minute {
		return e.warp
	}
	e.warp = warpStatus(ctx)
	return e.warp
}

func Start(o Options) (*Engine, error) {
	dataDir, version, log := o.DataDir, o.Version, o.Log
	if o.DeviceRelease <= 0 {
		o.DeviceRelease = 60 * time.Second
	}
	home := filepath.Join(dataDir, "mihomo")
	if err := os.MkdirAll(filepath.Join(home, "tls"), 0o700); err != nil {
		return nil, err
	}
	// Certificates must live under mihomo's home, otherwise its safe-path check rejects them.
	C.SetHomeDir(home)

	e := &Engine{
		dataDir: dataDir, home: home, log: log, version: version, started: time.Now(), allowPrivate: o.AllowPrivate,
		listeners: map[string]nodeapi.ListenerStatus{}, logs: newLogRing(500),
		errs: map[string]string{}, marker: make(chan string, 8), sys: newSysSampler(),
	}
	go e.pumpLogs()

	cs, err := loadCounters(filepath.Join(dataDir, countersFile))
	if err != nil {
		return nil, err
	}
	e.Reg = NewRegistry(cs.Epoch, cs.Seq, o.DeviceRelease, time.Now)
	e.tun = &Tunnel{inner: tunnel.Tunnel, reg: e.Reg}

	base, err := buildConfig(nodeapi.DesiredState{}, proto.Cert{}, o.AllowPrivate)
	if err != nil {
		return nil, err
	}
	cfg, err := executor.ParseWithBytes(base)
	if err != nil {
		return nil, fmt.Errorf("base config: %w", err)
	}
	// ApplyConfig patches listeners with the stock tunnel and drops any others, so it
	// runs exactly once, with no listeners; ours are patched separately in Apply.
	cfg.Listeners = map[string]C.InboundListener{}
	executor.ApplyConfig(cfg, true)

	var st nodeapi.DesiredState
	raw, err := os.ReadFile(filepath.Join(dataDir, stateFile))
	switch {
	case err == nil:
		if err := json.Unmarshal(raw, &st); err != nil {
			return nil, fmt.Errorf("%s: %w", stateFile, err)
		}
		if _, err := e.Apply(st); err != nil {
			log.Error("restore saved state", "err", err)
		}
	case !errors.Is(err, os.ErrNotExist):
		return nil, err
	}
	e.Reg.restore(cs)
	go e.sys.run()
	return e, nil
}

// Apply renders the desired state into mihomo listeners. Unchanged listeners keep their
// connections; policies and slots are updated in place.
func (e *Engine) Apply(st nodeapi.DesiredState) (nodeapi.ApplyResult, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	var cert proto.Cert
	if st.TLS != nil && st.TLS.CertPEM != "" {
		cert = proto.Cert{CertPath: filepath.Join(e.home, "tls", "node.crt"), KeyPath: filepath.Join(e.home, "tls", "node.key")}
		if err := writeFileAtomic(cert.CertPath, []byte(st.TLS.CertPEM), 0o600); err != nil {
			return nodeapi.ApplyResult{}, err
		}
		if err := writeFileAtomic(cert.KeyPath, []byte(st.TLS.KeyPEM), 0o600); err != nil {
			return nodeapi.ApplyResult{}, err
		}
	}
	e.cert = cert
	raw, err := buildConfig(st, cert, e.allowPrivate)
	if err != nil {
		return nodeapi.ApplyResult{}, &nodeapi.Error{Code: "invalid_state", Message: err.Error()}
	}
	cfg, err := executor.ParseWithBytes(raw)
	if err != nil {
		return nodeapi.ApplyResult{}, &nodeapi.Error{Code: "invalid_state", Message: err.Error()}
	}

	// Listeners are patched below; the way out (WARP and the rules that pick it) is
	// swapped only when it changed, so open connections keep their outbound otherwise.
	if key := routesKey(st, e.allowPrivate); key != e.routes {
		// The old tunnel's WireGuard device would keep its socket and goroutines.
		if old, ok := tunnel.Proxies()[warpProxy]; ok {
			defer func() {
				if c, ok := old.Adapter().(io.Closer); ok {
					_ = c.Close()
				}
			}()
		}
		tunnel.UpdateProxies(cfg.Proxies, cfg.Providers)
		tunnel.UpdateRules(cfg.Rules, cfg.SubRules, cfg.RuleProviders)
		e.routes = key
		e.warpMu.Lock()
		e.warp = nodeapi.WarpStatus{}
		e.warpMu.Unlock()
	}

	e.Reg.SetSlots(st.Slots)
	e.Reg.SetPolicies(st.Epoch, st.Policies)
	e.Reg.SetShared(sharedListeners(st))

	recreated := changedInbounds(e.applied, st)
	e.errsMu.Lock()
	for name := range cfg.Listeners {
		delete(e.errs, name)
	}
	e.errsMu.Unlock()
	listener.PatchInboundListeners(cfg.Listeners, e.tun, true)
	e.syncLogs()

	e.errsMu.Lock()
	failed := map[string]C.InboundListener{}
	statuses := make([]nodeapi.ListenerStatus, 0, len(cfg.Listeners))
	for name, l := range cfg.Listeners {
		ls := nodeapi.ListenerStatus{Name: name, OK: true}
		if msg, bad := e.errs[name]; bad {
			ls.OK, ls.Error = false, msg
		} else {
			failed[name] = l
		}
		statuses = append(statuses, ls)
	}
	hasFailures := len(failed) != len(cfg.Listeners)
	e.errsMu.Unlock()
	// A listener that failed to bind stays registered under its old instance in mihomo
	// and would be skipped as "unchanged" next time; re-patching without it forgets it.
	if hasFailures {
		listener.PatchInboundListeners(failed, e.tun, true)
	}
	sort.Slice(statuses, func(i, j int) bool { return statuses[i].Name < statuses[j].Name })

	e.applied = st
	e.listeners = map[string]nodeapi.ListenerStatus{}
	for _, s := range statuses {
		e.listeners[s.Name] = s
	}
	if err := e.saveState(st); err != nil {
		e.log.Error("save state", "err", err)
	}
	return nodeapi.ApplyResult{Revision: st.Revision, Recreated: recreated, Listeners: statuses}, nil
}

// Validate parses one inbound with mihomo's own parser without applying it, so the
// panel can refuse a template before it replaces a working listener.
func (e *Engine) Validate(req nodeapi.ValidateRequest) error {
	e.mu.Lock()
	cert := e.cert
	e.mu.Unlock()
	in := req.Inbound
	in.Name = "mikan-validate"
	probe := []nodeapi.Slot{{Name: "validate", UUID: "00000000-0000-4000-8000-000000000000", Secret: "validate"}}
	l, err := listenerFor(in, probe, cert, proto.Options{SelfStealPort: req.SelfStealPort})
	if err != nil {
		return err
	}
	_, err = listener.ParseListener(l)
	return err
}

// TargetAllowed says whether the node may test dest as a REALITY target for the panel:
// public sites, the panel's own port next to it (self-steal), anything in test setups.
func (e *Engine) TargetAllowed(dest string) bool {
	host, port, err := net.SplitHostPort(dest)
	if err != nil || host == "" {
		return false
	}
	if e.allowPrivate || proto.PublicHost(host) {
		return true
	}
	e.mu.Lock()
	self := e.applied.SelfStealPort
	e.mu.Unlock()
	return self > 0 && (host == "127.0.0.1" || host == "localhost") && port == strconv.Itoa(self)
}

func (e *Engine) SetPolicies(req nodeapi.PoliciesRequest) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.Reg.SetPolicies(req.Epoch, req.Policies)
	e.applied.Policies = req.Policies
	e.applied.Epoch = req.Epoch
	if err := e.saveState(e.applied); err != nil {
		e.log.Error("save state", "err", err)
	}
}

func (e *Engine) Health() nodeapi.Health {
	e.mu.Lock()
	ls := make([]nodeapi.ListenerStatus, 0, len(e.listeners))
	for _, s := range e.listeners {
		ls = append(ls, s)
	}
	rev := e.applied.Revision
	e.mu.Unlock()
	sort.Slice(ls, func(i, j int) bool { return ls[i].Name < ls[j].Name })
	return nodeapi.Health{
		Version: e.version, Core: "mihomo " + mihomoVersion(), Revision: rev, StartedAt: e.started,
		Listeners: ls, Conns: e.Reg.ConnCount(), System: e.sys.last(),
	}
}

func (e *Engine) Logs(since time.Time) []nodeapi.LogLine { return e.logs.since(since) }

// PersistCounters is called periodically and on shutdown; at most the last interval
// of traffic is lost if the process dies.
func (e *Engine) PersistCounters() error {
	raw, err := json.Marshal(e.Reg.snapshot())
	if err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(e.dataDir, countersFile), raw, 0o600)
}

func (e *Engine) saveState(st nodeapi.DesiredState) error {
	raw, err := json.Marshal(st)
	if err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(e.dataDir, stateFile), raw, 0o600)
}

// pumpLogs forwards mihomo's log stream: warnings and errors go to our logger and the
// ring buffer, listener bind errors are remembered for the listener status.
func (e *Engine) pumpLogs() {
	sub := mlog.Subscribe()
	for ev := range sub {
		msg := ev.Payload
		if strings.HasPrefix(msg, markerPrefix) {
			select {
			case e.marker <- msg:
			default:
			}
			continue
		}
		if ev.LogLevel < mlog.WARNING || connLine(msg) {
			continue
		}
		if name, rest, ok := parseListenErr(msg); ok {
			e.errsMu.Lock()
			e.errs[name] = rest
			e.errsMu.Unlock()
		}
		e.logs.add(nodeapi.LogLine{Time: time.Now(), Level: ev.Type(), Message: msg})
		if ev.LogLevel >= mlog.ERROR {
			e.log.Error("mihomo", "msg", msg)
		} else {
			e.log.Warn("mihomo", "msg", msg)
		}
	}
}

const markerPrefix = "mikan-sync "

// syncLogs waits until every log event emitted before this call has been processed:
// mihomo delivers events in order, so seeing our own marker is a barrier.
func (e *Engine) syncLogs() {
	id := make([]byte, 8)
	_, _ = rand.Read(id)
	want := markerPrefix + hex.EncodeToString(id)
	mlog.Warnln("%s", want)
	timeout := time.After(2 * time.Second)
	for {
		select {
		case got := <-e.marker:
			if got == want {
				return
			}
		case <-timeout:
			e.log.Warn("log barrier timed out; listener errors may be missing")
			return
		}
	}
}

// connLine reports per-connection lines ("[TCP] 1.2.3.4:5 --> host:443 error: ..."). They
// carry user IPs and destinations, so they never reach our logs, even as warnings.
func connLine(msg string) bool {
	return strings.HasPrefix(msg, "[TCP] ") || strings.HasPrefix(msg, "[UDP] ")
}

func parseListenErr(msg string) (name, reason string, ok bool) {
	const p = "Listener "
	const mid = " listen err: "
	if !strings.HasPrefix(msg, p) {
		return "", "", false
	}
	name, reason, ok = strings.Cut(msg[len(p):], mid)
	return name, reason, ok
}

func changedInbounds(prev, next nodeapi.DesiredState) []string {
	old := map[string]string{}
	for _, in := range prev.Inbounds {
		raw, _ := json.Marshal(in)
		old[in.Name] = string(raw)
	}
	slotsChanged := !sameSlots(prev.Slots, next.Slots)
	var out []string
	for _, in := range next.Inbounds {
		raw, _ := json.Marshal(in)
		if o, ok := old[in.Name]; !ok || o != string(raw) || slotsChanged {
			out = append(out, in.Name)
		}
	}
	return out
}

func sameSlots(a, b []nodeapi.Slot) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func mihomoVersion() string {
	if bi, ok := debug.ReadBuildInfo(); ok {
		for _, m := range bi.Deps {
			if m.Path == "github.com/metacubex/mihomo" {
				return m.Version
			}
		}
	}
	return "unknown"
}

func loadCounters(path string) (counterState, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		id := make([]byte, 8)
		_, _ = rand.Read(id)
		return counterState{Epoch: hex.EncodeToString(id)}, nil
	}
	if err != nil {
		return counterState{}, err
	}
	var cs counterState
	if err := json.Unmarshal(raw, &cs); err != nil {
		return counterState{}, fmt.Errorf("%s: %w", countersFile, err)
	}
	return cs, nil
}

func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
