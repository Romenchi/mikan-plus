package acme

import "testing"

// Let's Encrypt refuses a CSR with an IP in the Common Name (badCSR) and issues IP
// certificates only with the shortlived profile; domains stay as they were.
func TestOrder(t *testing.T) {
	for id, want := range map[string]struct {
		profile string
		noCN    bool
	}{
		"77.238.243.225":  {"shortlived", true},
		"2001:db8::1":     {"shortlived", true},
		"vpn.example.com": {"", false},
	} {
		req, noCN := order(id)
		if req.Profile != want.profile || noCN != want.noCN || len(req.Domains) != 1 || req.Domains[0] != id || !req.Bundle {
			t.Errorf("%s: %+v, no CN %v", id, req, noCN)
		}
	}
}
