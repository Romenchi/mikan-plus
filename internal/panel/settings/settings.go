package settings

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"

	"mikan/internal/panel/store/db"
)

const (
	KeyAdminPath  = "admin_path"
	KeySubPath    = "sub_path"
	KeyPublicHost = "public_host"
	KeyPanelPort  = "panel_port"
	// KeySubPort is a port of its own for subscriptions; 0 or unset: the panel's port.
	// The panel's port keeps serving subscriptions either way, for links handed out.
	KeySubPort   = "sub_port"
	KeyDomain    = "domain"
	KeyACMEEmail = "acme_email"
	KeyGroupMain = "sub_group_main" // subscription group names, see subs.Groups
	KeyGroupAuto = "sub_group_auto"
	KeyRouting   = "sub_routing" // subs.Routing
	KeyRules     = "sub_rules"   // the admin's own Clash rules, as typed (subs.ParseRules)
	// KeyFingerprint is the uTLS profile clients get where an inbound sets none
	// (proto.Fingerprints); unset means proto.DefaultFingerprint.
	KeyFingerprint = "client_fingerprint"
	// Automatic moves (internal/panel/autotune), on unless switched off.
	KeyAutoPort = "auto_port" // move an inbound whose port is blocked on the way to clients
	KeyAutoSNI  = "auto_sni"  // replace a REALITY target that stopped working
	// Devices (domain.Devices): bind subscriptions to devices, on unless switched off;
	// refuse apps that send no device id instead of seating them together, off by default.
	KeyDeviceBinding = "device_binding"
	KeyRequireHWID   = "device_require_hwid"
	// KeyDefaultLang is the language chosen at install: the admin panel and the subscription
	// page open in it until a visitor picks one, and new names (tariffs, the auto group, the
	// bot's menu) are written in it. "auto" or unset: the visitor's browser decides.
	KeyDefaultLang = "default_lang"
	// KeyAutoUpdate lets the host updater install new releases on its own, once a day;
	// off by default (internal/panel/updates).
	KeyAutoUpdate = "auto_update"
	// Branding and support: the bot's and the subscription page's name and the support link.
	KeyBrand      = "brand"
	KeySupportURL = "support_url"
	// KeyQuietHour is the UTC hour the slot pool is refilled, which reconnects QUIC clients.
	KeyQuietHour = "quiet_hour_utc"
)

// Switch is an on/off setting with its default: read it with On, so the default lives
// here and nowhere else.
type Switch struct {
	Key string
	Def bool
}

// The panel's switches.
var (
	AutoPort      = Switch{KeyAutoPort, true}
	AutoSNI       = Switch{KeyAutoSNI, true}
	DeviceBinding = Switch{KeyDeviceBinding, true}
	RequireHWID   = Switch{KeyRequireHWID, false}
	AutoUpdate    = Switch{KeyAutoUpdate, false}
)

// ValidLang says whether s is a language of the panel.
func ValidLang(s string) bool { return s == "ru" || s == "en" }

type Settings struct{ q *db.Queries }

func New(q *db.Queries) *Settings { return &Settings{q: q} }

// Get decodes the JSON value stored under key. ok is false when the key is absent.
func Get[T any](ctx context.Context, s *Settings, key string) (v T, ok bool, err error) {
	raw, err := s.q.GetSetting(ctx, key)
	if errors.Is(err, sql.ErrNoRows) {
		return v, false, nil
	}
	if err != nil {
		return v, false, err
	}
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return v, false, fmt.Errorf("setting %s: %w", key, err)
	}
	return v, true, nil
}

// GetOver decodes the value stored under key over def: what the stored JSON lacks, such
// as a field added after it was saved, keeps def's value.
func GetOver[T any](ctx context.Context, s *Settings, key string, def T) (T, bool, error) {
	raw, err := s.q.GetSetting(ctx, key)
	if errors.Is(err, sql.ErrNoRows) {
		return def, false, nil
	}
	if err != nil {
		return def, false, err
	}
	v := def
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return def, false, fmt.Errorf("setting %s: %w", key, err)
	}
	return v, true, nil
}

func Set[T any](ctx context.Context, s *Settings, key string, v T) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if err := s.q.SetSetting(ctx, db.SetSettingParams{Key: key, Value: string(raw)}); err != nil {
		return err
	}
	generation.Add(1)
	return nil
}

// generation counts the settings this process has written. Whoever keeps something built
// from settings checks it, and builds again when it moved. Changes made by another
// process (the CLI) do not move it: they are seen when what is kept expires.
var generation atomic.Uint64

// Generation is how many settings have been written by this process so far.
func Generation() uint64 { return generation.Load() }

func (s *Settings) String(ctx context.Context, key string) (string, error) {
	v, _, err := Get[string](ctx, s, key)
	return v, err
}

// On reads a switch; its default when it was never set.
func (s *Settings) On(ctx context.Context, sw Switch) (bool, error) {
	v, ok, err := Get[bool](ctx, s, sw.Key)
	if err != nil || !ok {
		return sw.Def, err
	}
	return v, nil
}

// Lang is the default language, "ru" or "en"; "" when the browser decides.
func (s *Settings) Lang(ctx context.Context) (string, error) {
	v, err := s.String(ctx, KeyDefaultLang)
	if err != nil || !ValidLang(v) {
		return "", err
	}
	return v, nil
}

type Paths struct {
	Admin string
	Sub   string
}

func (s *Settings) Paths(ctx context.Context) (Paths, error) {
	a, err := s.String(ctx, KeyAdminPath)
	if err != nil {
		return Paths{}, err
	}
	sub, err := s.String(ctx, KeySubPath)
	if err != nil {
		return Paths{}, err
	}
	return Paths{Admin: a, Sub: sub}, nil
}

// Endpoint is how clients reach the panel: host is an IP or a domain.
type Endpoint struct {
	Host string
	Port int
}

// SubEndpoint is where subscription links point: the subscription port when one is set.
func (s *Settings) SubEndpoint(ctx context.Context) (Endpoint, error) {
	ep, err := s.Endpoint(ctx)
	if err != nil {
		return ep, err
	}
	if p, _, err := Get[int](ctx, s, KeySubPort); err != nil {
		return ep, err
	} else if p > 0 {
		ep.Port = p
	}
	return ep, nil
}

func (s *Settings) Endpoint(ctx context.Context) (Endpoint, error) {
	host, err := s.String(ctx, KeyDomain)
	if err != nil {
		return Endpoint{}, err
	}
	if host == "" {
		if host, err = s.String(ctx, KeyPublicHost); err != nil {
			return Endpoint{}, err
		}
	}
	port, _, err := Get[int](ctx, s, KeyPanelPort)
	if err != nil {
		return Endpoint{}, err
	}
	return Endpoint{Host: host, Port: port}, nil
}
