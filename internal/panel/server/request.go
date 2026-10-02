package server

import (
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
)

// ClientIP is the address a request came from: the last X-Forwarded-For entry when the
// panel sits behind a proxy it trusts (the one the proxy appended), else the peer. An
// IPv4 address comes as IPv4 even when the socket is dual-stack (::ffff:a.b.c.d), so it
// reads the same to rate limits, to the nodes and to a provider's address list.
func ClientIP(h http.Header, remoteAddr string, trustProxy bool) string {
	if trustProxy {
		if xff := h.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			if ip, err := netip.ParseAddr(strings.TrimSpace(parts[len(parts)-1])); err == nil {
				return ip.Unmap().String()
			}
		}
	}
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		return ip.Unmap().String()
	}
	return host
}

// Site is where a browser says a request comes from.
type Site int

const (
	// SiteUnknown: no browser said anything (a script, curl, an app), or it was the user's
	// own navigation.
	SiteUnknown Site = iota
	SiteSame
	SiteCross
)

// FetchSite reads Sec-Fetch-Site, or Origin from a browser too old to send it. Callers
// decide what an unknown origin is worth: the API lets scripts through (they need the
// CSRF header anyway), the subscription's browser-only endpoints do not.
func FetchSite(h http.Header, host string) Site {
	switch h.Get("Sec-Fetch-Site") {
	case "same-origin":
		return SiteSame
	case "none":
		return SiteUnknown
	case "":
	default:
		return SiteCross
	}
	origin := h.Get("Origin")
	if origin == "" {
		return SiteUnknown
	}
	if u, err := url.Parse(origin); err == nil && u.Host != "" && u.Host == host {
		return SiteSame
	}
	return SiteCross
}
