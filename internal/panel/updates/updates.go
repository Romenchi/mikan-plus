// Package updates keeps the panel aware of new releases and talks to the host updater
// (the mikan command on the server) through files in the panel's data directory:
//
//	update/policy.json  the panel writes {"auto": true|false}; the daily timer on the host
//	                    updates only when it is on
//	update/request      the panel writes it for the Update button; a systemd path unit
//	                    runs `mikan update --requested`, which removes it first
//	update/status.json  the host writes how the last update went:
//	                    {"state": "running|ok|failed", "version", "from", "error", "at"}
//
// The panel has no Docker socket: updating is the host's job.
package updates

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"mikan/internal/fsutil"
	"mikan/internal/release"
)

// Source fetches the newest release's manifest, checked.
type Source func(ctx context.Context) (release.Manifest, error)

var (
	// ErrUnavailable: the panel runs without a data directory the host watches (tests,
	// UI development).
	ErrUnavailable = errors.New("updates_unavailable")
	// ErrNoRelease: the repository has no release yet; the UI says so in its language.
	ErrNoRelease = errors.New("no_release")
)

// Fetch reads the manifest at url and its signature at url + ".sig" and checks them
// against the release key.
func Fetch(url string) Source {
	pub, err := release.Key(release.PublicKey)
	if err != nil {
		panic(err)
	}
	return fetch(url, pub)
}

func fetch(url string, pub ed25519.PublicKey) Source {
	client := &http.Client{Timeout: 30 * time.Second}
	get := func(ctx context.Context, u string, limit int64) ([]byte, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return nil, err
		}
		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusNotFound {
			return nil, ErrNoRelease
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("GET %s: %s", u, resp.Status)
		}
		return io.ReadAll(io.LimitReader(resp.Body, limit))
	}
	once := func(ctx context.Context) (release.Manifest, error) {
		data, err := get(ctx, url, 1<<20)
		if err != nil {
			return release.Manifest{}, err
		}
		sig, err := get(ctx, url+".sig", 4096)
		if err != nil {
			return release.Manifest{}, err
		}
		return release.Parse(data, string(sig), pub)
	}
	return func(ctx context.Context) (release.Manifest, error) {
		m, err := once(ctx)
		// The manifest and its signature are two requests: a release published between them
		// leaves the new manifest with the old signature. Asking again gets a matching pair.
		if errors.Is(err, release.ErrSignature) {
			m, err = once(ctx)
		}
		return m, err
	}
}

type Checker struct {
	dir     string
	version string
	source  Source
	log     *slog.Logger
	now     func() time.Time

	mu      sync.Mutex
	latest  *release.Manifest
	checked time.Time
	err     string
}

// New makes a checker for the panel of version; dataDir "" or source nil turn parts off.
func New(dataDir, version string, source Source, log *slog.Logger, now func() time.Time) *Checker {
	c := &Checker{version: version, source: source, log: log, now: now}
	if dataDir != "" {
		c.dir = filepath.Join(dataDir, "update")
	}
	return c
}

// How often the release is looked for, and how soon again after a check that failed: a
// GitHub that was down at the moment must not leave "error" on the page for a day.
var (
	firstCheck = time.Minute
	checkEvery = 24 * time.Hour
	retryAfter = time.Hour
)

// Run checks a minute after the start, then once a day.
func (c *Checker) Run(ctx context.Context) {
	if c.source == nil {
		return
	}
	t := time.NewTimer(firstCheck)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			next := checkEvery
			// No release yet is an answer, not a failure.
			if err := c.check(ctx); err != nil && !errors.Is(err, ErrNoRelease) {
				next = retryAfter
			}
			t.Reset(next)
		}
	}
}

// Check asks for the newest release now.
func (c *Checker) Check(ctx context.Context) { _ = c.check(ctx) }

func (c *Checker) check(ctx context.Context) error {
	if c.source == nil {
		return nil
	}
	m, err := c.source(ctx)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.checked = c.now()
	if err != nil {
		c.err = err.Error()
		if c.log != nil {
			c.log.Warn("update check", "err", err)
		}
		return err
	}
	c.err = ""
	c.latest = &m
	return nil
}

type State struct {
	Current   string
	Latest    *release.Manifest
	CheckedAt time.Time
	Error     string
}

func (c *Checker) State() State {
	c.mu.Lock()
	defer c.mu.Unlock()
	return State{Current: c.version, Latest: c.latest, CheckedAt: c.checked, Error: c.err}
}

// Available says whether a newer release is out.
func (s State) Available() bool {
	return s.Latest != nil && release.Newer(s.Latest.Version, s.Current)
}

// SetAuto tells the host whether its daily check may update.
func (c *Checker) SetAuto(on bool) error {
	data, _ := json.Marshal(map[string]bool{"auto": on})
	return c.write("policy.json", data)
}

// Request asks the host to update now.
func (c *Checker) Request() error {
	data, _ := json.Marshal(map[string]string{"at": c.now().UTC().Format(time.RFC3339)})
	return c.write("request", data)
}

// Requested returns when the Update button was pressed, while the host has not taken
// the request yet.
func (c *Checker) Requested() (time.Time, bool) {
	if c.dir == "" {
		return time.Time{}, false
	}
	fi, err := os.Stat(filepath.Join(c.dir, "request"))
	if err != nil {
		return time.Time{}, false
	}
	return fi.ModTime(), true
}

// HostStatus is how the host's last update went.
type HostStatus struct {
	State   string `json:"state" enum:"running,ok,failed"`
	Version string `json:"version"`
	From    string `json:"from"`
	Error   string `json:"error"`
	At      string `json:"at" doc:"RFC 3339"`
}

func (c *Checker) Host() (HostStatus, bool) {
	var s HostStatus
	if c.dir == "" {
		return s, false
	}
	data, err := os.ReadFile(filepath.Join(c.dir, "status.json"))
	if err != nil || json.Unmarshal(data, &s) != nil || s.State == "" {
		return s, false
	}
	return s, true
}

func (c *Checker) write(name string, data []byte) error {
	if c.dir == "" {
		return ErrUnavailable
	}
	if err := os.MkdirAll(c.dir, 0o755); err != nil {
		return err
	}
	return fsutil.WriteFileAtomic(filepath.Join(c.dir, name), data, 0o644)
}
