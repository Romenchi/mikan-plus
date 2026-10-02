package server

import (
	"net/http"
	"testing"
)

func TestClientIP(t *testing.T) {
	for _, c := range []struct {
		xff, remote string
		trust       bool
		want        string
	}{
		{"", "198.51.100.7:5555", false, "198.51.100.7"},
		{"", "[::ffff:198.51.100.7]:5555", false, "198.51.100.7"},
		{"", "[2001:db8::1]:443", false, "2001:db8::1"},
		{"10.0.0.1, 203.0.113.5", "127.0.0.1:1", true, "203.0.113.5"},
		{"203.0.113.5", "127.0.0.1:1", false, "127.0.0.1"},       // not trusted: the peer
		{"1.2.3.4, not-an-ip", "127.0.0.1:1", true, "127.0.0.1"}, // garbage: the peer
		{"", "unix-socket", false, "unix-socket"},
	} {
		h := http.Header{}
		if c.xff != "" {
			h.Set("X-Forwarded-For", c.xff)
		}
		if got := ClientIP(h, c.remote, c.trust); got != c.want {
			t.Errorf("%+v: %q", c, got)
		}
	}
}

func TestFetchSite(t *testing.T) {
	for _, c := range []struct {
		site, origin string
		want         Site
	}{
		{"same-origin", "", SiteSame},
		{"cross-site", "", SiteCross},
		{"same-site", "", SiteCross},
		{"none", "", SiteUnknown},
		{"", "", SiteUnknown},
		{"", "https://panel.example", SiteSame},
		{"", "https://evil.example", SiteCross},
		{"", "null", SiteCross},
	} {
		h := http.Header{}
		if c.site != "" {
			h.Set("Sec-Fetch-Site", c.site)
		}
		if c.origin != "" {
			h.Set("Origin", c.origin)
		}
		if got := FetchSite(h, "panel.example"); got != c.want {
			t.Errorf("%+v: %v", c, got)
		}
	}
}
