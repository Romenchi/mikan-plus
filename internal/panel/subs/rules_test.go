package subs

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

var testGroups = Groups{Main: "Мой VPN", Auto: "Быстрый"}

func TestParseRules(t *testing.T) {
	text := strings.Join([]string{
		"# banks go direct",
		"domain-suffix , sber.ru , direct",
		"",
		"DOMAIN,api.example.com,Мой VPN",
		"DOMAIN-KEYWORD,youtube,PROXY",
		"DOMAIN-WILDCARD,*.cdn.example.com,Быстрый",
		`DOMAIN-REGEX,^ads\d+\.example\.com$,REJECT`,
		"GEOSITE,category-ads-all,REJECT-DROP",
		"GEOIP,telegram,PROXY,no-resolve",
		"IP-CIDR,10.0.0.0/8,DIRECT,no-resolve",
		"IP-CIDR6,2001:db8::/32,DIRECT",
		"IP-ASN,13335,PROXY",
		"DST-PORT,6881-6889/51413,REJECT",
		"NETWORK,udp,PROXY",
		"PROCESS-NAME,Telegram.exe,PROXY",
		"PROCESS-PATH,/usr/bin/curl,DIRECT",
	}, "\r\n")
	rules, err := ParseRules(text, testGroups)
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) != 14 || rules[0] != "DOMAIN-SUFFIX,sber.ru,DIRECT" || rules[6] != "GEOIP,telegram,PROXY,no-resolve" {
		t.Fatalf("rules: %q", rules)
	}

	for line, code := range map[string]string{
		"DOMAIN,example.com":                             "rule_format",
		"DOMAIN,example.com,DIRECT,no-resolve,x":         "rule_format",
		"RULE-SET,ads,REJECT":                            "rule_type",
		"AND,((DOMAIN,a.com)),DIRECT":                    "rule_type",
		"MATCH,DIRECT":                                   "rule_format",
		"DOMAIN,exa mple.com,DIRECT":                     "rule_value",
		"DOMAIN,a..com,DIRECT":                           "rule_value",
		"DOMAIN-REGEX,(unclosed,DIRECT":                  "rule_value",
		"IP-CIDR,10.0.0.0/33,DIRECT":                     "rule_value",
		"IP-CIDR6,10.0.0.0/8,DIRECT":                     "rule_value",
		"IP-ASN,AS13335,DIRECT":                          "rule_value",
		"DST-PORT,0,DIRECT":                              "rule_value",
		"DST-PORT,9000-80,DIRECT":                        "rule_value",
		"NETWORK,icmp,DIRECT":                            "rule_value",
		"GEOSITE,youtube ru,DIRECT":                      "rule_value",
		`PROCESS-NAME,"evil",DIRECT`:                     "rule_value",
		"DOMAIN,example.com,Other":                       "rule_target",
		"DOMAIN,example.com,мой vpn":                     "rule_target",
		"DOMAIN,example.com,DIRECT,no-resolve":           "rule_option",
		"IP-CIDR,10.0.0.0/8,DIRECT,src":                  "rule_option",
		"DOMAIN," + strings.Repeat("a", 600) + ",DIRECT": "rule_too_long",
	} {
		_, err := ParseRules("# first\n"+line, testGroups)
		var re *RuleError
		if !errors.As(err, &re) || re.Code != code || re.Line != 2 {
			t.Errorf("%q: %v, want %s on line 2", line, err, code)
		}
	}
	many := strings.Repeat("DOMAIN,a.com,DIRECT\n", MaxRules+1)
	if _, err := ParseRules(many, testGroups); err == nil || !strings.Contains(err.Error(), "rules_too_many") {
		t.Fatalf("too many: %v", err)
	}
}

// Served rules skip what no longer parses (a group renamed since) instead of failing
// every profile, and the profile puts them after the panel's own direct hosts and
// before the built-in routing.
func TestRulesInProfile(t *testing.T) {
	text := "DOMAIN-SUFFIX,sber.ru,DIRECT\nDOMAIN,old.example.com,Старая группа\nGEOSITE,youtube,PROXY"
	rules := ServedRules(text, testGroups)
	if len(rules) != 2 || rules[1] != "GEOSITE,youtube,PROXY" {
		t.Fatalf("served: %q", rules)
	}
	if again := ServedRules(text, testGroups); &again[0] != &rules[0] {
		t.Fatal("the same text is parsed again instead of cached")
	}
	prof := profile(t, "")
	prof.Direct, prof.Rules = []string{"vpn.example.com"}, rules
	raw, err := Mihomo(prof, testGroups, RoutingRUDirect)
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Rules []string `json:"rules"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	at := func(r string) int {
		for i, x := range cfg.Rules {
			if x == r {
				return i
			}
		}
		return -1
	}
	own, lan, geo := at("DOMAIN-SUFFIX,sber.ru,DIRECT"), at("GEOIP,LAN,DIRECT,no-resolve"), at("GEOSITE,category-ru,DIRECT")
	if own < 0 || lan < 0 || geo < 0 || !(lan < own && own < geo) || cfg.Rules[len(cfg.Rules)-1] != "MATCH,Мой VPN" {
		t.Fatalf("order: %q", cfg.Rules)
	}
	if at("DOMAIN,vpn.example.com,DIRECT") > own {
		t.Fatalf("the panel's host must come before the admin's rules: %q", cfg.Rules)
	}
}

// A rule type younger than an app's core would fail its whole profile: those go only to
// mihomo apps that name a core with them.
func TestRulesFor(t *testing.T) {
	rules := []string{"DOMAIN-SUFFIX,a.com,DIRECT", "DOMAIN-WILDCARD,*.b.com,PROXY", "IP-ASN,13335,PROXY", "GEOSITE,youtube,PROXY"}
	for ua, want := range map[string]int{
		"mihomo/1.19.31":     4,
		"mihomo/1.19.11":     3, // no DOMAIN-WILDCARD before 1.19.12
		"mihomo/1.18.1":      2, // nor IP-ASN before 1.18.2
		"clash-verge/v2.4.0": 2, // the core is not named
		"Stash/3.0":          2,
	} {
		if got := RulesFor(rules, DetectApp(ua)); len(got) != want {
			t.Errorf("%s: %q", ua, got)
		}
	}
	if got := RulesFor(rules[:1], DetectApp("Stash/3.0")); &got[0] != &rules[0] {
		t.Error("nothing to drop, yet the rules were copied")
	}
}
