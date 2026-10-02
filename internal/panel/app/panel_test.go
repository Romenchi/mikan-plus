package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/pquerna/otp/totp"

	"mikan/internal/panel/auth"
	"mikan/internal/panel/settings"
	"mikan/internal/panel/store"
	"mikan/internal/panel/store/db"
)

const (
	adminPath = "Kq7vN2xTg4mRz8pLw3YbC5dE"
	subPath   = "s9Hj2Kd8Lm4P"
	password  = "correct-horse-battery"
)

type harness struct {
	t      *testing.T
	st     *store.Store
	ts     *httptest.Server
	client *http.Client
	csrf   string
	now    time.Time
	p      *Panel
}

func newHarness(t *testing.T, with ...func(*Options)) *harness {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	hash, err := auth.HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Q.CreateAdmin(ctx, db.CreateAdminParams{Username: "admin", PasswordHash: hash, CreatedAt: time.Now().Unix()}); err != nil {
		t.Fatal(err)
	}
	set := settings.New(st.Q)
	for k, v := range map[string]string{settings.KeyAdminPath: adminPath, settings.KeySubPath: subPath} {
		if err := settings.Set(ctx, set, k, v); err != nil {
			t.Fatal(err)
		}
	}
	h := &harness{t: t, st: st, now: time.Now()}
	web := fstest.MapFS{
		"index.html":      {Data: []byte("<!doctype html><html><head><!-- mikan:base --></head><body></body></html>")},
		"assets/app-1.js": {Data: []byte("console.log(1)")},
		"sub.html":        {Data: []byte("<!doctype html><html><head><!-- mikan:base --></head><body>sub</body></html>")},
	}
	opts := Options{Version: "test", Web: web, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Now: func() time.Time { return h.now }, Resolve: testResolve}
	for _, f := range with {
		f(&opts)
	}
	p, err := NewPanel(st, opts)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Apply(ctx); err != nil {
		t.Fatal(err)
	}
	h.p = p
	h.ts = httptest.NewTLSServer(p.Handler)
	t.Cleanup(h.ts.Close)
	jar, _ := cookiejar.New(nil)
	h.client = h.ts.Client()
	h.client.Jar = jar
	h.client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return h
}

func (h *harness) do(method, path string, body any, hdr map[string]string) (*http.Response, []byte) {
	h.t.Helper()
	var rd io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		rd = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, h.ts.URL+path, rd)
	if err != nil {
		h.t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := h.client.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp, out
}

func (h *harness) login(pw, code string) (*http.Response, []byte) {
	body := map[string]string{"username": "admin", "password": pw}
	if code != "" {
		body["totp"] = code
	}
	resp, out := h.do(http.MethodPost, "/"+adminPath+"/api/v1/auth/login", body, nil)
	if resp.StatusCode == http.StatusOK {
		var me struct {
			CSRFToken string `json:"csrf_token"`
		}
		_ = json.Unmarshal(out, &me)
		h.csrf = me.CSRFToken
	}
	return resp, out
}

func TestUnknownPathsLookIdentical(t *testing.T) {
	h := newHarness(t)
	for _, p := range []string{"/", "/admin", "/login", "/api/v1/auth/me", "/" + adminPath + "x/", "/" + adminPath[:10] + "/", "//" + adminPath + "/", "/" + adminPath + "/../x"} {
		resp, body := h.do(http.MethodGet, p, nil, nil)
		if resp.StatusCode != http.StatusNotFound || string(body) != "404 Not Found\n" {
			t.Errorf("%s: got %d %q, want bare 404", p, resp.StatusCode, body)
		}
		if resp.Header.Get("Server") != "" || resp.Header.Get("X-Powered-By") != "" {
			t.Errorf("%s: leaks server identity", p)
		}
	}
}

func TestSecurityHeaders(t *testing.T) {
	h := newHarness(t)
	resp, _ := h.do(http.MethodGet, "/"+adminPath+"/", nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("admin root: %d", resp.StatusCode)
	}
	csp := resp.Header.Get("Content-Security-Policy")
	for _, want := range []string{"frame-ancestors 'none'", "script-src 'self'", "object-src 'none'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP %q lacks %q", csp, want)
		}
	}
	if got := resp.Header.Get("Referrer-Policy"); got != "no-referrer" {
		t.Errorf("Referrer-Policy = %q", got)
	}
}

func TestAdminPathServesSPAWithBase(t *testing.T) {
	h := newHarness(t)
	resp, _ := h.do(http.MethodGet, "/"+adminPath, nil, nil)
	if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != "/"+adminPath+"/" {
		t.Fatalf("no-slash: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	for _, p := range []string{"/", "/users/5"} {
		resp, body := h.do(http.MethodGet, "/"+adminPath+p, nil, nil)
		if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `<base href="/`+adminPath+`/">`) {
			t.Errorf("%s: %d %s", p, resp.StatusCode, body)
		}
	}
	resp, _ = h.do(http.MethodGet, "/"+adminPath+"/assets/app-1.js", nil, nil)
	if resp.StatusCode != http.StatusOK || !strings.Contains(resp.Header.Get("Cache-Control"), "immutable") {
		t.Errorf("asset: %d %s", resp.StatusCode, resp.Header.Get("Cache-Control"))
	}
	resp, _ = h.do(http.MethodGet, "/"+adminPath+"/assets/missing.js", nil, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("missing asset: %d", resp.StatusCode)
	}
}

// The language chosen at install opens the admin panel and the subscription page for
// visitors who have not picked one; without it the browser decides.
func TestDefaultLangReachesPages(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	pages := []string{"/" + adminPath + "/", "/" + subPath + "/tg"}
	check := func(want string) {
		t.Helper()
		for _, path := range pages {
			resp, body := h.do(http.MethodGet, path, nil, nil)
			if resp.StatusCode != http.StatusOK || strings.Contains(string(body), "mikan-lang") != (want != "") ||
				want != "" && !strings.Contains(string(body), `<meta name="mikan-lang" content="`+want+`">`) {
				t.Fatalf("%s with %q: %d %s", path, want, resp.StatusCode, body)
			}
		}
	}
	check("")
	set := settings.New(h.st.Q)
	for _, lang := range []string{"en", "auto"} {
		if err := settings.Set(ctx, set, settings.KeyDefaultLang, lang); err != nil {
			t.Fatal(err)
		}
		if _, err := h.p.Apply(ctx); err != nil {
			t.Fatal(err)
		}
		if lang == "auto" {
			lang = ""
		}
		check(lang)
	}
}

func TestLoginSessionAndLogout(t *testing.T) {
	h := newHarness(t)
	resp, _ := h.login(password, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login: %d", resp.StatusCode)
	}
	var ck *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == auth.CookieName {
			ck = c
		}
	}
	if ck == nil || !ck.Secure || !ck.HttpOnly || ck.SameSite != http.SameSiteStrictMode || ck.Path != "/" {
		t.Fatalf("session cookie flags: %+v", ck)
	}
	if h.csrf == "" {
		t.Fatal("no csrf token in login response")
	}
	if resp, _ := h.do(http.MethodGet, "/"+adminPath+"/api/v1/auth/me", nil, nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("me: %d", resp.StatusCode)
	}
	// AC-8 (б): мутирующий запрос без CSRF-токена.
	if resp, _ := h.do(http.MethodPost, "/"+adminPath+"/api/v1/auth/logout", nil, nil); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("logout without csrf: %d, want 403", resp.StatusCode)
	}
	if resp, _ := h.do(http.MethodPost, "/"+adminPath+"/api/v1/auth/logout", nil, map[string]string{"X-CSRF-Token": h.csrf}); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("logout: %d", resp.StatusCode)
	}
	if resp, _ := h.do(http.MethodGet, "/"+adminPath+"/api/v1/auth/me", nil, nil); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("me after logout: %d, want 401", resp.StatusCode)
	}
}

func TestCrossSiteLoginRejected(t *testing.T) {
	h := newHarness(t)
	resp, _ := h.do(http.MethodPost, "/"+adminPath+"/api/v1/auth/login", map[string]string{"username": "admin", "password": password}, map[string]string{"Sec-Fetch-Site": "cross-site"})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-site login: %d, want 403", resp.StatusCode)
	}
	resp, _ = h.do(http.MethodPost, "/"+adminPath+"/api/v1/auth/login", map[string]string{"username": "admin", "password": password}, map[string]string{"Origin": "https://evil.example"})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("foreign origin login: %d, want 403", resp.StatusCode)
	}
}

// AC-8 (а): после 10 неверных попыток с одного IP даже верный пароль получает 429.
func TestBruteForceBlocksIP(t *testing.T) {
	h := newHarness(t)
	for i := 0; i < 10; i++ {
		if resp, _ := h.login("wrong-password-"+string(rune('a'+i)), ""); resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("attempt %d: %d, want 401", i+1, resp.StatusCode)
		}
	}
	resp, _ := h.login(password, "")
	if resp.StatusCode != http.StatusTooManyRequests || resp.Header.Get("Retry-After") == "" {
		t.Fatalf("11th attempt: %d retry-after=%q, want 429", resp.StatusCode, resp.Header.Get("Retry-After"))
	}
	var n int
	if err := h.st.DB.QueryRow(`SELECT count(*) FROM audit_log WHERE action = 'auth.login_failed'`).Scan(&n); err != nil || n != 10 {
		t.Fatalf("audit failed logins = %d (%v), want 10", n, err)
	}
	h.now = h.now.Add(16 * time.Minute)
	if resp, _ := h.login(password, ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("after block expiry: %d", resp.StatusCode)
	}
}

// AC-8 (в): протухшая по простою сессия.
func TestIdleSessionExpires(t *testing.T) {
	h := newHarness(t)
	if resp, _ := h.login(password, ""); resp.StatusCode != http.StatusOK {
		t.Fatal("login failed")
	}
	h.now = h.now.Add(auth.IdleTTL + time.Minute)
	if resp, _ := h.do(http.MethodGet, "/"+adminPath+"/api/v1/auth/me", nil, nil); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("idle session: %d, want 401", resp.StatusCode)
	}
}

// AC-9: вход с 2FA и одноразовые резервные коды.
func TestTOTPLogin(t *testing.T) {
	h := newHarness(t)
	key, err := auth.NewTOTPKey("admin")
	if err != nil {
		t.Fatal(err)
	}
	plain, stored := auth.NewRecoveryCodes(2)
	if err := h.st.Q.SetAdminTOTP(context.Background(), db.SetAdminTOTPParams{
		TotpSecret: sql.NullString{String: key.Secret(), Valid: true}, RecoveryCodes: sql.NullString{String: stored, Valid: true}, ID: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if resp, body := h.login(password, ""); resp.StatusCode != http.StatusUnauthorized || !strings.Contains(string(body), "totp_required") {
		t.Fatalf("no code: %d %s", resp.StatusCode, body)
	}
	if resp, body := h.login(password, "000000"); resp.StatusCode != http.StatusUnauthorized || !strings.Contains(string(body), "invalid_totp") {
		t.Fatalf("wrong code: %d %s", resp.StatusCode, body)
	}
	code, err := totp.GenerateCode(key.Secret(), h.now)
	if err != nil {
		t.Fatal(err)
	}
	if resp, _ := h.login(password, code); resp.StatusCode != http.StatusOK {
		t.Fatalf("valid code: %d", resp.StatusCode)
	}
	if resp, _ := h.login(password, code); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("replayed code: %d, want 401", resp.StatusCode)
	}
	if resp, _ := h.login(password, plain[0]); resp.StatusCode != http.StatusOK {
		t.Fatalf("recovery code: %d", resp.StatusCode)
	}
	if resp, _ := h.login(password, plain[0]); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("reused recovery code: %d, want 401", resp.StatusCode)
	}
}
