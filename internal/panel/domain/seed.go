package domain

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"mikan/internal/panel/presets"
	"mikan/internal/panel/settings"
	"mikan/internal/panel/store"
	"mikan/internal/panel/store/db"
	"mikan/internal/proto"
)

// Seed creates the default inbounds, tariffs and slot pool on a fresh install.
// Each part is created only if its table is empty, so it is safe to call on every start.
// Tariffs are named in the default language set at bootstrap, Russian without one.
func Seed(ctx context.Context, st *store.Store, now time.Time) error {
	inbounds, err := st.Q.ListInbounds(ctx)
	if err != nil {
		return err
	}
	if len(inbounds) == 0 {
		for _, p := range presets.All {
			if !p.Default {
				continue
			}
			config, err := presets.NewConfig(p.ID, presets.DefaultDest)
			if err != nil {
				return err
			}
			if _, err := st.Q.CreateInbound(ctx, db.CreateInboundParams{NodeID: 1, Name: p.Name, Preset: p.ID, Port: p.Port, Config: config, CreatedAt: now.Unix(), UpdatedAt: now.Unix()}); err != nil {
				return err
			}
		}
	}
	// Inbounds created by mikan ≤ 0.1.2 carry per-preset settings; give them a template.
	for _, in := range inbounds {
		if in.Config != "" {
			continue
		}
		t, err := proto.FromPreset(in.Preset, []byte(in.Settings))
		if err != nil {
			return fmt.Errorf("inbound %s: %w", in.Name, err)
		}
		if err := st.Q.SetInboundConfig(ctx, db.SetInboundConfigParams{Config: proto.Marshal(t), ID: in.ID}); err != nil {
			return err
		}
	}
	if n, err := st.Q.CountTariffs(ctx); err != nil {
		return err
	} else if n == 0 {
		lang, err := settings.New(st.Q).Lang(ctx)
		if err != nil {
			return err
		}
		name := func(ru, en string) string {
			if lang == "en" {
				return en
			}
			return ru
		}
		defaults := []db.CreateTariffParams{
			{Name: name("Пробный", "Trial"), TrafficLimit: nullInt(5 * GiB), DurationDays: 3, DeviceLimit: nullInt(1), ResetStrategy: "none", Sort: 1},
			{Name: name("Стандарт", "Standard"), TrafficLimit: nullInt(150 * GiB), DurationDays: 30, DeviceLimit: nullInt(3), ResetStrategy: "period", Sort: 2},
			{Name: name("Безлимит", "Unlimited"), DurationDays: 30, DeviceLimit: nullInt(3), ResetStrategy: "none", Sort: 3},
		}
		for _, t := range defaults {
			t.CreatedAt = now.Unix()
			if _, err := st.Q.CreateTariff(ctx, t); err != nil {
				return err
			}
		}
	}
	stats, err := NewPool(st, func() time.Time { return now }).Stats(ctx)
	if err != nil {
		return err
	}
	if stats.Free+stats.Assigned+stats.Burned == 0 {
		return NewPool(st, func() time.Time { return now }).Refill(ctx, RefillBatch)
	}
	return nil
}

func nullInt(v int64) sql.NullInt64 { return sql.NullInt64{Int64: v, Valid: true} }
