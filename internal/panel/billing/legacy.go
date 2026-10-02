package billing

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"mikan/internal/panel/addons"
	"mikan/internal/panel/settings"
)

// YooKassa and CryptoBot were built into the panel until 0.4.3; since 0.4.4 they are the
// marketplace's adapters of the same names. What the built-in ones left behind leads
// there: their keys move to the adapters' settings once, payments made with them carry
// the adapter's provider name, and the webhook URLs set in their dashboards and the bot's
// old pay buttons still work.

// The built-in providers' names, as payments, webhook URLs and settings keep them.
const (
	legacyYooKassa  = "yookassa"
	legacyCryptoBot = "cryptobot"
)

// What the built-in providers kept besides Config.
const (
	keyLegacyYooKassaSecret = "pay_yookassa_secret"
	keyLegacyCryptoBotToken = "pay_cryptobot_token"
	// KeyMoving lists the adapters whose keys moved and that the host still has to
	// install; the Payments page tells the admin while it is not empty.
	KeyMoving = "pay_moving"
)

// legacyConfig is the part of the payment settings the built-in providers had.
type legacyConfig struct {
	YooKassa  bool   `json:"yookassa"`
	ShopID    string `json:"yookassa_shop_id"`
	CryptoBot bool   `json:"cryptobot"`
	Testnet   bool   `json:"cryptobot_testnet"`
}

// AdapterOf is the adapter that took over a built-in provider's name, the name itself
// otherwise.
func AdapterOf(provider string) string {
	if provider == legacyYooKassa || provider == legacyCryptoBot {
		return AddonPrefix + provider
	}
	return provider
}

// MoveBuiltin moves what the built-in YooKassa and CryptoBot left to their adapters. It
// runs at every start and does nothing once they are gone: keys move once (an adapter
// already set up keeps its own), the built-in keys and switches are then cleared, and
// payments made with them take the adapters' names, whose ids are the providers' own.
func (s *Service) MoveBuiltin(ctx context.Context) error {
	old, found, err := settings.Get[legacyConfig](ctx, s.d.Settings, KeyConfig)
	if err != nil {
		return err
	}
	ykSecret, err := s.d.Settings.String(ctx, keyLegacyYooKassaSecret)
	if err != nil {
		return err
	}
	cbToken, err := s.d.Settings.String(ctx, keyLegacyCryptoBotToken)
	if err != nil {
		return err
	}
	type move struct {
		id     string
		on     bool
		values addons.Settings
	}
	var moves []move
	if old.ShopID != "" && ykSecret != "" {
		moves = append(moves, move{legacyYooKassa, old.YooKassa, addons.Settings{"shop_id": old.ShopID, "secret_key": ykSecret}})
	}
	if cbToken != "" {
		moves = append(moves, move{legacyCryptoBot, old.CryptoBot, addons.Settings{"token": cbToken, "testnet": old.Testnet}})
	}
	moving, err := s.Moving(ctx)
	if err != nil {
		return err
	}
	for _, m := range moves {
		_, set, err := settings.Get[AddonConfig](ctx, s.d.Settings, addonKey(m.id))
		if err != nil {
			return err
		}
		if !set {
			if err := settings.Set(ctx, s.d.Settings, addonKey(m.id), AddonConfig{Enabled: m.on, Values: m.values}); err != nil {
				return err
			}
			s.d.Log.Info("billing: built-in provider moved to its adapter", "provider", m.id, "enabled", m.on)
		}
		// Only one that took payments needs its adapter now; the others wait for the admin.
		if m.on && !slices.Contains(moving, m.id) {
			moving = append(moving, m.id)
		}
	}
	if len(moves) > 0 {
		if err := settings.Set(ctx, s.d.Settings, KeyMoving, moving); err != nil {
			return err
		}
		for _, k := range []string{keyLegacyYooKassaSecret, keyLegacyCryptoBotToken} {
			if err := settings.Set(ctx, s.d.Settings, k, ""); err != nil {
				return err
			}
		}
	}
	if found && (old != legacyConfig{}) {
		// Saved again, the payment settings lose the built-in providers' fields.
		c, err := s.LoadConfig(ctx)
		if err != nil {
			return err
		}
		if err := settings.Set(ctx, s.d.Settings, KeyConfig, c); err != nil {
			return err
		}
	}
	res, err := s.d.Store.DB.ExecContext(ctx, `UPDATE payments SET provider = 'addon:' || provider WHERE provider IN (?, ?)`, legacyYooKassa, legacyCryptoBot)
	if err != nil {
		return fmt.Errorf("billing: rename built-in payments: %w", err)
	}
	if n, _ := res.RowsAffected(); n > 0 {
		s.d.Log.Info("billing: payments of the built-in providers now go through their adapters", "payments", n)
	}
	return nil
}

// Moving is the adapters the move still waits for.
func (s *Service) Moving(ctx context.Context) ([]string, error) {
	v, _, err := settings.Get[[]string](ctx, s.d.Settings, KeyMoving)
	return v, err
}

// installMoved asks the host for the adapters the move waits for, one request at a time,
// and forgets those that run. Reconcile calls it.
func (s *Service) installMoved(ctx context.Context) {
	moving, err := s.Moving(ctx)
	if err != nil || len(moving) == 0 || s.d.Addons == nil {
		return
	}
	st, err := s.d.Addons.State()
	if err != nil {
		return
	}
	left := moving[:0]
	for _, id := range moving {
		if a, ok := st.Adapters[id]; !ok || a.Status != "running" {
			left = append(left, id)
		}
	}
	if len(left) != len(moving) {
		_ = settings.Set(ctx, s.d.Settings, KeyMoving, left)
	}
	if len(left) == 0 || !s.d.Addons.Supported() {
		return
	}
	if _, waiting := s.d.Addons.Pending(); waiting {
		return
	}
	// A request the host failed is not repeated every minute: the admin sees why on the
	// Payments page and asks again there.
	if r := st.Request; r != nil && r.ID == left[0] && r.Action == "install" && r.State == "failed" {
		return
	}
	if err := s.d.Addons.Ask("install", left[0]); err != nil && !errors.Is(err, addons.ErrBusy) {
		s.d.Log.Warn("billing: ask the host for an adapter", "addon", left[0], "err", err)
	}
}
