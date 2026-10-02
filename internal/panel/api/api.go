package api

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"mikan/internal/nodeapi"
	"mikan/internal/nodetls"
	"mikan/internal/panel/acme"
	"mikan/internal/panel/audit"
	"mikan/internal/panel/auth"
	"mikan/internal/panel/autotune"
	"mikan/internal/panel/billing"
	"mikan/internal/panel/domain"
	"mikan/internal/panel/nodesync"
	"mikan/internal/panel/secure"
	"mikan/internal/panel/settings"
	"mikan/internal/panel/store"
	"mikan/internal/panel/store/db"
	"mikan/internal/panel/tgbot"
	"mikan/internal/panel/updates"
	"mikan/internal/panel/warp"
)

type Deps struct {
	Version    string
	Store      *store.Store
	Settings   *settings.Settings
	Sessions   *auth.Sessions
	IPLimit    *auth.Limiter
	UserLimit  *auth.Limiter
	TOTP       *auth.TOTPGuard
	TrustProxy bool
	Log        *slog.Logger
	Now        func() time.Time

	Users     *domain.Users
	Devices   *domain.Devices
	Pool      *domain.Pool
	Changes   domain.Changes
	SubURL    func(ctx context.Context, token string) string
	Online    func() map[string]nodeapi.Online
	Cert      func() acme.Status
	RenewCert func()
	// Nodes is the live side of the nodes; nil when the panel runs without them.
	Nodes NodeRuntime
	// PanelCert is the client certificate remote nodes pin; their join keys carry its hash.
	PanelCert func() (nodetls.Pair, error)
	// Tuner reports what the automatic moves see; nil when the panel runs without nodes.
	Tuner interface {
		Status(inboundID int64) (autotune.Status, bool)
	}
	// Telegram is the subscription owners' bot.
	Telegram *tgbot.Bot
	// Billing sells tariffs; SubBase is https://host:port/<sub path> ("" without an address).
	Billing *billing.Service
	// Warp registers WARP accounts with Cloudflare.
	Warp    warp.Client
	SubBase func(ctx context.Context) string
	// Updates knows the newest release and talks to the host updater; nil in tests.
	Updates *updates.Checker
}

// NodeRuntime is what the API needs from the running nodes.
type NodeRuntime interface {
	Health(id int64) (nodesync.HealthView, bool)
	// Validate runs mihomo's parser on an inbound on the node that will run it.
	Validate(ctx context.Context, id int64, req nodeapi.ValidateRequest) error
	// Retire makes a node drop its listeners and users before it is removed.
	Retire(ctx context.Context, id int64) error
	NodesChanged()
	// ScanTargets looks for REALITY targets from the node itself (its RTTs, its routes).
	ScanTargets(ctx context.Context, id int64, req nodeapi.TargetScanRequest) (nodeapi.TargetScan, error)
	// Warp checks the node's way out through WARP.
	Warp(ctx context.Context, id int64) (nodeapi.WarpStatus, error)
}

type ctxKey int

const (
	keyClient ctxKey = iota
	keySession
)

type client struct{ IP, UserAgent string }

type handlers struct {
	d         Deps
	api       huma.API
	dummyHash string

	pendingMu sync.Mutex
	pending   map[int64]pendingTOTP
}

// Config builds the huma config shared by the server and the `mikan openapi` command.
func Config(version string) huma.Config {
	// Handlers always return non-nil slices; nullable arrays would force null checks in the UI.
	huma.DefaultArrayNullable = false
	cfg := huma.DefaultConfig("mikan", version)
	// No docs UI, no spec endpoint and no $schema links at runtime: the spec is
	// exported by the CLI at build time for the TypeScript client.
	cfg.DocsPath = ""
	cfg.OpenAPIPath = ""
	cfg.SchemasPath = ""
	cfg.CreateHooks = nil
	cfg.Info.Description = "REST API панели mikan. Все пути — под секретным адресом админки: https://<панель>/<секретный путь>/api/v1/…\n\n" +
		"Скрипты и интеграции авторизуются ключом API (Настройки → Ключи API): заголовок `Authorization: Bearer mk_…`. " +
		"Ключ «чтение» выполняет только GET, «полный» — всё, кроме входа, сессий и самих ключей.\n\n" +
		"Админка в браузере ходит с cookie сессии; изменяющие запросы тогда требуют заголовок `X-CSRF-Token` из `GET /auth/me`.\n\n" +
		"Ошибки — RFC 9457 (application/problem+json): `detail` — код ошибки, `errors[].message` — код по полю."
	cfg.Components.SecuritySchemes = map[string]*huma.SecurityScheme{
		"apiKey":  {Type: "http", Scheme: "bearer", Description: "Ключ API: Authorization: Bearer mk_…"},
		"session": {Type: "apiKey", In: "cookie", Name: auth.CookieName, Description: "Сессия админки + заголовок X-CSRF-Token на изменяющих запросах"},
	}
	cfg.Security = []map[string][]string{{"apiKey": {}}, {"session": {}}}
	return cfg
}

var hideInternalOnce sync.Once

// hideInternal keeps the text of unexpected errors (SQL constraints, file paths) out of
// responses: huma would put it into the 500 body. It is logged instead.
func hideInternal(log *slog.Logger) {
	hideInternalOnce.Do(func() {
		base := huma.NewError
		huma.NewError = func(status int, msg string, errs ...error) huma.StatusError {
			if status >= 500 && len(errs) > 0 {
				if log != nil {
					log.Error("api internal error", "status", status, "err", errors.Join(errs...))
				}
				return base(status, msg)
			}
			return base(status, msg, errs...)
		}
	})
}

func New(d Deps) (http.Handler, huma.API, error) {
	hideInternal(d.Log)
	mux := http.NewServeMux()
	api := humago.New(mux, Config(d.Version))
	dummy, err := auth.HashPassword(secure.Token(32))
	if err != nil {
		return nil, nil, err
	}
	h := &handlers{d: d, api: api, dummyHash: dummy, pending: map[int64]pendingTOTP{}}
	api.UseMiddleware(h.middleware)
	h.registerAuth()
	h.registerUsers()
	h.registerCatalog()
	h.registerInbounds()
	h.registerTargets()
	h.registerStats()
	h.registerSettings()
	h.registerTelegram()
	h.registerUpdates()
	h.registerNodes()
	h.registerAPIKeys()
	h.registerPayments()
	h.registerWarp()
	h.registerRelay()
	return noStore(mux), api, nil
}

func noStore(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

func (h *handlers) middleware(ctx huma.Context, next func(huma.Context)) {
	ctx = huma.WithValue(ctx, keyClient, client{IP: h.clientIP(ctx), UserAgent: ctx.Header("User-Agent")})
	op := ctx.Operation()
	mutating := op.Method != http.MethodGet && op.Method != http.MethodHead
	if mutating && !sameOrigin(ctx) {
		_ = huma.WriteErr(h.api, ctx, http.StatusForbidden, "csrf")
		return
	}
	if public, _ := op.Metadata["public"].(bool); public {
		next(ctx)
		return
	}
	if ctx.Header("Authorization") != "" {
		h.bearer(ctx, next, mutating)
		return
	}
	ck, err := huma.ReadCookie(ctx, auth.CookieName)
	if err != nil {
		_ = huma.WriteErr(h.api, ctx, http.StatusUnauthorized, "unauthorized")
		return
	}
	sess, err := h.d.Sessions.Lookup(ctx.Context(), ck.Value)
	if err != nil {
		if err != auth.ErrNoSession {
			h.d.Log.Error("session lookup", "err", err)
			_ = huma.WriteErr(h.api, ctx, http.StatusInternalServerError, "internal error")
			return
		}
		_ = huma.WriteErr(h.api, ctx, http.StatusUnauthorized, "unauthorized")
		return
	}
	if mutating && !secure.Equal(ctx.Header("X-CSRF-Token"), sess.CsrfToken) {
		_ = huma.WriteErr(h.api, ctx, http.StatusForbidden, "csrf")
		return
	}
	next(huma.WithValue(ctx, keySession, sess))
}

// sameOrigin rejects cross-site browser requests. Non-browser clients send neither
// Sec-Fetch-Site nor Origin; they still need the CSRF header for authenticated calls.
func sameOrigin(ctx huma.Context) bool {
	switch ctx.Header("Sec-Fetch-Site") {
	case "same-origin", "none":
		return true
	case "":
	default:
		return false
	}
	origin := ctx.Header("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	return err == nil && u.Host == ctx.Host()
}

func (h *handlers) clientIP(ctx huma.Context) string {
	if h.d.TrustProxy {
		if xff := ctx.Header("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			if ip := strings.TrimSpace(parts[len(parts)-1]); net.ParseIP(ip) != nil {
				return ip
			}
		}
	}
	host, _, err := net.SplitHostPort(ctx.RemoteAddr())
	if err != nil {
		return ctx.RemoteAddr()
	}
	return host
}

func clientOf(ctx context.Context) client {
	c, _ := ctx.Value(keyClient).(client)
	return c
}

func sessionOf(ctx context.Context) db.Session {
	s, _ := ctx.Value(keySession).(db.Session)
	return s
}

func (h *handlers) audit(ctx context.Context, adminID int64, action, targetType, targetID string, details any) {
	// What an API key did says which key: the admin may hand keys to several scripts.
	if k, ok := apiKeyOf(ctx); ok {
		m := map[string]any{"api_key": k.Prefix}
		if d, isMap := details.(map[string]any); isMap {
			for key, v := range d {
				m[key] = v
			}
		} else if details != nil {
			m["details"] = details
		}
		details = m
	}
	err := audit.Write(ctx, h.d.Store.Q, h.d.Now(), audit.Entry{
		AdminID: adminID, Action: action, TargetType: targetType, TargetID: targetID,
		IP: clientOf(ctx).IP, Details: details,
	})
	if err != nil {
		h.d.Log.Warn("audit write failed", "action", action, "err", err)
	}
}
