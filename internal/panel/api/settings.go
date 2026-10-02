package api

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"mikan/internal/hostname"
	"mikan/internal/panel/acme"
	"mikan/internal/panel/dnscheck"
	"mikan/internal/panel/domain"
	"mikan/internal/panel/secure"
	"mikan/internal/panel/settings"
	"mikan/internal/panel/store/db"
	"mikan/internal/panel/subs"
	"mikan/internal/proto"
)

type SettingsView struct {
	Brand        string   `json:"brand"`
	SupportURL   string   `json:"support_url"`
	PublicHost   string   `json:"public_host"`
	Domain       string   `json:"domain"`
	PanelPort    int      `json:"panel_port"`
	SubPort      int      `json:"sub_port" doc:"Отдельный порт подписок; 0 — порт панели. Порт панели отдаёт подписки в любом случае"`
	SubPortError string   `json:"sub_port_error,omitempty" doc:"sub_port_busy — сохранённый порт занят на сервере, подписки пока идут через порт панели"`
	QuietHourUTC int      `json:"quiet_hour_utc" doc:"Час (UTC), когда пополняется пул слотов: переподключение QUIC-клиентов"`
	AdminURL     string   `json:"admin_url"`
	SubBaseURL   string   `json:"sub_base_url"`
	SubGroupMain string   `json:"sub_group_main" doc:"Главная группа в Clash-приложениях"`
	SubGroupAuto string   `json:"sub_group_auto" doc:"Группа автовыбора самого быстрого подключения"`
	SubRules     string   `json:"sub_rules" doc:"Свои правила Clash: по строке TYPE,VALUE,TARGET[,no-resolve]; # — комментарий"`
	RuleTargets  []string `json:"rule_targets" doc:"Куда правило может направить трафик: DIRECT, REJECT, REJECT-DROP, PROXY и группы"`
	SubRouting   string   `json:"sub_routing" enum:"ru_direct,all" doc:"Маршруты в Clash-приложениях: ru_direct — российские сайты и IP напрямую по геобазам mihomo, all — всё через VPN"`
	Fingerprint  string   `json:"client_fingerprint" doc:"Отпечаток TLS (uTLS) у клиентов, если у подключения не задан свой: chrome, firefox, safari, ios, android, edge, 360, qq, random, randomized или своё значение"`
	AutoPort     bool     `json:"auto_port" doc:"Переносить подключение на другой порт, если клиенты перестали до него доходить"`
	AutoSNI      bool     `json:"auto_sni" doc:"Менять сайт маскировки REALITY, если он перестал подходить"`
	// Devices: see domain.Devices.
	DeviceBinding bool        `json:"device_binding" doc:"Привязывать подписку к устройствам: у каждого устройства свои ключи"`
	RequireHWID   bool        `json:"device_require_hwid" doc:"Не выдавать подписку приложениям без ID устройства (иначе они вместе занимают одно место)"`
	DefaultLang   string      `json:"default_lang" enum:"auto,ru,en" doc:"Язык админки и страницы подписки, пока человек не выбрал свой; auto — по языку браузера. На нём же названия по умолчанию: группа автовыбора и меню ненастроенного бота"`
	Certificate   acme.Status `json:"certificate"`
}

type settingsOutput struct{ Body SettingsView }

type patchSettingsInput struct {
	Body struct {
		Brand         *string `json:"brand,omitempty" maxLength:"40"`
		SupportURL    *string `json:"support_url,omitempty" maxLength:"200" doc:"https://… или tg://…"`
		PublicHost    *string `json:"public_host,omitempty" maxLength:"253"`
		Domain        *string `json:"domain,omitempty" maxLength:"253"`
		QuietHourUTC  *int    `json:"quiet_hour_utc,omitempty" minimum:"0" maximum:"23"`
		SubGroupMain  *string `json:"sub_group_main,omitempty" maxLength:"200"`
		SubGroupAuto  *string `json:"sub_group_auto,omitempty" maxLength:"200"`
		SubRouting    *string `json:"sub_routing,omitempty" enum:"ru_direct,all"`
		SubRules      *string `json:"sub_rules,omitempty" maxLength:"65536" doc:"Свои правила Clash, до 500 строк; ошибка указывает номер строки"`
		Fingerprint   *string `json:"client_fingerprint,omitempty" pattern:"^[a-z0-9_]{1,32}$" doc:"Из списка или своё: латиница в нижнем регистре, цифры и _, до 32 символов"`
		AutoPort      *bool   `json:"auto_port,omitempty"`
		AutoSNI       *bool   `json:"auto_sni,omitempty"`
		DeviceBinding *bool   `json:"device_binding,omitempty"`
		RequireHWID   *bool   `json:"device_require_hwid,omitempty"`
		DefaultLang   *string `json:"default_lang,omitempty" enum:"auto,ru,en"`
		SubPort       *int    `json:"sub_port,omitempty" minimum:"0" maximum:"65535" doc:"Отдельный порт подписок на сервере панели; 0 — убрать. Ссылки переезжают на него, старые продолжают работать"`
	}
}

type resetPathOutput struct {
	Body struct {
		AdminURL string `json:"admin_url"`
	}
}

func (h *handlers) registerSettings() {
	huma.Register(h.api, huma.Operation{OperationID: "get-settings", Method: http.MethodGet, Path: "/api/v1/settings", Summary: "Настройки", Tags: []string{"settings"}}, h.getSettings)
	huma.Register(h.api, huma.Operation{OperationID: "update-settings", Method: http.MethodPatch, Path: "/api/v1/settings", Summary: "Изменить настройки", Tags: []string{"settings"}}, h.updateSettings)
	huma.Register(h.api, huma.Operation{OperationID: "reset-admin-path", Metadata: sessionOnly, Extensions: sessionOnlyExt, Method: http.MethodPost, Path: "/api/v1/settings/reset-admin-path", Summary: "Выдать новую секретную ссылку на панель", Tags: []string{"settings"}}, h.resetAdminPath)
	huma.Register(h.api, huma.Operation{OperationID: "renew-certificate", Method: http.MethodPost, Path: "/api/v1/settings/certificate/renew", Summary: "Запросить сертификат Let's Encrypt сейчас", Tags: []string{"settings"}, DefaultStatus: http.StatusAccepted}, h.renewCertificate)
}

func (h *handlers) renewCertificate(ctx context.Context, _ *struct{}) (*struct{}, error) {
	if h.d.RenewCert == nil {
		return nil, huma.Error409Conflict("acme_disabled")
	}
	h.d.RenewCert()
	h.audit(ctx, sessionOf(ctx).AdminID, "settings.renew_certificate", "", "", nil)
	return nil, nil
}

func (h *handlers) readSettings(ctx context.Context) (SettingsView, error) {
	var v SettingsView
	var err error
	get := func(key string, dst *string) {
		if err == nil {
			*dst, _, err = settings.Get[string](ctx, h.d.Settings, key)
		}
	}
	get(settings.KeyBrand, &v.Brand)
	get(settings.KeySupportURL, &v.SupportURL)
	get(settings.KeyPublicHost, &v.PublicHost)
	get(settings.KeyDomain, &v.Domain)
	get(settings.KeyGroupMain, &v.SubGroupMain)
	get(settings.KeyGroupAuto, &v.SubGroupAuto)
	get(settings.KeyRouting, &v.SubRouting)
	get(settings.KeyRules, &v.SubRules)
	v.SubRouting = string(subs.ParseRouting(v.SubRouting))
	get(settings.KeyFingerprint, &v.Fingerprint)
	if !proto.ValidFingerprint(v.Fingerprint) {
		v.Fingerprint = proto.DefaultFingerprint
	}
	if err != nil {
		return v, err
	}
	if v.DefaultLang, err = h.d.Settings.Lang(ctx); err != nil {
		return v, err
	}
	g := subs.Groups{Main: v.SubGroupMain, Auto: v.SubGroupAuto}.WithDefaults(v.DefaultLang)
	v.SubGroupMain, v.SubGroupAuto = g.Main, g.Auto
	v.RuleTargets = subs.RuleTargets(g)
	if v.DefaultLang == "" {
		v.DefaultLang = "auto"
	}
	if v.PanelPort, _, err = settings.Get[int](ctx, h.d.Settings, settings.KeyPanelPort); err != nil {
		return v, err
	}
	if v.SubPort, _, err = settings.Get[int](ctx, h.d.Settings, settings.KeySubPort); err != nil {
		return v, err
	}
	if v.SubPort > 0 && h.d.SubPortError != nil {
		v.SubPortError = h.d.SubPortError()
	}
	if v.QuietHourUTC, _, err = settings.Get[int](ctx, h.d.Settings, settings.KeyQuietHour); err != nil {
		return v, err
	}
	if v.AutoPort, err = h.d.Settings.On(ctx, settings.AutoPort); err != nil {
		return v, err
	}
	if v.AutoSNI, err = h.d.Settings.On(ctx, settings.AutoSNI); err != nil {
		return v, err
	}
	if v.DeviceBinding, err = h.d.Settings.On(ctx, settings.DeviceBinding); err != nil {
		return v, err
	}
	if v.RequireHWID, err = h.d.Settings.On(ctx, settings.RequireHWID); err != nil {
		return v, err
	}
	if v.Brand == "" {
		v.Brand = "VPN"
	}
	paths, err := h.d.Settings.Paths(ctx)
	if err != nil {
		return v, err
	}
	host := v.Domain
	if host == "" {
		host = v.PublicHost
	}
	// The addresses carry the secret path segments: not for a key that may only read.
	if host != "" && !hidesSecrets(ctx) {
		v.AdminURL = "https://" + net.JoinHostPort(host, strconv.Itoa(v.PanelPort)) + "/" + paths.Admin + "/"
		subPort := v.PanelPort
		if v.SubPort > 0 {
			subPort = v.SubPort
		}
		v.SubBaseURL = "https://" + net.JoinHostPort(host, strconv.Itoa(subPort)) + "/" + paths.Sub + "/"
	}
	v.Certificate = acme.Status{Kind: "self-signed"}
	if h.d.Cert != nil {
		v.Certificate = h.d.Cert()
	}
	return v, nil
}

func (h *handlers) getSettings(ctx context.Context, _ *struct{}) (*settingsOutput, error) {
	v, err := h.readSettings(ctx)
	if err != nil {
		return nil, err
	}
	return &settingsOutput{Body: v}, nil
}

func (h *handlers) updateSettings(ctx context.Context, in *patchSettingsInput) (*settingsOutput, error) {
	b := in.Body
	// Where clients are sent, and what they are told to trust: a leaked API key must not
	// move subscriptions to another server or add rules to every client.
	for field, touched := range map[string]bool{"public_host": b.PublicHost != nil, "domain": b.Domain != nil, "sub_port": b.SubPort != nil,
		"sub_rules": b.SubRules != nil, "support_url": b.SupportURL != nil} {
		if touched {
			if err := requireSession(ctx, field); err != nil {
				return nil, err
			}
		}
	}
	var details []error
	if b.PublicHost != nil && !hostname.Valid(*b.PublicHost) {
		details = append(details, &huma.ErrorDetail{Location: "body.public_host", Message: "public_host_invalid"})
	}
	if b.Domain != nil && *b.Domain != "" && !hostname.Valid(*b.Domain) {
		details = append(details, &huma.ErrorDetail{Location: "body.domain", Message: "domain_invalid"})
	}
	if b.SupportURL != nil && *b.SupportURL != "" && !strings.HasPrefix(*b.SupportURL, "https://") && !strings.HasPrefix(*b.SupportURL, "tg://") {
		details = append(details, &huma.ErrorDetail{Location: "body.support_url", Message: "support_url_invalid"})
	}
	if b.SubGroupMain != nil || b.SubGroupAuto != nil || b.SubRules != nil {
		cur, err := h.groups(ctx)
		if err != nil {
			return nil, err
		}
		next := cur
		if b.SubGroupMain != nil {
			next.Main = strings.TrimSpace(*b.SubGroupMain)
		}
		if b.SubGroupAuto != nil {
			next.Auto = strings.TrimSpace(*b.SubGroupAuto)
		}
		if b.SubGroupMain != nil || b.SubGroupAuto != nil {
			details = append(details, h.checkGroups(ctx, next)...)
		}
		// The rules are checked against the groups they will meet: a renamed group must
		// not leave a rule pointing nowhere.
		rules := b.SubRules
		if rules == nil {
			saved, err := h.d.Settings.String(ctx, settings.KeyRules)
			if err != nil {
				return nil, err
			}
			rules = &saved
		}
		if _, err := subs.ParseRules(*rules, next); err != nil {
			var re *subs.RuleError
			if !errors.As(err, &re) {
				return nil, err
			}
			field := "body.sub_rules"
			if b.SubRules == nil {
				field, re.Code = "body.sub_group_main", "group_in_rules"
				if b.SubGroupMain == nil {
					field = "body.sub_group_auto"
				}
			}
			details = append(details, &huma.ErrorDetail{Location: field, Message: re.Code, Value: re.Line})
		}
	}
	if b.SubPort != nil {
		if d, err := h.checkSubPort(ctx, *b.SubPort); err != nil {
			return nil, err
		} else if d != nil {
			details = append(details, d)
		}
	}
	// The domain must lead to this server: checked when it or the server's address changes,
	// after the cheap checks, since it asks public DNS.
	if (b.Domain != nil || b.PublicHost != nil) && len(details) == 0 {
		cur, err := h.d.Settings.String(ctx, settings.KeyDomain)
		if err != nil {
			return nil, err
		}
		dom := cur
		if b.Domain != nil {
			dom = strings.TrimSpace(*b.Domain)
		}
		if dom != "" && (dom != cur || b.PublicHost != nil) {
			host := ""
			if b.PublicHost != nil {
				host = strings.TrimSpace(*b.PublicHost)
			} else if host, err = h.d.Settings.String(ctx, settings.KeyPublicHost); err != nil {
				return nil, err
			}
			if d := h.domainHere(ctx, "body.domain", dom, dnscheck.Own(host)); d != nil {
				details = append(details, d)
			}
		}
	}
	if len(details) > 0 {
		return nil, huma.Error422UnprocessableEntity("validation", details...)
	}
	// The port opens first: one that cannot be had changes nothing. If the settings then
	// fail to save, the port is put back, so the server does not listen on one nobody saved.
	var oldPort int
	if b.SubPort != nil {
		if h.d.SubPort == nil {
			return nil, huma.Error503ServiceUnavailable("sub_port_unavailable")
		}
		var err error
		if oldPort, _, err = settings.Get[int](ctx, h.d.Settings, settings.KeySubPort); err != nil {
			return nil, err
		}
		if err := h.d.SubPort(*b.SubPort); err != nil {
			return nil, huma.Error422UnprocessableEntity("validation", &huma.ErrorDetail{Location: "body.sub_port", Message: "sub_port_busy", Value: *b.SubPort})
		}
	}
	// Every setting of the request is written in one transaction: a failure in the middle
	// leaves the settings as they were, not half changed.
	err := h.d.Store.Tx(ctx, func(q *db.Queries) error {
		set := settings.New(q)
		if b.SubPort != nil {
			if err := settings.Set(ctx, set, settings.KeySubPort, *b.SubPort); err != nil {
				return err
			}
		}
		if b.SubRules != nil {
			if err := settings.Set(ctx, set, settings.KeyRules, strings.TrimRight(*b.SubRules, " \n\r\t")); err != nil {
				return err
			}
		}
		for key, v := range map[string]*string{settings.KeyBrand: b.Brand, settings.KeySupportURL: b.SupportURL, settings.KeyPublicHost: b.PublicHost, settings.KeyDomain: b.Domain,
			settings.KeyGroupMain: b.SubGroupMain, settings.KeyGroupAuto: b.SubGroupAuto, settings.KeyRouting: b.SubRouting, settings.KeyFingerprint: b.Fingerprint, settings.KeyDefaultLang: b.DefaultLang} {
			if v == nil {
				continue
			}
			if err := settings.Set(ctx, set, key, strings.TrimSpace(*v)); err != nil {
				return err
			}
		}
		if b.QuietHourUTC != nil {
			if err := settings.Set(ctx, set, settings.KeyQuietHour, *b.QuietHourUTC); err != nil {
				return err
			}
		}
		for key, v := range map[string]*bool{settings.KeyAutoPort: b.AutoPort, settings.KeyAutoSNI: b.AutoSNI,
			settings.KeyDeviceBinding: b.DeviceBinding, settings.KeyRequireHWID: b.RequireHWID} {
			if v != nil {
				if err := settings.Set(ctx, set, key, *v); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		if b.SubPort != nil {
			if rerr := h.d.SubPort(oldPort); rerr != nil {
				h.d.Log.Warn("sub port not put back", "port", oldPort, "err", rerr)
			}
		}
		return nil, err
	}
	// The bot's Mini App button points at the subscription page.
	if b.SubPort != nil && h.d.Telegram != nil {
		h.d.Telegram.Reload()
	}
	var auditDetails map[string]any
	if b.SubPort != nil {
		auditDetails = map[string]any{"sub_port": *b.SubPort}
	}
	h.audit(ctx, sessionOf(ctx).AdminID, "settings.update", "", "", auditDetails)
	v, err := h.readSettings(ctx)
	if err != nil {
		return nil, err
	}
	return &settingsOutput{Body: v}, nil
}

// subPortReserved can never serve subscriptions: SSH, and 80 that Let's Encrypt needs.
var subPortReserved = map[int]bool{22: true, 80: true}

// checkSubPort says what is wrong with port as the subscription port, or nil: it has to
// differ from the panel's port and stay clear of what the panel's own node listens on
// over TCP (an inbound or the cascade relay), which shares the panel's server.
func (h *handlers) checkSubPort(ctx context.Context, port int) (*huma.ErrorDetail, error) {
	if port == 0 {
		return nil, nil
	}
	bad := func(code string, value any) (*huma.ErrorDetail, error) {
		return &huma.ErrorDetail{Location: "body.sub_port", Message: code, Value: value}, nil
	}
	if subPortReserved[port] {
		return bad("sub_port_reserved", port)
	}
	nodes, err := h.d.Store.Q.ListNodes(ctx)
	if err != nil {
		return nil, err
	}
	for _, n := range nodes {
		if n.Address != "" {
			continue
		}
		ports, err := domain.NodePorts(ctx, h.d.Store.Q, n)
		if err != nil {
			return nil, err
		}
		owner, busy := ports.Busy(strconv.Itoa(port), "tcp", domain.PortHolder{Kind: domain.PortSub})
		switch {
		case !busy:
		case owner.Kind == domain.PortPanel:
			return bad("sub_port_panel", port)
		case owner.Kind == domain.PortRelay:
			return bad("sub_port_relay", port)
		default:
			return bad("sub_port_inbound", owner.Name)
		}
	}
	return nil, nil
}

// groups returns the subscription group names with defaults applied.
func (h *handlers) groups(ctx context.Context) (subs.Groups, error) {
	var g subs.Groups
	var err error
	if g.Main, err = h.d.Settings.String(ctx, settings.KeyGroupMain); err != nil {
		return g, err
	}
	if g.Auto, err = h.d.Settings.String(ctx, settings.KeyGroupAuto); err != nil {
		return g, err
	}
	lang, err := h.d.Settings.Lang(ctx)
	if err != nil {
		return g, err
	}
	return g.WithDefaults(lang), nil
}

// checkGroups: a profile with a group named like a proxy, a built-in policy or the
// other group does not load in any Clash app.
func (h *handlers) checkGroups(ctx context.Context, g subs.Groups) []error {
	var out []error
	bad := func(field, code string, value any) {
		out = append(out, &huma.ErrorDetail{Location: "body." + field, Message: code, Value: value})
	}
	if err := subs.ValidName(g.Main); err != nil {
		bad("sub_group_main", err.Error(), nil)
	}
	if err := subs.ValidName(g.Auto); err != nil {
		bad("sub_group_auto", err.Error(), nil)
	}
	if strings.EqualFold(g.Auto, subs.AliasGroup) {
		bad("sub_group_auto", "group_alias_taken", subs.AliasGroup)
	}
	if strings.EqualFold(g.Main, g.Auto) {
		bad("sub_group_auto", "groups_same", nil)
	}
	if inbounds, err := h.d.Store.Q.ListInbounds(ctx); err == nil {
		for _, in := range inbounds {
			name := domain.ProxyName(in)
			if strings.EqualFold(name, g.Main) {
				bad("sub_group_main", "group_is_proxy", in.Name)
			}
			if strings.EqualFold(name, g.Auto) {
				bad("sub_group_auto", "group_is_proxy", in.Name)
			}
		}
	}
	return out
}

// checkSubName validates an inbound's name in the subscription and returns an error
// code, "" when the name is fine.
func (h *handlers) checkSubName(ctx context.Context, name string) string {
	if err := subs.ValidName(name); err != nil {
		return err.Error()
	}
	g, err := h.groups(ctx)
	if err != nil {
		return ""
	}
	for _, x := range []string{g.Main, g.Auto, subs.AliasGroup} {
		if strings.EqualFold(name, x) {
			return "name_is_group"
		}
	}
	return ""
}

func (h *handlers) resetAdminPath(ctx context.Context, _ *struct{}) (*resetPathOutput, error) {
	if err := settings.Set(ctx, h.d.Settings, settings.KeyAdminPath, secure.Token(24)); err != nil {
		return nil, err
	}
	h.audit(ctx, sessionOf(ctx).AdminID, "settings.reset_admin_path", "", "", nil)
	v, err := h.readSettings(ctx)
	if err != nil {
		return nil, err
	}
	out := &resetPathOutput{}
	out.Body.AdminURL = v.AdminURL
	return out, nil
}
