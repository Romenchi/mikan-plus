package subs

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"mikan/internal/panel/presets"
	"mikan/internal/panel/store/db"
	"mikan/internal/proto"
)

func TestFormat(t *testing.T) {
	cases := map[string]string{
		"clash-verge/v2.2.3":         "clash",
		"FlClash/v0.8.80 clash-meta": "clash",
		"mihomo/1.19.31":             "clash",
		"Stash/3.1.1 Clash/1.9.0":    "clash",
		"Happ/3.4.1":                 "uri",
		"v2rayNG/1.10.2":             "uri",
		"v2RayTun/Android":           "uri",
		"Streisand/1.6":              "uri",
		"HiddifyNext/2.5.7":          "uri",
		"curl/8.9":                   "uri",
	}
	for ua, want := range cases {
		if got := Format(ua, "*/*", ""); got != want {
			t.Errorf("%q → %s, want %s", ua, got, want)
		}
	}
	if Format("Mozilla/5.0 (iPhone)", "text/html,application/xhtml+xml", "") != "html" {
		t.Error("browser must get the page")
	}
	if Format("Happ/3", "", "clash") != "clash" {
		t.Error("?format= must override the User-Agent")
	}
}

func profile(t *testing.T, pin string) Profile {
	t.Helper()
	var ins []db.Inbound
	for i, p := range presets.All {
		if !p.Default {
			continue
		}
		c, err := presets.NewConfig(p.ID, "www.example.com:443")
		if err != nil {
			t.Fatal(err)
		}
		ins = append(ins, db.Inbound{ID: int64(i + 1), NodeID: 1, Name: p.Name, Preset: p.ID, Port: p.Port, Enabled: 1, Config: c})
	}
	return Profile{
		Slot:     db.Slot{Name: "s000001", Uuid: "0b4ddc4c-7c4f-4a36-9d62-6f1a44b8c4e1", Secret: "S3cr3t+/="},
		Inbounds: ins,
		Nodes:    []Node{{ID: 1, Endpoint: Endpoint{Host: "203.0.113.7", PinSHA256: pin}}},
	}
}

func TestURIs(t *testing.T) {
	links, err := URIs(profile(t, "ab12"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(links, "\n")
	if len(lines) != 4 {
		t.Fatalf("got %d links", len(lines))
	}
	byName := map[string]*url.URL{}
	for _, l := range lines {
		u, err := url.Parse(l)
		if err != nil {
			t.Fatal(err)
		}
		byName[u.Fragment] = u
	}
	xhttp := byName["VLESS XHTTP"]
	if lines[0] != xhttp.String() || xhttp.Host != "203.0.113.7:443" ||
		xhttp.Query().Get("type") != "xhttp" || xhttp.Query().Get("mode") != "stream-one" || !strings.HasPrefix(xhttp.Query().Get("path"), "/") {
		t.Fatalf("xhttp must be the first link, on 443: %s", links)
	}
	vision := byName["VLESS Vision"]
	q := vision.Query()
	if vision.Scheme != "vless" || vision.User.Username() != "0b4ddc4c-7c4f-4a36-9d62-6f1a44b8c4e1" || vision.Host != "203.0.113.7:8443" ||
		q.Get("flow") != "xtls-rprx-vision" || q.Get("security") != "reality" || q.Get("pbk") == "" || q.Get("sid") == "" || q.Get("sni") != "www.example.com" {
		t.Fatalf("vision link: %s", vision)
	}
	hy2 := byName["Hysteria2"]
	if hy2.Scheme != "hysteria2" || hy2.User.Username() != "S3cr3t+/=" || hy2.Query().Get("pinSHA256") != "ab12" || hy2.Query().Get("obfs") != "salamander" {
		t.Fatalf("hy2 link: %s", hy2)
	}
	tuic := byName["TUIC"]
	pw, _ := tuic.User.Password()
	if tuic.Scheme != "tuic" || pw != "S3cr3t+/=" || tuic.Query().Get("allow_insecure") != "1" {
		t.Fatalf("tuic link: %s", tuic)
	}
}

func TestURIsWithRealCertificateDoNotPin(t *testing.T) {
	links, err := URIs(profile(t, ""))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(links, "insecure") || strings.Contains(links, "pinSHA256") {
		t.Fatalf("real certificate must be verified normally: %s", links)
	}
}

func TestMihomoProfile(t *testing.T) {
	prof := profile(t, "ab12")
	prof.Direct = []string{"203.0.113.7", "vpn.example.com", ""}
	raw, err := Mihomo(prof, Groups{}, RoutingAll)
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Proxies []map[string]any `json:"proxies"`
		Groups  []struct {
			Name    string   `json:"name"`
			Proxies []string `json:"proxies"`
			Hidden  bool     `json:"hidden"`
		} `json:"proxy-groups"`
		Rules []string `json:"rules"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Proxies) != 4 || cfg.Groups[0].Name != "VPN" || cfg.Groups[1].Name != "Авто" {
		t.Fatalf("profile: %s", raw)
	}
	// Rules injected by the app itself ("PROCESS-NAME,x.exe,PROXY") need a PROXY group.
	if alias := cfg.Groups[2]; alias.Name != "PROXY" || !alias.Hidden || len(alias.Proxies) != 1 || alias.Proxies[0] != "VPN" {
		t.Fatalf("PROXY alias: %+v", cfg.Groups)
	}
	wantRules := []string{"IP-CIDR,203.0.113.7/32,DIRECT,no-resolve", "DOMAIN,vpn.example.com,DIRECT", "GEOIP,LAN,DIRECT,no-resolve", "MATCH,VPN"}
	if strings.Join(cfg.Rules, "|") != strings.Join(wantRules, "|") {
		t.Fatalf("rules: %v", cfg.Rules)
	}
	byType := map[string]map[string]any{}
	for _, p := range cfg.Proxies {
		byType[p["name"].(string)] = p
	}
	if byType["VLESS XHTTP"]["network"] != "xhttp" || byType["VLESS Vision"]["flow"] != "xtls-rprx-vision" {
		t.Fatalf("vless proxies: %v", cfg.Proxies)
	}
	// Without reuse-settings mihomo opens a TLS handshake per app connection (see xmux).
	if opts, _ := byType["VLESS XHTTP"]["xhttp-opts"].(map[string]any); opts["reuse-settings"] == nil {
		t.Fatalf("xhttp must multiplex: %v", byType["VLESS XHTTP"])
	}
	if byType["Hysteria2"]["fingerprint"] != "ab12" || byType["TUIC"]["password"] != "S3cr3t+/=" {
		t.Fatalf("quic proxies: %v", cfg.Proxies)
	}
}

func TestCustomNames(t *testing.T) {
	prof := profile(t, "")
	prof.Inbounds[0].DisplayName = "🇳🇱 Нидерланды"
	prof.Inbounds[1].DisplayName = "🇳🇱 Нидерланды" // duplicates must not break the profile
	raw, err := Mihomo(prof, Groups{Main: "🚀 Мой VPN", Auto: "⚡ Быстрый"}, RoutingRUDirect)
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Proxies []map[string]any `json:"proxies"`
		Groups  []struct {
			Name    string   `json:"name"`
			Proxies []string `json:"proxies"`
		} `json:"proxy-groups"`
		Rules []string `json:"rules"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Proxies[0]["name"] != "🇳🇱 Нидерланды" || cfg.Proxies[1]["name"] != "🇳🇱 Нидерланды 2" {
		t.Fatalf("names: %v %v", cfg.Proxies[0]["name"], cfg.Proxies[1]["name"])
	}
	if cfg.Groups[0].Name != "🚀 Мой VPN" || cfg.Groups[0].Proxies[0] != "⚡ Быстрый" || cfg.Rules[len(cfg.Rules)-1] != "MATCH,🚀 Мой VPN" {
		t.Fatalf("groups: %+v rules: %v", cfg.Groups, cfg.Rules)
	}
	links, err := URIs(prof)
	if err != nil {
		t.Fatal(err)
	}
	first, _ := url.Parse(strings.Split(links, "\n")[0])
	if first.Fragment != "🇳🇱 Нидерланды" {
		t.Fatalf("link name: %q", first.Fragment)
	}
}

func TestRouting(t *testing.T) {
	type dns struct {
		Nameserver  []string            `json:"nameserver"`
		ProxyServer []string            `json:"proxy-server-nameserver"`
		Policy      map[string][]string `json:"nameserver-policy"`
	}
	type profileJSON struct {
		Groups []struct {
			Name string `json:"name"`
		} `json:"proxy-groups"`
		Rules   []string          `json:"rules"`
		DNS     dns               `json:"dns"`
		GeoxURL map[string]string `json:"geox-url"`
	}
	render := func(g Groups, r Routing) profileJSON {
		t.Helper()
		prof := profile(t, "")
		prof.Direct = []string{"203.0.113.7"}
		raw, err := Mihomo(prof, g, r)
		if err != nil {
			t.Fatal(err)
		}
		var cfg profileJSON
		if err := json.Unmarshal(raw, &cfg); err != nil {
			t.Fatal(err)
		}
		return cfg
	}

	ru := render(Groups{}, RoutingRUDirect)
	want := append([]string{"IP-CIDR,203.0.113.7/32,DIRECT,no-resolve", "GEOIP,LAN,DIRECT,no-resolve"}, ruDirectRules...)
	want = append(want, "MATCH,VPN")
	if strings.Join(ru.Rules, "|") != strings.Join(want, "|") {
		t.Fatalf("ru_direct rules: %v", ru.Rules)
	}
	if !strings.HasPrefix(ru.GeoxURL["geosite"], "https://github.com/MetaCubeX/meta-rules-dat/") || ru.GeoxURL["mmdb"] == "" {
		t.Fatalf("geodata must come from MetaCubeX: %v", ru.GeoxURL)
	}
	// Every domain gets resolved on the client for GEOIP,ru: through the tunnel, except
	// Russian ones and the server's own name (resolving it through itself would deadlock).
	for _, ns := range ru.DNS.Nameserver {
		if !strings.HasSuffix(ns, "#PROXY") {
			t.Errorf("nameserver %q must go through the tunnel", ns)
		}
	}
	if len(ru.DNS.ProxyServer) == 0 || strings.Contains(strings.Join(ru.DNS.ProxyServer, ""), "#") {
		t.Errorf("proxy-server-nameserver must be direct: %v", ru.DNS.ProxyServer)
	}
	if len(ru.DNS.Policy["geosite:category-ru"]) == 0 {
		t.Errorf("Russian domains need a Russian resolver: %v", ru.DNS.Policy)
	}

	// The "#PROXY" suffix must name a group even when the admin calls the main group PROXY.
	named := render(Groups{Main: "PROXY"}, RoutingRUDirect)
	if named.Groups[0].Name != "PROXY" || named.Rules[len(named.Rules)-1] != "MATCH,PROXY" {
		t.Fatalf("main group named PROXY: %+v %v", named.Groups, named.Rules)
	}

	all := render(Groups{}, RoutingAll)
	for _, r := range all.Rules {
		if strings.HasPrefix(r, "GEOSITE,") || r == "GEOIP,ru,DIRECT" {
			t.Errorf("all mode needs no geodata: %v", all.Rules)
		}
	}
	if all.GeoxURL != nil || len(all.DNS.Policy) != 0 || strings.Contains(strings.Join(all.DNS.Nameserver, ""), "#") {
		t.Errorf("all mode keeps plain DNS: %+v %v", all.DNS, all.GeoxURL)
	}

	for in, want := range map[string]Routing{"": RoutingRUDirect, "all": RoutingAll, "ru_direct": RoutingRUDirect, "blocked": RoutingRUDirect} {
		if got := ParseRouting(in); got != want {
			t.Errorf("ParseRouting(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestValidName(t *testing.T) {
	for _, ok := range []string{"VPN", "🇳🇱 Нидерланды", "Авто", "PROXY"} {
		if err := ValidName(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"", " VPN", "a,b", "DIRECT", "global", "x\ny", strings.Repeat("я", 49)} {
		if ValidName(bad) == nil {
			t.Errorf("%q must be refused", bad)
		}
	}
}

func TestMultiNodeProfile(t *testing.T) {
	prof := profile(t, "aa11")
	nl := Node{ID: 1, Name: "🇳🇱 Нидерланды", Endpoint: Endpoint{Host: "203.0.113.7", PinSHA256: "aa11"}}
	us := Node{ID: 2, Name: "🇺🇸 США", Endpoint: Endpoint{Host: "usa.example.com", SNI: "usa.example.com", PinSHA256: "bb22"}}
	prof.Nodes = []Node{nl, us}
	for i, p := range []string{"vless_reality_xhttp", "hysteria2"} {
		info, _ := presets.Get(p)
		c, err := presets.NewConfig(p, "www.example.com:443")
		if err != nil {
			t.Fatal(err)
		}
		prof.Inbounds = append(prof.Inbounds, db.Inbound{ID: int64(100 + i), NodeID: 2, Name: info.Name, Preset: p, Port: info.Port, Enabled: 1, Config: c})
	}
	prof.Inbounds[len(prof.Inbounds)-1].DisplayName = "Особый"
	// An inbound of a node the user is not given (disabled node) stays out.
	prof.Inbounds = append(prof.Inbounds, db.Inbound{ID: 200, NodeID: 3, Name: "x", Preset: "hysteria2", Port: "443", Enabled: 1, Config: prof.Inbounds[0].Config})

	raw, err := Mihomo(prof, Groups{}, RoutingAll)
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Proxies []map[string]any `json:"proxies"`
		Groups  []struct {
			Name    string   `json:"name"`
			Type    string   `json:"type"`
			Proxies []string `json:"proxies"`
		} `json:"proxy-groups"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	server := map[string]any{}
	var names []string
	for _, p := range cfg.Proxies {
		name := p["name"].(string)
		names = append(names, name)
		server[name] = p["server"]
	}
	want := []string{"🇳🇱 VLESS XHTTP", "🇳🇱 Hysteria2", "🇳🇱 TUIC", "🇳🇱 VLESS Vision", "🇺🇸 VLESS XHTTP", "Особый"}
	if strings.Join(names, "|") != strings.Join(want, "|") {
		t.Fatalf("proxies: %v", names)
	}
	if server["🇺🇸 VLESS XHTTP"] != "usa.example.com" || server["🇳🇱 VLESS XHTTP"] != "203.0.113.7" {
		t.Fatalf("each node is reached at its own address: %v", server)
	}
	for _, p := range cfg.Proxies {
		if p["name"] == "Особый" && (p["sni"] != "usa.example.com" || p["fingerprint"] != "bb22") {
			t.Fatalf("Hysteria2 on the US node pins its own certificate: %v", p)
		}
	}
	main, byName := cfg.Groups[0], map[string][]string{}
	for _, g := range cfg.Groups {
		byName[g.Name] = g.Proxies
	}
	if strings.Join(main.Proxies[:3], "|") != "Авто|🇳🇱 Нидерланды|🇺🇸 США" {
		t.Fatalf("main group lists the country groups first: %v", main.Proxies)
	}
	if strings.Join(byName["🇺🇸 США"], "|") != "🇺🇸 VLESS XHTTP|Особый" || len(byName["Авто"]) != len(want) {
		t.Fatalf("country groups: %v", byName)
	}
	links, err := URIs(prof)
	if err != nil || strings.Count(links, "\n") != len(want)-1 {
		t.Fatalf("links: %v %q", err, links)
	}

	// A node named like a group must not break the profile: it just gets no group.
	prof.Nodes[1].Name = "VPN"
	raw, err = Mihomo(prof, Groups{}, RoutingAll)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Groups = nil
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	vpn := 0
	for _, g := range cfg.Groups {
		if g.Name == "VPN" {
			vpn++
		}
	}
	if vpn != 1 {
		t.Fatalf("group names must stay unique: %+v", cfg.Groups)
	}
}

func TestNodePrefix(t *testing.T) {
	for in, want := range map[string]string{"🇳🇱 Нидерланды": "🇳🇱", "🇺🇸США": "🇺🇸", "Германия": "Германия", "": "", " 🇩🇪 DE ": "🇩🇪"} {
		if got := NodePrefix(in); got != want {
			t.Errorf("NodePrefix(%q) = %q, want %q", in, got, want)
		}
	}
}

// The panel's default fingerprint reaches every uTLS proxy in links and the mihomo
// profile; an inbound's own one wins.
func TestDefaultFingerprint(t *testing.T) {
	prof := profile(t, "ab12")
	prof.Fingerprint = "firefox"
	for i, in := range prof.Inbounds {
		if in.Preset == "vless_reality_vision" {
			tpl, err := proto.Parse(in.Config)
			if err != nil || proto.SetFingerprint(tpl, "safari") != nil {
				t.Fatal(err)
			}
			prof.Inbounds[i].Config = proto.Marshal(tpl)
		}
	}
	want := map[string]string{"VLESS XHTTP": "firefox", "VLESS Vision": "safari"}
	links, err := URIs(prof)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range strings.Split(links, "\n") {
		u, err := url.Parse(l)
		if err != nil {
			t.Fatal(err)
		}
		if fp := u.Query().Get("fp"); fp != want[u.Fragment] {
			t.Errorf("%s: fp=%q, want %q", u.Fragment, fp, want[u.Fragment])
		}
	}
	raw, err := Mihomo(prof, Groups{}, RoutingAll)
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Proxies []map[string]any `json:"proxies"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	for _, p := range cfg.Proxies {
		name := p["name"].(string)
		got, _ := p["client-fingerprint"].(string)
		if got != want[name] {
			t.Errorf("%s: client-fingerprint=%q, want %q", name, got, want[name])
		}
	}
}
