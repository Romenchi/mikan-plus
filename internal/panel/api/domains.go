package api

import (
	"context"
	"errors"
	"net/netip"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"mikan/internal/panel/dnscheck"
)

// A domain of the panel or of a node is taken only when public DNS sends clients to that
// machine: otherwise anyone could write someone else's domain into every subscription.

// domainHere checks that domain points only to own; nil when it does (or when the panel
// runs without the check, in tests). field is where the error goes.
func (h *handlers) domainHere(ctx context.Context, field, domain string, own []netip.Addr) *huma.ErrorDetail {
	if h.d.DNS == nil || domain == "" {
		return nil
	}
	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	err := h.d.DNS.PointsTo(cctx, domain, own)
	var e *dnscheck.Elsewhere
	switch {
	case err == nil:
		return nil
	case errors.As(err, &e):
		return &huma.ErrorDetail{Location: field, Message: "domain_elsewhere", Value: dnscheck.Join(e.Foreign) + " → " + dnscheck.Join(own)}
	case errors.Is(err, dnscheck.ErrNotFound):
		return &huma.ErrorDetail{Location: field, Message: "domain_not_found"}
	case errors.Is(err, dnscheck.ErrUnknownServer):
		return &huma.ErrorDetail{Location: field, Message: "server_ip_unknown"}
	}
	h.d.Log.Warn("domain check", "domain", domain, "err", err)
	return &huma.ErrorDetail{Location: field, Message: "domain_unchecked"}
}

// nodeAddrs is where a node is: its host when it is an IP, else what the host resolves to.
func (h *handlers) nodeAddrs(ctx context.Context, host string) []netip.Addr {
	if ip, err := netip.ParseAddr(strings.Trim(host, "[]")); err == nil {
		return []netip.Addr{ip.Unmap()}
	}
	if h.d.DNS == nil {
		return nil
	}
	as, _ := h.d.DNS.Lookup(ctx, host)
	return as
}
