package warp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const peerKey = "bmXOC+F1FxEMF9dyiK2H5/1SUtzH0JuVo51h2wPfgyo="

func fakeCloudflare(t *testing.T, plus *bool) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("CF-Client-Version") == "" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		reg := func() map[string]any {
			return map[string]any{"id": "dev-1", "token": "tok-1", "account": map[string]any{"warp_plus": *plus},
				"config": map[string]any{"client_id": base64.StdEncoding.EncodeToString([]byte{7, 8, 9}),
					"peers":     []any{map[string]any{"public_key": peerKey, "endpoint": map[string]any{"v4": "162.159.192.5:0", "host": "engage.cloudflareclient.com:2408"}}},
					"interface": map[string]any{"addresses": map[string]any{"v4": "172.16.0.2", "v6": "2606:4700:110:8a36::2"}}}}
		}
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/reg":
			var in map[string]string
			_ = json.NewDecoder(r.Body).Decode(&in)
			if k, err := base64.StdEncoding.DecodeString(in["key"]); err != nil || len(k) != 32 {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			_ = json.NewEncoder(w).Encode(reg())
		case r.URL.Path == "/reg/dev-1/account" && r.Method == http.MethodPut:
			if r.Header.Get("Authorization") != "Bearer tok-1" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			*plus = true
			_, _ = w.Write([]byte(`{}`))
		case r.URL.Path == "/reg/dev-1" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(reg())
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestRegister(t *testing.T) {
	plus := false
	c := Client{API: fakeCloudflare(t, &plus).URL}
	a, err := c.Register(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if a.Endpoint != "162.159.192.5:2408" || a.IPv4 != "172.16.0.2" || a.PeerPublicKey != peerKey || string(a.Reserved) != "\x07\x08\x09" || a.ID != "dev-1" || a.Plus {
		t.Fatalf("account: %+v", a)
	}
	if k, _ := base64.StdEncoding.DecodeString(a.PrivateKey); len(k) != 32 {
		t.Fatal("private key")
	}
	if _, err := c.SetLicense(context.Background(), a.ID, a.Token, "not-a-key"); code(err) != "warp_license_format" {
		t.Fatalf("license format: %v", err)
	}
	ok, err := c.SetLicense(context.Background(), a.ID, a.Token, "aB3dE5fG-hI7jK9lM-nO1pQ3rS")
	if err != nil || !ok {
		t.Fatalf("license: %v %v", ok, err)
	}
	var refused *Error
	if _, err := c.SetLicense(context.Background(), a.ID, "wrong", "aB3dE5fG-hI7jK9lM-nO1pQ3rS"); !errors.As(err, &refused) || refused.Code != "warp_refused" || refused.Status != 401 {
		t.Fatalf("someone else's registration: %v", err)
	}
	if _, err := (Client{API: "http://127.0.0.1:1"}).Register(context.Background(), ""); code(err) != "warp_unreachable" {
		t.Fatalf("unreachable: %v", err)
	}
}

func code(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

func TestParseConf(t *testing.T) {
	wgcf := `[Interface]
PrivateKey = yAnz5TF+lXXJte14tji3zlMNq+hd2rYUIgJBgB3fBmk=
Address = 172.16.0.2/32, 2606:4700:110:8a36:df92:102a:9602:fa18/128
DNS = 1.1.1.1
MTU = 1280
# Reserved from the app
Reserved = 1, 2, 3
[Peer]
PublicKey = ` + peerKey + `
AllowedIPs = 0.0.0.0/0, ::/0
Endpoint = engage.cloudflareclient.com:2408
`
	a, err := ParseConf(wgcf)
	if err != nil {
		t.Fatal(err)
	}
	if a.IPv4 != "172.16.0.2" || a.IPv6 != "2606:4700:110:8a36:df92:102a:9602:fa18" || a.Endpoint != "engage.cloudflareclient.com:2408" || len(a.Reserved) != 3 || a.MTU != 1280 {
		t.Fatalf("parsed: %+v", a)
	}
	for name, src := range map[string]string{
		"no peer key":       strings.Replace(wgcf, "PublicKey = "+peerKey, "", 1),
		"short key":         strings.Replace(wgcf, peerKey, "abc=", 1),
		"bad reserved":      strings.Replace(wgcf, "1, 2, 3", "1, 2, 300", 1),
		"endpoint with ,":   strings.Replace(wgcf, "engage.cloudflareclient.com:2408", "a.com,DIRECT:2408", 1),
		"address not an IP": strings.Replace(wgcf, "172.16.0.2/32", "nope", 1),
		"tiny mtu":          strings.Replace(wgcf, "MTU = 1280", "MTU = 100", 1),
	} {
		if _, err := ParseConf(src); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestParseRoutes(t *testing.T) {
	r, bad := ParseRoutes([]string{"openai.com\n*.Netflix.com, .example.org", "104.16.0.0/13 1.1.1.1", "2606:4700::/32", "evil.com,DIRECT", "bad_name", "-x.com"})
	want := []string{"openai.com", "netflix.com", "example.org", "evil.com"}
	if strings.Join(r.Domains, " ") != strings.Join(want, " ") {
		t.Fatalf("domains: %v", r.Domains)
	}
	if strings.Join(r.CIDRs, " ") != "104.16.0.0/13 1.1.1.1/32 2606:4700::/32" {
		t.Fatalf("networks: %v", r.CIDRs)
	}
	if strings.Join(bad, " ") != "direct bad_name -x.com" {
		t.Fatalf("bad: %v", bad)
	}
}
