package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"mikan/internal/nodeapi"
	"mikan/internal/panel/auth"
	"mikan/internal/panel/domain"
	"mikan/internal/panel/store"
	"mikan/internal/panel/store/db"
)

// What every user of an answer shares (the subscription address, who is online) is worked
// out once for the answer, not once per user: a page of 500 users asked for the address
// (six settings reads) and the whole online map 500 times each.
func TestUserListReadsSharedStateOnce(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Now()
	clock := func() time.Time { return now }
	if err := domain.Seed(ctx, st, now); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Q.CreateAdmin(ctx, db.CreateAdminParams{Username: "admin", PasswordHash: "x", CreatedAt: now.Unix()}); err != nil {
		t.Fatal(err)
	}
	pool := domain.NewPool(st, clock)
	users := domain.NewUsers(st, pool, noChanges{}, clock)
	tariffs, _ := st.Q.ListTariffs(ctx)
	var first int64
	for _, name := range []string{"a", "b", "c", "d", "e"} {
		u, err := users.Create(ctx, domain.CreateInput{Name: name, TariffID: tariffs[1].ID})
		if err != nil {
			t.Fatal(err)
		}
		if first == 0 {
			first = u.ID
		}
	}
	sessions := auth.NewSessions(st.Q, clock, nil)
	token, _, err := sessions.Create(ctx, 1, "127.0.0.1", "test")
	if err != nil {
		t.Fatal(err)
	}
	var subBase, online int
	handler, _, err := New(Deps{
		Version: "test", Store: st, Sessions: sessions, Now: clock, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		IPLimit: auth.NewLimiter(10, time.Minute, time.Minute, time.Hour), UserLimit: auth.NewLimiter(10, time.Minute, time.Minute, time.Hour), TOTP: auth.NewTOTPGuard(),
		Users: users, Pool: pool, Changes: noChanges{},
		SubBase: func(context.Context) string { subBase++; return "https://203.0.113.10:21355/s" },
		Online:  func() map[string]nodeapi.Online { online++; return map[string]nodeapi.Online{} },
	})
	if err != nil {
		t.Fatal(err)
	}
	get := func(path string) []byte {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: token})
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", path, rec.Code, rec.Body)
		}
		return rec.Body.Bytes()
	}

	body := get("/api/v1/users")
	var list struct {
		Items []struct {
			SubURL string `json:"sub_url"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &list); err != nil || len(list.Items) != 5 {
		t.Fatalf("list: %v %s", err, body)
	}
	for _, it := range list.Items {
		if !strings.HasPrefix(it.SubURL, "https://203.0.113.10:21355/s/") {
			t.Fatalf("sub_url %q", it.SubURL)
		}
	}
	if subBase != 1 || online != 1 {
		t.Fatalf("a list of 5 users read the subscription address %d times and the online map %d times, want once each", subBase, online)
	}
	subBase, online = 0, 0
	get("/api/v1/users/" + strconv.FormatInt(first, 10))
	get("/api/v1/users/" + strconv.FormatInt(first, 10) + "/devices")
	if subBase != 1 || online != 2 {
		t.Fatalf("one user: address %d times, online map %d times, want 1 and 2 (user, devices)", subBase, online)
	}
}
