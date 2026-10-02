package node

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mikan/internal/nodeapi"
	"mikan/internal/proto"
)

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// A broken state or counters file must not keep the node from starting: the systemd or
// Docker restart would loop on it until someone deletes the file over SSH. The file is
// kept aside and the node starts clean.
func TestBrokenFilesAreSetAsideNotFatal(t *testing.T) {
	for name, content := range map[string]string{"empty": "", "garbage": "{not json", "truncated": `{"epoch":"e1","seq":`} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			counters, state := filepath.Join(dir, countersFile), filepath.Join(dir, stateFile)
			for _, p := range []string{counters, state} {
				if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			cs, err := loadCounters(counters, quiet())
			if err != nil || cs.Epoch == "" || cs.Seq != 0 {
				t.Fatalf("counters: %+v, %v", cs, err)
			}
			_, ok, err := loadState(state, quiet())
			if err != nil || ok {
				t.Fatalf("state: ok=%v err=%v", ok, err)
			}
			for _, base := range []string{countersFile, stateFile} {
				if _, err := os.Stat(filepath.Join(dir, base)); !os.IsNotExist(err) {
					t.Fatalf("%s must be moved away: %v", base, err)
				}
				kept, _ := filepath.Glob(filepath.Join(dir, base+".corrupt-*"))
				if len(kept) != 1 {
					t.Fatalf("%s: the broken file is not kept: %v", base, kept)
				}
				if got, _ := os.ReadFile(kept[0]); string(got) != content {
					t.Fatalf("%s: the kept file differs: %q", base, got)
				}
			}
		})
	}
}

func TestSavedFilesLoad(t *testing.T) {
	dir := t.TempDir()
	if cs, err := loadCounters(filepath.Join(dir, countersFile), quiet()); err != nil || cs.Epoch == "" {
		t.Fatalf("no file yet: a new epoch, %+v %v", cs, err)
	}
	if _, ok, err := loadState(filepath.Join(dir, stateFile), quiet()); ok || err != nil {
		t.Fatalf("no file yet: nothing to apply, %v %v", ok, err)
	}
	e := &Engine{dataDir: dir, log: quiet(), Reg: NewRegistry("e1", 7, time.Minute, time.Now)}
	if err := e.saveState(nodeapi.DesiredState{Revision: 5, Epoch: "e1"}); err != nil {
		t.Fatal(err)
	}
	if err := e.PersistCounters(); err != nil {
		t.Fatal(err)
	}
	st, ok, err := loadState(filepath.Join(dir, stateFile), quiet())
	if err != nil || !ok || st.Revision != 5 {
		t.Fatalf("state: %+v ok=%v err=%v", st, ok, err)
	}
	cs, err := loadCounters(filepath.Join(dir, countersFile), quiet())
	if err != nil || cs.Epoch != "e1" || cs.Seq != 7 {
		t.Fatalf("counters: %+v %v", cs, err)
	}
}

func counted(r *Registry, slot string, up, down int64) {
	s := r.lookup(slot)
	s.count(up, down)
}

// The counters file is behind by up to ten seconds of batches when the node crashes, and
// the panel drops a batch whose seq it has stored already. The first traffic after a
// crash must therefore get a seq beyond anything the old process cut.
func TestRestoreSkipsAheadOfBatchesLostInACrash(t *testing.T) {
	r := NewRegistry("e1", 0, time.Minute, time.Now)
	r.SetSlots([]nodeapi.Slot{{Name: "s1", UUID: "u1"}})
	r.SetPolicies("e1", []nodeapi.Policy{{Slot: "s1", Allowed: true, QuotaRemaining: -1}})
	counted(r, "s1", 10, 20)
	b := r.Counters()
	if b.Seq != 1 || b.Slots["s1"].Up != 10 {
		t.Fatalf("first batch: %+v", b)
	}
	saved := r.snapshot() // written to disk here; the node then cuts seq 2..5 and dies
	for seq := int64(2); seq <= 5; seq++ {
		r.Ack("e1", seq-1)
		counted(r, "s1", 1, 1)
		if got := r.Counters().Seq; got != seq {
			t.Fatalf("batch %d has seq %d", seq, got)
		}
	}
	panelSeq := int64(5) // the panel stored all of them

	r2 := NewRegistry(saved.Epoch, saved.Seq, time.Minute, time.Now)
	r2.SetSlots([]nodeapi.Slot{{Name: "s1", UUID: "u1"}})
	r2.restore(saved)
	// The pending batch of the file is sent again under its own seq: the panel drops it.
	pend := r2.Counters()
	if pend.Seq != 1 || pend.Seq > panelSeq {
		t.Fatalf("the pending batch keeps its seq: %+v", pend)
	}
	r2.Ack("e1", 1)
	r2.SetPolicies("e1", []nodeapi.Policy{{Slot: "s1", Allowed: true, QuotaRemaining: -1}})
	counted(r2, "s1", 5, 5)
	fresh := r2.Counters()
	if fresh.Seq <= panelSeq {
		t.Fatalf("the first batch after the crash has seq %d, which the panel (at %d) would drop as a duplicate", fresh.Seq, panelSeq)
	}
	if fresh.Slots["s1"].Up != 5 {
		t.Fatalf("its traffic: %+v", fresh)
	}
}

// With no traffic nothing is cut: no seq burned, no pending batch, nothing to store or to
// acknowledge, and the live view still comes through.
func TestIdleCountersCutNoBatch(t *testing.T) {
	r := NewRegistry("e1", 3, time.Minute, time.Now)
	r.SetSlots([]nodeapi.Slot{{Name: "s1", UUID: "u1"}})
	r.SetPolicies("e1", []nodeapi.Policy{{Slot: "s1", Allowed: true, QuotaRemaining: -1}})
	r.admit("u1", "vless", "203.0.113.1", false)
	for range 3 {
		c := r.Counters()
		if !c.Idle || c.Seq != 3 || len(c.Slots) != 0 || c.Epoch != "e1" {
			t.Fatalf("idle reply: %+v", c)
		}
		if got := c.Online["s1"].IPs; len(got) != 1 || got[0] != "203.0.113.1" {
			t.Fatalf("the live view is still there: %+v", c.Online)
		}
	}
	if r.Ack("e1", 3) {
		t.Fatal("nothing is pending, so there is nothing to acknowledge")
	}
	counted(r, "s1", 100, 0)
	c := r.Counters()
	if c.Idle || c.Seq != 4 || c.Slots["s1"].Up != 100 {
		t.Fatalf("traffic cuts the next batch: %+v", c)
	}
	// Unacknowledged, the same batch comes back; acknowledged, the node is idle again.
	if again := r.Counters(); again.Seq != 4 || again.Idle {
		t.Fatalf("the pending batch repeats: %+v", again)
	}
	if !r.Ack("e1", 4) {
		t.Fatal("ack")
	}
	if c := r.Counters(); !c.Idle || c.Seq != 4 {
		t.Fatalf("idle again: %+v", c)
	}
}

// Apply runs before the counters are restored, so after a restart the quotas are set once
// more with the counters in place.
func TestRestoreRebasesQuotas(t *testing.T) {
	e := &Engine{dataDir: t.TempDir(), log: quiet(), Reg: NewRegistry("e1", 0, time.Minute, time.Now)}
	e.Reg.SetSlots([]nodeapi.Slot{{Name: "s1", UUID: "u1"}})
	policies := []nodeapi.Policy{{Slot: "s1", Allowed: true, QuotaRemaining: 1000}}
	e.applied = nodeapi.DesiredState{Epoch: "e1", Policies: policies}
	e.Reg.SetPolicies("e1", policies) // what Apply does on start
	e.restoreCounters(counterState{Epoch: "e1", Seq: 2, Current: map[string]nodeapi.Traffic{"s1": {Up: 400, Down: 200}}})
	if got := e.Reg.lookup("s1").remaining.Load(); got != 400 {
		t.Fatalf("remaining quota %d, want 400 (1000 minus the restored 600)", got)
	}
}

// Listeners with one key for everyone have no devices and no counters to keep: every
// packet of a busy UDP listener would otherwise take locks and grow maps nobody reads.
func TestSharedListenerKeepsNothing(t *testing.T) {
	r := NewRegistry("e1", 0, time.Minute, time.Now)
	r.SetShared([]string{"ss"})
	for i := range 500 {
		s := r.admit("", "ss", "198.51.100."+string(rune('a'+i%26)), true)
		if s == nil {
			t.Fatal("a shared listener takes everyone")
		}
		s.countIn(nil, 1000, 1000)
	}
	s := r.sharedSlot("ss")
	if len(s.ips) != 0 || len(s.seen) != 0 || s.up.Load() != 0 || s.down.Load() != 0 {
		t.Fatalf("a shared slot kept state: ips=%d seen=%d up=%d", len(s.ips), len(s.seen), s.up.Load())
	}
	if c := r.Counters(); len(c.Slots) != 0 || len(c.Online) != 0 {
		t.Fatalf("a shared listener is nobody's traffic: %+v", c)
	}
}

// A device that left is dropped from the slot, but not by a sweep on every packet.
func TestDevicesThatLeftAreSweptOncePerSecond(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	r := NewRegistry("e1", 0, 10*time.Second, func() time.Time { return now })
	r.SetSlots([]nodeapi.Slot{{Name: "s1", UUID: "u1"}})
	r.SetPolicies("e1", []nodeapi.Policy{{Slot: "s1", Allowed: true, QuotaRemaining: -1, DeviceLimit: 1}})
	r.admit("u1", "hy2", "203.0.113.1", false)
	now = now.Add(11 * time.Second)
	// The first device is gone: a second one takes its place at once.
	if r.admit("u1", "hy2", "203.0.113.2", false) == nil {
		t.Fatal("the place of a device that left is free")
	}
	// The same device's packets within the same second do not sweep again.
	swept := r.lookup("s1").cleaned
	for range 50 {
		r.admit("u1", "hy2", "203.0.113.2", false)
	}
	if !r.lookup("s1").cleaned.Equal(swept) {
		t.Fatal("the sweep ran again within the second")
	}
	if r.admit("u1", "hy2", "203.0.113.3", false) != nil {
		t.Fatal("the limit still holds for a third device")
	}
}

// The panel pushes the quotas left every half minute; they change with every byte, and
// the node does not write the whole state file for that, only for a real change.
func TestPoliciesPushWritesStateOnlyWhenTheyChange(t *testing.T) {
	dir := t.TempDir()
	e := &Engine{dataDir: dir, log: quiet(), Reg: NewRegistry("e1", 0, time.Minute, time.Now)}
	e.Reg.SetSlots([]nodeapi.Slot{{Name: "s1", UUID: "u1"}})
	file := filepath.Join(dir, stateFile)
	push := func(allowed bool, quota int64) {
		e.SetPolicies(nodeapi.PoliciesRequest{Epoch: "e1", Policies: []nodeapi.Policy{{Slot: "s1", Allowed: allowed, QuotaRemaining: quota, BaseSeq: quota}}})
	}
	mtime := func() time.Time {
		st, err := os.Stat(file)
		if err != nil {
			return time.Time{}
		}
		return st.ModTime()
	}
	push(true, 1000)
	first := mtime()
	if first.IsZero() {
		t.Fatal("a first push is saved")
	}
	time.Sleep(20 * time.Millisecond)
	push(true, 900)
	push(true, 800)
	if !mtime().Equal(first) {
		t.Fatal("only the quotas changed: the state file must not be written again")
	}
	if got := e.applied.Policies[0].QuotaRemaining; got != 800 {
		t.Fatalf("the quotas in memory are current: %d", got)
	}
	push(false, 800)
	if mtime().Equal(first) {
		t.Fatal("a policy that changed is saved")
	}
	// Going from a limit to none is a change too.
	changed := mtime()
	time.Sleep(20 * time.Millisecond)
	push(false, -1)
	if mtime().Equal(changed) {
		t.Fatal("unlimited is a change of shape")
	}
}

// A panel newer than the node sends fields the node does not know; they must not turn
// the push of policies or state into an error.
func TestNodeAPIIgnoresUnknownFields(t *testing.T) {
	e := &Engine{dataDir: t.TempDir(), log: quiet(), Reg: NewRegistry("e1", 0, time.Minute, time.Now)}
	srv := httptest.NewServer(Handler(e, quiet()))
	defer srv.Close()
	body := `{"epoch":"e1","policies":[{"slot":"s1","allowed":true,"quota_remaining":-1,"base_seq":0,"from_the_future":{"x":1}}],"also_new":true}`
	req, _ := http.NewRequest(http.MethodPut, srv.URL+"/v1/policies", strings.NewReader(body))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if e.applied.Epoch != "e1" || len(e.applied.Policies) != 1 {
		t.Fatalf("the known fields were not applied: %+v", e.applied)
	}
	// Garbage is still refused.
	resp, err = http.Post(srv.URL+"/v1/validate", "application/json", bytes.NewReader([]byte(`{`)))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("broken JSON: status %d", resp.StatusCode)
	}
}

// One inbound the node cannot run must not stop the node from taking the rest of the
// state: the others still start, and the broken one is reported by name.
func TestBrokenInboundIsRejectedAlone(t *testing.T) {
	good := nodeapi.Inbound{Name: "hysteria2", Port: "2443", Config: []byte(`{"type":"hysteria2","alpn":["h3"]}`)}
	bad := nodeapi.Inbound{Name: "mystery", Port: "2444", Config: []byte(`{"type":"nope"}`)}
	st := nodeapi.DesiredState{
		Inbounds: []nodeapi.Inbound{bad, good},
		Slots:    []nodeapi.Slot{{Name: "s1", UUID: "00000000-0000-4000-8000-000000000001", Secret: "x"}},
	}
	raw, rejected, err := buildConfig(st, proto.Cert{CertPath: "/tmp/c.pem", KeyPath: "/tmp/k.pem"}, false)
	if err != nil {
		t.Fatalf("one bad inbound failed the whole state: %v", err)
	}
	if len(rejected) != 1 || rejected[0].Name != "mystery" || rejected[0].OK || rejected[0].Error == "" {
		t.Fatalf("rejected: %+v", rejected)
	}
	var cfg struct {
		Listeners []map[string]any `json:"listeners"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil || len(cfg.Listeners) != 1 || cfg.Listeners[0]["name"] != "hysteria2" {
		t.Fatalf("the good inbound runs on: %s %v", raw, err)
	}
	if got := withoutRejected([]string{"mystery", "hysteria2"}, rejected); len(got) != 1 || got[0] != "hysteria2" {
		t.Fatalf("a rejected inbound is not 'recreated': %v", got)
	}
}
