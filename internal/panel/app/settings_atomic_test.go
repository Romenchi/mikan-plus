package app

import (
	"context"
	"net/http"
	"testing"

	"mikan/internal/panel/settings"
)

// The settings of one request are written together: when one cannot be written, the
// others of the request are not left behind.
func TestSettingsOfOneRequestAreWrittenTogether(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	if resp, _ := h.login(password, ""); resp.StatusCode != http.StatusOK {
		t.Fatal("login")
	}
	csrf := map[string]string{"X-CSRF-Token": h.csrf}
	api := "/" + adminPath + "/api/v1"
	set := settings.New(h.st.Q)
	if err := settings.Set(ctx, set, settings.KeyBrand, "Before"); err != nil {
		t.Fatal(err)
	}
	// One of the keys refuses to be written (a full disk or a busy database would do the same).
	if _, err := h.st.DB.ExecContext(ctx, "CREATE TRIGGER refuse_lang BEFORE INSERT ON settings WHEN NEW.key = '"+settings.KeyDefaultLang+"' BEGIN SELECT RAISE(ABORT, 'refused'); END"); err != nil {
		t.Fatal(err)
	}
	for range 5 { // the order the keys are written in is random
		resp, _ := h.do(http.MethodPatch, api+"/settings", map[string]any{"brand": "After", "quiet_hour_utc": 5, "default_lang": "en", "auto_port": false}, csrf)
		if resp.StatusCode != http.StatusInternalServerError {
			t.Fatalf("a request with a setting that cannot be written: %d", resp.StatusCode)
		}
		if brand, _, _ := settings.Get[string](ctx, set, settings.KeyBrand); brand != "Before" {
			t.Fatalf("brand is %q: the settings written before the failure stayed", brand)
		}
		if hour, ok, _ := settings.Get[int](ctx, set, settings.KeyQuietHour); ok && hour == 5 {
			t.Fatal("the quiet hour of the failed request was kept")
		}
	}
	if _, err := h.st.DB.ExecContext(ctx, "DROP TRIGGER refuse_lang"); err != nil {
		t.Fatal(err)
	}
	if resp, body := h.do(http.MethodPatch, api+"/settings", map[string]any{"brand": "After", "default_lang": "en"}, csrf); resp.StatusCode != http.StatusOK {
		t.Fatalf("the same request once it can be written: %d %s", resp.StatusCode, body)
	}
	if brand, _, _ := settings.Get[string](ctx, set, settings.KeyBrand); brand != "After" {
		t.Fatalf("brand %q", brand)
	}
}
