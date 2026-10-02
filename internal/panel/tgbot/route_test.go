package tgbot

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"mikan/internal/panel/settings"
)

func TestParseProxy(t *testing.T) {
	for raw, ok := range map[string]bool{
		"socks5://user:pass@203.0.113.5:1080": true,
		"http://203.0.113.5:3128":             true,
		"https://proxy.example.com:443/":      true,
		" socks5://[2001:db8::1]:1080 ":       true,
		"":                                    false,
		"203.0.113.5:1080":                    false,
		"socks4://203.0.113.5:1080":           false,
		"ftp://203.0.113.5:21":                false,
		"http://203.0.113.5":                  false,
		"http://203.0.113.5:0":                false,
		"http://203.0.113.5:70000":            false,
		"http://:8080":                        false,
		"http://203.0.113.5:8080/path":        false,
		"http://203.0.113.5:8080/?a=1":        false,
		"http://203.0.113.5:8080/#x":          false,
	} {
		if _, err := ParseProxy(raw); (err == nil) != ok {
			t.Errorf("%q: %v", raw, err)
		}
	}
	if got := MaskProxy("socks5://user:s3cret@203.0.113.5:1080"); got != "socks5://user:•••@203.0.113.5:1080" {
		t.Errorf("mask: %s", got)
	}
	if got := MaskProxy("http://203.0.113.5:3128"); got != "http://203.0.113.5:3128" {
		t.Errorf("mask without a password: %s", got)
	}
}

// forwardProxy is a plain HTTP proxy that counts what it forwards and remembers the
// credentials it was given.
func forwardProxy(t *testing.T) (*httptest.Server, *atomic.Int64, *atomic.Value) {
	var n atomic.Int64
	var auth atomic.Value
	auth.Store("")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		auth.Store(r.Header.Get("Proxy-Authorization"))
		out, err := http.NewRequestWithContext(r.Context(), r.Method, r.URL.String(), r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		out.Header = r.Header.Clone()
		out.Header.Del("Proxy-Authorization")
		resp, err := http.DefaultTransport.RoundTrip(out)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		for k, v := range resp.Header {
			w.Header()[k] = v
		}
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
	}))
	t.Cleanup(srv.Close)
	return srv, &n, &auth
}

// The bot reaches Telegram through the proxy, with its credentials.
func TestBotThroughProxy(t *testing.T) {
	proxy, n, auth := forwardProxy(t)
	setup(t, func(e *env, _ *Deps) {
		if err := settings.Set(e.ctx, e.set, KeyRoute, Route{Mode: RouteProxy, Proxy: "http://bot:s3cret@" + proxy.Listener.Addr().String()}); err != nil {
			t.Fatal(err)
		}
	})
	if n.Load() == 0 {
		t.Fatal("the bot went past the proxy")
	}
	if want := "Basic " + base64.StdEncoding.EncodeToString([]byte("bot:s3cret")); auth.Load() != want {
		t.Fatalf("proxy credentials: %q", auth.Load())
	}
}

// The bot reaches Telegram through a node: every connection goes through Tunnel.
func TestBotThroughNode(t *testing.T) {
	var n atomic.Int64
	setup(t, func(e *env, d *Deps) {
		if err := settings.Set(e.ctx, e.set, KeyRoute, Route{Mode: RouteNode, NodeID: 7}); err != nil {
			t.Fatal(err)
		}
		d.Tunnel = func(ctx context.Context, id int64, addr string) (net.Conn, error) {
			if id != 7 {
				return nil, errors.New("wrong node")
			}
			n.Add(1)
			return (&net.Dialer{}).DialContext(ctx, "tcp", addr)
		}
	})
	if n.Load() == 0 {
		t.Fatal("the bot went past the node")
	}
}

// A route that does not reach Telegram fails the check; a node route needs nodes.
func TestCheckRoute(t *testing.T) {
	e := setup(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	dead := ln.Addr().String()
	ln.Close()
	if err := e.bot.CheckRoute(e.ctx, Route{Mode: RouteProxy, Proxy: "socks5://" + dead}, "123:test"); err == nil {
		t.Fatal("a dead proxy passed the check")
	}
	if err := e.bot.CheckRoute(e.ctx, Route{Mode: RouteNode, NodeID: 3}, ""); !errors.Is(err, ErrRouteNode) {
		t.Fatalf("node route without nodes: %v", err)
	}
	proxy, _, _ := forwardProxy(t)
	if err := e.bot.CheckRoute(e.ctx, Route{Mode: RouteProxy, Proxy: "http://" + proxy.Listener.Addr().String()}, ""); err != nil {
		t.Fatalf("a working proxy without a token: %v", err)
	}
}
