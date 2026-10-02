package billing

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"mikan/internal/panel/domain"
	"mikan/internal/panel/settings"
	"mikan/internal/panel/store"
	"mikan/internal/panel/store/db"
)

type noChanges struct{}

func (noChanges) PoliciesChanged() {}
func (noChanges) SlotsChanged()    {}

// fakeTG is the bot: Stars links, refunds and what buyers were told.
type fakeTG struct {
	mu      sync.Mutex
	paid    []db.Payment
	refunds []string
}

func (f *fakeTG) InvoiceLink(_ context.Context, _, _, payload string, stars int64) (string, error) {
	return "https://t.me/$" + payload[:8] + "?stars=" + strconv.FormatInt(stars, 10), nil
}
func (f *fakeTG) RefundStars(_ context.Context, _ int64, charge string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.refunds = append(f.refunds, charge)
	return nil
}
func (f *fakeTG) Paid(_ context.Context, p db.Payment, _ db.User, _ bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.paid = append(f.paid, p)
}
func (f *fakeTG) BotURL(context.Context) string { return "https://t.me/mikan_test_bot" }
func (f *fakeTG) told() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.paid)
}

type env struct {
	t     *testing.T
	st    *store.Store
	s     *Service
	tg    *fakeTG
	now   time.Time
	logs  *bytes.Buffer
	sale  db.Tariff
	token string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	ctx := context.Background()
	e := &env{t: t, now: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC), logs: &bytes.Buffer{}, tg: &fakeTG{}}
	var err error
	if e.st, err = store.Open(ctx, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.st.Close() })
	if err := domain.Seed(ctx, e.st, e.now); err != nil {
		t.Fatal(err)
	}
	clock := func() time.Time { return e.now }
	set := settings.New(e.st.Q)
	e.s = New(Deps{Store: e.st, Settings: set, Users: domain.NewUsers(e.st, domain.NewPool(e.st, clock), noChanges{}, clock),
		Log: slog.New(slog.NewTextHandler(e.logs, &slog.HandlerOptions{Level: slog.LevelDebug})), Now: clock})
	e.s.SetTelegram(e.tg)
	must(t, settings.Set(ctx, set, KeyConfig, Config{Enabled: true, Stars: true, AllowNew: true, RenewResetsTraffic: true}))
	ts, _ := e.st.Q.ListTariffs(ctx)
	std := ts[1]
	e.sale, err = e.st.Q.UpdateTariff(ctx, db.UpdateTariffParams{Name: std.Name, TrafficLimit: std.TrafficLimit, DurationDays: 30, DeviceLimit: std.DeviceLimit,
		ResetStrategy: std.ResetStrategy, Sort: std.Sort, PriceStars: sql.NullInt64{Int64: 150, Valid: true}, PriceRub: sql.NullInt64{Int64: 19900, Valid: true}, OnSale: 1, ID: std.ID})
	must(t, err)
	_ = e.st.Q.UpsertTgChat(ctx, db.UpsertTgChatParams{TgID: 555, Username: "buyer", CreatedAt: e.now.Unix(), UpdatedAt: e.now.Unix()})
	e.token, err = e.s.WebhookToken(ctx)
	must(t, err)
	return e
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func (e *env) invoice(tg, user int64, provider string) db.Payment {
	e.t.Helper()
	p, err := e.s.Invoice(context.Background(), InvoiceRequest{TgID: tg, UserID: user, TariffID: e.sale.ID, Provider: provider})
	if err != nil {
		e.t.Fatalf("invoice %s: %v", provider, err)
	}
	if p.PayUrl == "" || p.Status != "pending" {
		e.t.Fatalf("invoice %s: %+v", provider, p)
	}
	return p
}

func (e *env) payment(id int64) db.Payment {
	e.t.Helper()
	p, err := e.st.Q.GetPayment(context.Background(), id)
	must(e.t, err)
	return p
}

func (e *env) users() int {
	var n int
	_ = e.st.DB.QueryRowContext(context.Background(), "SELECT count(*) FROM users").Scan(&n)
	return n
}

func (e *env) hook(provider, token, ip string, body []byte, hdr map[string]string) int {
	r := httptest.NewRequest(http.MethodPost, "/"+provider+"/"+token, bytes.NewReader(body))
	r.RemoteAddr = ip + ":40000"
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	e.s.Webhook().ServeHTTP(w, r)
	return w.Code
}

// A new buyer pays in Stars: one subscription, linked to the account, told once — however
// many times or how concurrently Telegram reports the payment.
func TestStarsNewSubscription(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	before := e.users()
	p := e.invoice(555, 0, Stars)
	if p.Amount != 150 || p.Currency != "XTR" || p.Kind != "new" {
		t.Fatalf("invoice: %+v", p)
	}
	// Asking again within minutes shows the same invoice.
	if again := e.invoice(555, 0, Stars); again.ID != p.ID {
		t.Fatalf("a second invoice for the same purchase: %d", again.ID)
	}
	for _, c := range []struct {
		name         string
		tg           int64
		payload, cur string
		amount       int64
	}{
		{"other account", 556, p.Payload, "XTR", 150},
		{"other amount", 555, p.Payload, "XTR", 1},
		{"other currency", 555, p.Payload, "USD", 150},
		{"unknown payload", 555, "nope", "XTR", 150},
	} {
		if err := e.s.PreCheckout(ctx, c.tg, c.payload, c.cur, c.amount); err == nil {
			t.Errorf("pre-checkout %s accepted", c.name)
		}
		if err := e.s.StarsPaid(ctx, c.tg, c.payload, "ch-x", c.cur, c.amount); err == nil {
			t.Errorf("payment %s accepted", c.name)
		}
	}
	must(t, e.s.PreCheckout(ctx, 555, p.Payload, "XTR", 150))

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = e.s.StarsPaid(ctx, 555, p.Payload, "ch-1", "XTR", 150)
		}()
	}
	wg.Wait()
	// A different charge id for the same invoice changes nothing either.
	_ = e.s.StarsPaid(ctx, 555, p.Payload, "ch-2", "XTR", 150)
	got := e.payment(p.ID)
	if got.Status != "applied" || got.ExternalID.String != "ch-1" || !got.UserID.Valid {
		t.Fatalf("payment: %+v", got)
	}
	if e.users() != before+1 || e.tg.told() != 1 {
		t.Fatalf("users %d (was %d), told %d", e.users(), before, e.tg.told())
	}
	link, err := e.st.Q.GetTgLink(ctx, got.UserID.Int64)
	if err != nil || link.TgID != 555 {
		t.Fatalf("link: %+v %v", link, err)
	}
	u, _ := e.st.Q.GetUser(ctx, got.UserID.Int64)
	if u.Name != "@buyer" || time.Unix(u.ExpiresAt.Int64, 0).Sub(e.now) != 30*24*time.Hour {
		t.Fatalf("user: %+v", u)
	}
	// Paid once: the invoice cannot be paid again.
	if err := e.s.PreCheckout(ctx, 555, p.Payload, "XTR", 150); err == nil {
		t.Fatal("an applied invoice passed pre-checkout")
	}
	// Refund: Stars go back through the bot, the payment says so.
	must(t, e.s.Refund(ctx, p.ID))
	if e.payment(p.ID).Status != "refunded" || len(e.tg.refunds) != 1 || e.tg.refunds[0] != "ch-1" {
		t.Fatalf("refund: %+v %v", e.payment(p.ID), e.tg.refunds)
	}
	if err := e.s.Refund(ctx, p.ID); !errors.Is(err, ErrNotRefunable) {
		t.Fatalf("second refund: %v", err)
	}
}

// Renewal: only the owner renews, the term goes on from the current end, the traffic
// period starts anew.
func TestRenewal(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	clock := func() time.Time { return e.now }
	u, err := domain.NewUsers(e.st, domain.NewPool(e.st, clock), noChanges{}, clock).Create(ctx, domain.CreateInput{Name: "a", TariffID: e.sale.ID})
	must(t, err)
	must(t, e.st.Q.LinkTg(ctx, db.LinkTgParams{UserID: u.ID, TgID: 555, CreatedAt: e.now.Unix()}))
	_, err = e.st.DB.ExecContext(ctx, "UPDATE users SET used_up = 1000, used_down = 2000 WHERE id = ?", u.ID)
	must(t, err)

	if _, err := e.s.Invoice(ctx, InvoiceRequest{TgID: 777, UserID: u.ID, TariffID: e.sale.ID, Provider: Stars}); !errors.Is(err, ErrNotYours) {
		t.Fatalf("someone else's subscription: %v", err)
	}
	p := e.invoice(555, u.ID, Stars)
	must(t, e.s.StarsPaid(ctx, 555, p.Payload, "ch-r", "XTR", 150))
	after, _ := e.st.Q.GetUser(ctx, u.ID)
	if after.ExpiresAt.Int64 != u.ExpiresAt.Int64+30*24*3600 || after.UsedUp+after.UsedDown != 0 || after.Status != "active" {
		t.Fatalf("renewed: expires %d (was %d), used %d", after.ExpiresAt.Int64, u.ExpiresAt.Int64, after.UsedUp+after.UsedDown)
	}
	if e.payment(p.ID).UserID.Int64 != u.ID {
		t.Fatal("payment not tied to the renewed user")
	}
}

func TestInvoiceRefusals(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	ts, _ := e.st.Q.ListTariffs(ctx)
	if _, err := e.s.Invoice(ctx, InvoiceRequest{TgID: 555, TariffID: ts[0].ID, Provider: Stars}); !errors.Is(err, ErrNotForSale) {
		t.Fatalf("not on sale: %v", err)
	}
	if _, err := e.s.Invoice(ctx, InvoiceRequest{TgID: 555, TariffID: e.sale.ID, Provider: "paypal"}); !errors.Is(err, ErrProviderOff) {
		t.Fatalf("unknown provider: %v", err)
	}
	must(t, settings.Set(ctx, e.s.d.Settings, KeyConfig, Config{Enabled: true, Stars: true, AllowNew: false}))
	if _, err := e.s.Invoice(ctx, InvoiceRequest{TgID: 555, TariffID: e.sale.ID, Provider: "addon:yookassa"}); !errors.Is(err, ErrProviderOff) {
		t.Fatalf("an adapter that is not installed: %v", err)
	}
	if _, err := e.s.Invoice(ctx, InvoiceRequest{TgID: 555, TariffID: e.sale.ID, Provider: Stars}); !errors.Is(err, ErrNewOff) {
		t.Fatalf("new buyers off: %v", err)
	}
	must(t, settings.Set(ctx, e.s.d.Settings, KeyConfig, Config{Enabled: true, Stars: true, AllowNew: true}))
	for i := range 5 {
		u, err := e.s.d.Users.Create(ctx, domain.CreateInput{Name: "x" + strconv.Itoa(i), TariffID: e.sale.ID})
		must(t, err)
		must(t, e.st.Q.LinkTg(ctx, db.LinkTgParams{UserID: u.ID, TgID: 900, CreatedAt: 1}))
	}
	if _, err := e.s.Invoice(ctx, InvoiceRequest{TgID: 900, TariffID: e.sale.ID, Provider: Stars}); err == nil {
		t.Fatal("a sixth subscription for one account")
	}
	// One account cannot open invoices without end.
	for tariff := range maxPerHour + 1 {
		_, err := e.st.Q.CreatePayment(ctx, db.CreatePaymentParams{Provider: Stars, Payload: "p" + strconv.Itoa(tariff), TgID: 321, Kind: "new",
			TariffID: sql.NullInt64{Int64: e.sale.ID, Valid: true}, TariffName: "x", Amount: 1, Currency: "XTR", CreatedAt: e.now.Unix()})
		must(t, err)
	}
	if _, err := e.s.Invoice(ctx, InvoiceRequest{TgID: 321, TariffID: e.sale.ID, Provider: Stars}); !errors.Is(err, ErrTooMany) {
		t.Fatalf("invoice flood: %v", err)
	}
}

// With the reset off a renewal only adds the term: the traffic counter runs on.
func TestRenewalKeepsTraffic(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	must(t, settings.Set(ctx, e.s.d.Settings, KeyConfig, Config{Enabled: true, Stars: true, AllowNew: true, RenewResetsTraffic: false}))
	clock := func() time.Time { return e.now }
	u, err := domain.NewUsers(e.st, domain.NewPool(e.st, clock), noChanges{}, clock).Create(ctx, domain.CreateInput{Name: "a", TariffID: e.sale.ID})
	must(t, err)
	must(t, e.st.Q.LinkTg(ctx, db.LinkTgParams{UserID: u.ID, TgID: 555, CreatedAt: e.now.Unix()}))
	_, err = e.st.DB.ExecContext(ctx, "UPDATE users SET used_up = 1000, used_down = 2000 WHERE id = ?", u.ID)
	must(t, err)
	p := e.invoice(555, u.ID, Stars)
	must(t, e.s.StarsPaid(ctx, 555, p.Payload, "ch-k", "XTR", 150))
	after, _ := e.st.Q.GetUser(ctx, u.ID)
	if after.ExpiresAt.Int64 != u.ExpiresAt.Int64+30*24*3600 || after.UsedUp+after.UsedDown != 3000 {
		t.Fatalf("renewed: expires %d, used %d", after.ExpiresAt.Int64, after.UsedUp+after.UsedDown)
	}
}

// Selling off: nothing on offer and no new invoices, but an invoice opened before still
// turns into the subscription once it is paid — nobody pays for nothing.
func TestSalesOff(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	p := e.invoice(555, 0, Stars)
	c := e.s.Config(ctx)
	c.Enabled = false
	must(t, settings.Set(ctx, e.s.d.Settings, KeyConfig, c))

	if o, av, err := e.s.Offers(ctx); err != nil || len(o) != 0 || av.Any() {
		t.Fatalf("offers with selling off: %v %+v %v", o, av, err)
	}
	for _, prov := range []string{Stars, "addon:yookassa"} {
		if _, err := e.s.Invoice(ctx, InvoiceRequest{TgID: 556, TariffID: e.sale.ID, Provider: prov}); !errors.Is(err, ErrProviderOff) {
			t.Fatalf("invoice %s with selling off: %v", prov, err)
		}
	}
	must(t, e.s.PreCheckout(ctx, 555, p.Payload, "XTR", 150))
	must(t, e.s.StarsPaid(ctx, 555, p.Payload, "ch-off", "XTR", 150))
	if got := e.payment(p.ID); got.Status != "applied" || !got.UserID.Valid {
		t.Fatalf("an invoice opened before selling went off: %+v", got)
	}
}

// Payment settings saved before the switch existed keep selling; a panel that never saved
// them starts with selling off.
func TestSalesDefault(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	_, err := e.st.DB.ExecContext(ctx, "DELETE FROM settings WHERE key = ?", KeyConfig)
	must(t, err)
	if e.s.Config(ctx).Enabled || e.s.Available(ctx).Any() {
		t.Fatal("selling on in a panel that never set it up")
	}
	must(t, settings.Set(ctx, e.s.d.Settings, KeyConfig, map[string]any{"stars": true, "allow_new": true}))
	if c := e.s.Config(ctx); !c.Enabled || !c.Stars {
		t.Fatalf("settings from 0.4.1 lost selling: %+v", c)
	}
}
