package billing

import (
	"context"
	"database/sql"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mikan/internal/panel/addons"
	"mikan/internal/panel/settings"
	"mikan/internal/panel/store/db"
)

// A panel of 0.4.3 with the built-in YooKassa on and CryptoBot set up but off: the keys
// move to the adapters once, the host is asked for the adapter that took payments, and a
// payment opened before the update is paid through the adapter, also by a notification
// at the old URL.
func TestMoveBuiltin(t *testing.T) {
	e, fa, dir := adapterEnv(t, "yookassa")
	ctx := context.Background()
	set := e.s.d.Settings
	must(t, settings.Set(ctx, set, KeyConfig, map[string]any{"enabled": true, "stars": true, "allow_new": true, "renew_resets_traffic": true,
		"yookassa": true, "yookassa_shop_id": "123456", "cryptobot": false, "cryptobot_testnet": true}))
	must(t, settings.Set(ctx, set, keyLegacyYooKassaSecret, adapterSecret))
	must(t, settings.Set(ctx, set, keyLegacyCryptoBotToken, "12345:AAcryptoTOKEN"))
	old, err := e.st.Q.CreatePayment(ctx, db.CreatePaymentParams{Provider: legacyYooKassa, Payload: "legacy-payload", TgID: 555, Kind: "new",
		TariffID: sql.NullInt64{Int64: e.sale.ID, Valid: true}, TariffName: e.sale.Name, Amount: 19900, Currency: "RUB", CreatedAt: e.now.Unix()})
	must(t, err)
	must(t, e.st.Q.SetPaymentInvoice(ctx, db.SetPaymentInvoiceParams{ExternalID: sql.NullString{String: "yk-1", Valid: true}, PayUrl: "https://yoomoney.ru/x", ID: old.ID}))

	must(t, e.s.MoveBuiltin(ctx))
	yk, err := e.s.AddonConfig(ctx, legacyYooKassa)
	must(t, err)
	if !yk.Enabled || yk.Values["shop_id"] != "123456" || yk.Values["secret_key"] != adapterSecret {
		t.Fatalf("yookassa moved: %+v", yk)
	}
	cb, err := e.s.AddonConfig(ctx, legacyCryptoBot)
	must(t, err)
	if cb.Enabled || cb.Values["token"] != "12345:AAcryptoTOKEN" || cb.Values["testnet"] != true {
		t.Fatalf("cryptobot moved, off: %+v", cb)
	}
	for _, k := range []string{keyLegacyYooKassaSecret, keyLegacyCryptoBotToken} {
		if v, _ := set.String(ctx, k); v != "" {
			t.Fatalf("%s left behind", k)
		}
	}
	var raw string
	must(t, e.st.DB.QueryRowContext(ctx, "SELECT value FROM settings WHERE key = ?", KeyConfig).Scan(&raw))
	if strings.Contains(raw, "yookassa") || !strings.Contains(raw, `"enabled":true`) {
		t.Fatalf("payment settings: %s", raw)
	}
	if p := e.payment(old.ID); p.Provider != "addon:yookassa" || p.ExternalID.String != "yk-1" {
		t.Fatalf("the old payment: %+v", p)
	}
	if m, _ := e.s.Moving(ctx); len(m) != 1 || m[0] != legacyYooKassa {
		t.Fatalf("waiting for: %v", m)
	}

	// Run again (every start), it changes nothing: the admin's own changes stay.
	must(t, settings.Set(ctx, set, addonKey(legacyYooKassa), AddonConfig{Enabled: false, Values: yk.Values}))
	must(t, e.s.MoveBuiltin(ctx))
	if c, _ := e.s.AddonConfig(ctx, legacyYooKassa); c.Enabled {
		t.Fatal("a second start moved the keys again")
	}
	must(t, settings.Set(ctx, set, addonKey(legacyYooKassa), yk))

	// The host is asked for the adapter; until it runs, nothing is offered for rubles.
	e.s.Reconcile(ctx)
	if r, ok := e.s.d.Addons.Pending(); !ok || r.Action != "install" || r.ID != legacyYooKassa {
		t.Fatalf("install request: %+v %v", r, ok)
	}
	if e.s.Available(ctx).Rub() {
		t.Fatal("rubles offered before the adapter runs")
	}
	must(t, os.Remove(filepath.Join(dir, "addons", "request.json")))
	runAdapter(t, dir, legacyYooKassa, fa)
	fa.set("yk-1", func(s *addons.Status) { s.Status, s.Amount, s.Currency = "pending", 19900, "RUB" })
	e.s.Reconcile(ctx)
	if m, _ := e.s.Moving(ctx); len(m) != 0 {
		t.Fatalf("still waiting for: %v", m)
	}
	if av := e.s.Available(ctx); len(av.Addons) != 1 || av.Addons[0] != legacyYooKassa {
		t.Fatalf("available: %+v", av)
	}

	// YooKassa notifies the URL its dashboard has had since 0.4.0.
	fa.set("yk-1", func(s *addons.Status) { s.Status = "paid" })
	if code := e.hook(legacyYooKassa, e.token, "185.71.76.5", []byte(`{"id":"yk-1"}`), map[string]string{"X-Sig": "ok"}); code != http.StatusOK {
		t.Fatalf("the old webhook URL: %d", code)
	}
	if p := e.payment(old.ID); p.Status != "applied" {
		t.Fatalf("the old payment after the move: %s", p.Status)
	}
	if code := e.hook(legacyCryptoBot, "wrong-token-wrong-token-wrong-tok", "203.0.113.9", nil, nil); code != http.StatusNotFound {
		t.Fatalf("the old URL with a wrong token: %d", code)
	}
}

// A panel that never had the built-in providers set up has nothing to move.
func TestMoveBuiltinNothing(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	must(t, e.s.MoveBuiltin(ctx))
	if m, _ := e.s.Moving(ctx); len(m) != 0 {
		t.Fatalf("waiting for: %v", m)
	}
	if _, set, _ := settings.Get[AddonConfig](ctx, e.s.d.Settings, addonKey(legacyYooKassa)); set {
		t.Fatal("an adapter set up from nothing")
	}
	if AdapterOf("yookassa") != "addon:yookassa" || AdapterOf(Stars) != Stars || AdapterOf("addon:x") != "addon:x" {
		t.Fatal("AdapterOf")
	}
}
