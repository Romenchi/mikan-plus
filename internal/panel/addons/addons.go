// Package addons runs the payment adapters of the marketplace (getmikan/marketplace): the
// signed catalog, what the host installed, the install and remove requests to the host,
// and the adapter protocol (client.go). The contract is protocol v1, see PROTOCOL.md in the
// marketplace repository.
//
// The panel has no Docker socket: containers are the host's job. They talk through files
// in the panel's data directory, as updates do:
//
//	addons/request.json  the panel: {"action": "install|remove", "id", "at"}; it then wakes
//	                     the host with update/request {"do": "addons"}
//	addons/state.json    the host: what runs where, with the token, and how the last
//	                     request went
package addons

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"

	"mikan/internal/fsutil"
	"mikan/internal/release"
)

// CatalogURL is where the signed catalog is published, next to its .sig.
const CatalogURL = "https://github.com/getmikan/marketplace/releases/latest/download/index.json"

// Protocol is the adapter protocol this panel speaks.
const Protocol = 1

// AdapterTimeout bounds one call to an adapter: some make two calls to their provider.
const AdapterTimeout = 40 * time.Second

var (
	ErrUnavailable  = errors.New("addons_unavailable") // no data directory (tests, the CLI)
	ErrUnknown      = errors.New("addon_unknown")
	ErrNotInstalled = errors.New("addon_not_installed")
	ErrBusy         = errors.New("addon_busy") // a request is still with the host
)

// validID: adapter ids are path and provider-name safe.
var validID = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

func ValidID(id string) bool { return validID.MatchString(id) }

// Text is a string per language: {"ru": …, "en": …}.
type Text map[string]string

// In picks the text of lang, else English, else any.
func (t Text) In(lang string) string {
	if s := t[lang]; s != "" {
		return s
	}
	if s := t["en"]; s != "" {
		return s
	}
	for _, s := range t {
		return s
	}
	return ""
}

// Entry is one adapter of the catalog.
type Entry struct {
	ID          string `json:"id"`
	Name        Text   `json:"name"`
	Description Text   `json:"description"`
	Version     string `json:"version"`
	Protocol    int    `json:"protocol"`
	Image       string `json:"image"`
	Digest      string `json:"digest"`
	MinPanel    string `json:"min_panel"`
	Homepage    string `json:"homepage"`
}

type Catalog struct {
	Version  int       `json:"version"`
	Updated  time.Time `json:"updated"`
	Adapters []Entry   `json:"adapters"`
}

// ParseCatalog checks the signature over the exact bytes, then the entries: what this
// panel cannot run (another protocol, a newer panel, a malformed entry) is left out.
func ParseCatalog(data []byte, sig string, pub ed25519.PublicKey, panelVersion string) (Catalog, error) {
	raw, err := base64.StdEncoding.DecodeString(string(trimSpace([]byte(sig))))
	if err != nil || len(pub) != ed25519.PublicKeySize || !ed25519.Verify(pub, data, raw) {
		return Catalog{}, errors.New("addons: the catalog signature does not match")
	}
	var c Catalog
	if err := json.Unmarshal(data, &c); err != nil {
		return Catalog{}, fmt.Errorf("addons: catalog: %w", err)
	}
	out := c.Adapters[:0]
	for _, e := range c.Adapters {
		if !ValidID(e.ID) || e.Protocol != Protocol || e.Image == "" || !validDigest.MatchString(e.Digest) {
			continue
		}
		if e.MinPanel != "" && panelVersion != "dev" && release.Newer(e.MinPanel, panelVersion) {
			continue
		}
		out = append(out, e)
	}
	c.Adapters = out
	return c, nil
}

var validDigest = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

func trimSpace(b []byte) []byte {
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == '\r' || b[len(b)-1] == ' ') {
		b = b[:len(b)-1]
	}
	return b
}

// Installed is an adapter the host runs.
type Installed struct {
	Version string `json:"version"`
	Digest  string `json:"digest"`
	Listen  string `json:"listen"`
	Token   string `json:"token"`
	Status  string `json:"status"` // running | failed
	Error   string `json:"error,omitempty"`
	At      string `json:"at"`
}

// LastRequest is how the host handled the panel's last request.
type LastRequest struct {
	ID     string `json:"id"`
	Action string `json:"action"`
	State  string `json:"state"` // done | failed
	Error  string `json:"error,omitempty"`
	At     string `json:"at"`
}

type State struct {
	Adapters map[string]Installed `json:"adapters"`
	Request  *LastRequest         `json:"request,omitempty"`
}

// Request is what the panel asks the host to do.
type Request struct {
	Action string `json:"action"` // install | remove
	ID     string `json:"id"`
	At     string `json:"at"`
}

// Manager reads the catalog and the host's state, and talks to the adapters.
type Manager struct {
	dir, updateDir string // "" without a data directory
	catalogURL     string
	pub            ed25519.PublicKey
	version        string
	hc, adapters   *http.Client
	log            *slog.Logger
	now            func() time.Time

	mu      sync.Mutex
	catalog *Catalog
	fetched time.Time
	infos   map[string]cachedInfo
}

type cachedInfo struct {
	digest string
	info   Info
}

// New: dataDir is the panel's data directory ("" turns the host side off); catalogURL ""
// is the marketplace's own.
func New(dataDir, catalogURL, panelVersion string, log *slog.Logger, now func() time.Time) *Manager {
	if catalogURL == "" {
		catalogURL = CatalogURL
	}
	pub, _ := release.Key(release.PublicKey)
	m := &Manager{catalogURL: catalogURL, pub: pub, version: panelVersion, log: log, now: now,
		hc: &http.Client{Timeout: 15 * time.Second}, adapters: &http.Client{Timeout: AdapterTimeout}, infos: map[string]cachedInfo{}}
	if dataDir != "" {
		m.dir, m.updateDir = filepath.Join(dataDir, "addons"), filepath.Join(dataDir, "update")
	}
	return m
}

// Supported: the panel sees the server's data directory, so the host can run adapters.
func (m *Manager) Supported() bool { return m.dir != "" }

// SetKey replaces the catalog key (tests sign with their own).
func (m *Manager) SetKey(pub ed25519.PublicKey) { m.pub = pub }

// Catalog is the signed catalog, fetched at most every ten minutes.
func (m *Manager) Catalog(ctx context.Context) (Catalog, error) {
	m.mu.Lock()
	if m.catalog != nil && m.now().Sub(m.fetched) < 10*time.Minute {
		c := *m.catalog
		m.mu.Unlock()
		return c, nil
	}
	m.mu.Unlock()
	data, err := m.get(ctx, m.catalogURL, 1<<20)
	if err != nil {
		return Catalog{}, err
	}
	sig, err := m.get(ctx, m.catalogURL+".sig", 4<<10)
	if err != nil {
		return Catalog{}, err
	}
	c, err := ParseCatalog(data, string(sig), m.pub, m.version)
	if err != nil {
		return Catalog{}, err
	}
	m.mu.Lock()
	m.catalog, m.fetched = &c, m.now()
	m.mu.Unlock()
	return c, nil
}

func (m *Manager) get(ctx context.Context, url string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := m.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("addons: catalog: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("addons: catalog: HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, limit))
}

// State is what the host reports; nothing installed when it has not written anything.
func (m *Manager) State() (State, error) {
	s := State{Adapters: map[string]Installed{}}
	if m.dir == "" {
		return s, nil
	}
	data, err := os.ReadFile(filepath.Join(m.dir, "state.json"))
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return s, fmt.Errorf("addons: state.json: %w", err)
	}
	if s.Adapters == nil {
		s.Adapters = map[string]Installed{}
	}
	return s, nil
}

// Pending is the request the host has not taken yet.
func (m *Manager) Pending() (Request, bool) {
	var r Request
	if m.dir == "" {
		return r, false
	}
	data, err := os.ReadFile(filepath.Join(m.dir, "request.json"))
	if err != nil || json.Unmarshal(data, &r) != nil {
		return r, false
	}
	return r, true
}

// Ask asks the host to install or remove an adapter. The host takes one request at a time.
func (m *Manager) Ask(action, id string) error {
	if m.dir == "" {
		return ErrUnavailable
	}
	if (action != "install" && action != "remove") || !ValidID(id) {
		return ErrUnknown
	}
	if _, busy := m.Pending(); busy {
		return ErrBusy
	}
	at := m.now().UTC().Format(time.RFC3339)
	if err := writeFile(m.dir, "request.json", Request{Action: action, ID: id, At: at}); err != nil {
		return err
	}
	// The host's update request unit wakes up on this file: no unit of its own needed. An
	// update the admin asked for stays asked: the host takes the adapters after it.
	if _, err := os.Stat(filepath.Join(m.updateDir, "request")); err == nil {
		return nil
	}
	return writeFile(m.updateDir, "request", map[string]string{"at": at, "do": "addons"})
}

func writeFile(dir, name string, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return fsutil.WriteFileAtomic(filepath.Join(dir, name), data, 0o644)
}

// Client talks to an installed, running adapter.
func (m *Manager) Client(id string) (*Client, error) {
	s, err := m.State()
	if err != nil {
		return nil, err
	}
	a, ok := s.Adapters[id]
	if !ok || a.Status != "running" || a.Listen == "" {
		return nil, ErrNotInstalled
	}
	return NewClient("http://"+a.Listen, a.Token, m.adapters), nil
}

// Info is the adapter's own description, asked once per installed image.
func (m *Manager) Info(ctx context.Context, id string) (Info, error) {
	s, err := m.State()
	if err != nil {
		return Info{}, err
	}
	a, ok := s.Adapters[id]
	if !ok || a.Status != "running" {
		return Info{}, ErrNotInstalled
	}
	m.mu.Lock()
	if c, ok := m.infos[id]; ok && c.digest == a.Digest {
		m.mu.Unlock()
		return c.info, nil
	}
	m.mu.Unlock()
	info, err := NewClient("http://"+a.Listen, a.Token, m.adapters).Info(ctx)
	if err != nil {
		return Info{}, err
	}
	if info.ID != id || info.Protocol != Protocol {
		return Info{}, fmt.Errorf("addons: %s answers as %q, protocol %d", id, info.ID, info.Protocol)
	}
	m.mu.Lock()
	m.infos[id] = cachedInfo{digest: a.Digest, info: info}
	m.mu.Unlock()
	return info, nil
}
