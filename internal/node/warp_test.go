package node

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/metacubex/mihomo/hub/executor"

	"mikan/internal/nodeapi"
	"mikan/internal/proto"
)

func warpState() nodeapi.DesiredState {
	tpl, _ := proto.Parse("type: anytls\n")
	return nodeapi.DesiredState{
		Inbounds: []nodeapi.Inbound{{Name: "in-anytls", Port: "2083", Config: tpl.JSON()}},
		Warp: &nodeapi.Warp{
			PrivateKey: "yAnz5TF+lXXJte14tji3zlMNq+hd2rYUIgJBgB3fBmk=", PeerPublicKey: "bmXOC+F1FxEMF9dyiK2H5/1SUtzH0JuVo51h2wPfgyo=",
			Endpoint: "162.159.192.1:2408", IPv4: "172.16.0.2", IPv6: "2606:4700:110:8a36::2", Reserved: []uint8{1, 2, 3},
			Inbounds: []string{"in-anytls", "in-gone", "bad,name"},
			Domains:  []string{"openai.com", "Example.ORG", "evil.com,DIRECT\nMATCH"},
			CIDRs:    []string{"104.16.0.0/13", "2606:4700::/32", "not-a-net"},
		},
	}
}

// WARP rules come after the REJECT ones, name only inbounds that exist and never let a
// value split a rule; mihomo's own parser takes the result.
func TestWarpConfig(t *testing.T) {
	st := warpState()
	raw, _, err := buildConfig(st, proto.Cert{CertPath: "/tmp/c.pem", KeyPath: "/tmp/k.pem"}, false)
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Proxies []map[string]any `json:"proxies"`
		Rules   []string         `json:"rules"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"IN-NAME,in-anytls,WARP",
		"DOMAIN-SUFFIX,openai.com,WARP",
		"DOMAIN-SUFFIX,example.org,WARP",
		"IP-CIDR,104.16.0.0/13,WARP,no-resolve",
		"IP-CIDR6,2606:4700::/32,WARP,no-resolve",
	}
	got := strings.Join(cfg.Rules, "\n")
	if !strings.Contains(got, strings.Join(append([]string{"DST-PORT,25,REJECT"}, want...), "\n")+"\nMATCH,DIRECT") {
		t.Fatalf("rules:\n%s", got)
	}
	if strings.Index(got, "IP-CIDR,127.0.0.0/8,REJECT") > strings.Index(got, ",WARP") {
		t.Fatal("WARP must come after the REJECT rules")
	}
	for _, bad := range []string{"in-gone", "bad", "evil.com", "not-a-net"} {
		if strings.Contains(got, bad) {
			t.Fatalf("%q leaked into the rules:\n%s", bad, got)
		}
	}
	if len(cfg.Proxies) != 1 || cfg.Proxies[0]["type"] != "wireguard" || cfg.Proxies[0]["remote-dns-resolve"] != true {
		t.Fatalf("proxies: %v", cfg.Proxies)
	}
	parsed, err := executor.ParseWithBytes(raw)
	if err != nil {
		t.Fatalf("mihomo refuses the config: %v", err)
	}
	if _, ok := parsed.Proxies[warpProxy]; !ok || len(parsed.Rules) != len(cfg.Rules) {
		t.Fatalf("parsed: %d rules, proxies %v", len(parsed.Rules), parsed.Proxies)
	}

	// Without WARP nothing of it remains; a broken endpoint is refused.
	st.Warp = nil
	raw, _, _ = buildConfig(st, proto.Cert{}, false)
	if strings.Contains(string(raw), "WARP") {
		t.Fatalf("WARP without WARP: %s", raw)
	}
	st = warpState()
	st.Warp.Endpoint = "nowhere"
	if _, _, err := buildConfig(st, proto.Cert{}, false); err == nil {
		t.Fatal("a bad endpoint was accepted")
	}
	if routesKey(warpState(), false) == routesKey(st, false) || routesKey(warpState(), false) != routesKey(warpState(), false) {
		t.Fatal("routesKey must follow the WARP settings")
	}
}
