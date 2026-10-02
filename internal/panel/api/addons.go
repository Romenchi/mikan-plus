package api

import (
	"context"
	"errors"
	"maps"
	"net/http"
	"slices"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"mikan/internal/panel/addons"
	"mikan/internal/panel/billing"
)

// The marketplace of payment adapters: what the signed catalog offers, what the server
// runs, and each adapter's settings. Only a session may change them: whoever installs a
// payment adapter or sets its keys decides where the money goes.

type AddonCatalogEntry struct {
	ID          string            `json:"id"`
	Name        map[string]string `json:"name" doc:"Название по языкам"`
	Description map[string]string `json:"description"`
	Version     string            `json:"version"`
	Homepage    string            `json:"homepage"`
	Installed   bool              `json:"installed"`
	Update      bool              `json:"update" doc:"Установлен, а в каталоге другая сборка"`
}

type AddonField struct {
	Key      string            `json:"key"`
	Label    map[string]string `json:"label"`
	Type     string            `json:"type" enum:"string,bool"`
	Secret   bool              `json:"secret" doc:"Значение API не отдаёт; пусто при сохранении — оставить прежнее"`
	Required bool              `json:"required"`
	Pattern  string            `json:"pattern,omitempty"`
	Value    any               `json:"value,omitempty" doc:"Текущее значение, кроме секретов"`
	Set      bool              `json:"set" doc:"Значение сохранено"`
}

type AddonView struct {
	ID         string            `json:"id"`
	Version    string            `json:"version"`
	Status     string            `json:"status" enum:"running,failed"`
	Error      string            `json:"error,omitempty" doc:"Почему контейнер не работает"`
	Name       map[string]string `json:"name"`
	Help       map[string]string `json:"help"`
	Enabled    bool              `json:"enabled"`
	Available  bool              `json:"available" doc:"Принимает оплату прямо сейчас: включён, настроен, работает"`
	Settings   []AddonField      `json:"settings"`
	WebhookURL string            `json:"webhook_url" doc:"Адрес для уведомлений в кабинете провайдера"`
	InfoError  string            `json:"info_error,omitempty" doc:"Адаптер не ответил о себе: настройки недоступны"`
}

// AddonRequest is what the panel asked the server for.
type AddonRequest struct {
	Action string `json:"action" enum:"install,remove"`
	ID     string `json:"id"`
	At     string `json:"at" doc:"RFC 3339"`
}

// AddonResult is how the server did what was asked.
type AddonResult struct {
	Action string `json:"action" enum:"install,remove"`
	ID     string `json:"id"`
	State  string `json:"state" enum:"done,failed"`
	Error  string `json:"error,omitempty"`
	At     string `json:"at" doc:"RFC 3339"`
}

type AddonsView struct {
	Catalog      []AddonCatalogEntry `json:"catalog"`
	CatalogError string              `json:"catalog_error,omitempty" doc:"catalog_unavailable — каталог не загрузился"`
	Installed    []AddonView         `json:"installed"`
	Pending      *AddonRequest       `json:"pending,omitempty" doc:"Заявка, которую сервер ещё не взял"`
	Last         *AddonResult        `json:"last,omitempty" doc:"Как сервер выполнил последнюю заявку"`
	Supported    bool                `json:"supported" doc:"Панель видит каталог данных сервера; иначе ставить адаптеры нельзя"`
}

type addonsOutput struct{ Body AddonsView }

type addonIDInput struct {
	ID string `path:"id" pattern:"^[a-z0-9][a-z0-9-]{0,31}$"`
}

type patchAddonInput struct {
	ID   string `path:"id" pattern:"^[a-z0-9][a-z0-9-]{0,31}$"`
	Body struct {
		Enabled  *bool          `json:"enabled,omitempty"`
		Settings map[string]any `json:"settings,omitempty" doc:"Только то, что меняется; секрет пустой строкой — оставить прежний"`
	}
}

func (h *handlers) registerAddons() {
	tags := []string{"payments"}
	op := func(id, method, path, summary string, status int) huma.Operation {
		return huma.Operation{OperationID: id, Method: method, Path: path, Summary: summary, Tags: tags, DefaultStatus: status, Metadata: sessionOnly, Extensions: sessionOnlyExt}
	}
	huma.Register(h.api, op("list-addons", http.MethodGet, "/api/v1/addons", "Маркетплейс способов оплаты", 0), h.listAddons)
	huma.Register(h.api, op("install-addon", http.MethodPost, "/api/v1/addons/{id}/install", "Установить или обновить адаптер: заявка серверу", http.StatusAccepted), h.installAddon)
	huma.Register(h.api, op("remove-addon", http.MethodPost, "/api/v1/addons/{id}/remove", "Удалить адаптер: заявка серверу", http.StatusAccepted), h.removeAddon)
	huma.Register(h.api, op("update-addon", http.MethodPatch, "/api/v1/addons/{id}", "Настройки адаптера", 0), h.patchAddon)
}

func (h *handlers) catalog(ctx context.Context) (addons.Catalog, error) {
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	c, err := h.d.Addons.Catalog(cctx)
	if err != nil {
		h.d.Log.Warn("addons: catalog", "err", err)
	}
	return c, err
}

func (h *handlers) addonsView(ctx context.Context) (AddonsView, error) {
	v := AddonsView{Catalog: []AddonCatalogEntry{}, Installed: []AddonView{}}
	m := h.d.Addons
	if m == nil {
		return v, nil
	}
	st, err := m.State()
	if err != nil {
		return v, err
	}
	v.Supported = m.Supported()
	if l := st.Request; l != nil {
		v.Last = &AddonResult{Action: l.Action, ID: l.ID, State: l.State, Error: l.Error, At: l.At}
	}
	if r, ok := m.Pending(); ok {
		v.Pending = &AddonRequest{Action: r.Action, ID: r.ID, At: r.At}
	}
	if c, err := h.catalog(ctx); err != nil {
		v.CatalogError = "catalog_unavailable"
	} else {
		for _, e := range c.Adapters {
			a, ok := st.Adapters[e.ID]
			v.Catalog = append(v.Catalog, AddonCatalogEntry{ID: e.ID, Name: e.Name, Description: e.Description, Version: e.Version, Homepage: e.Homepage,
				Installed: ok, Update: ok && a.Digest != e.Digest})
		}
	}
	available := h.d.Billing.Available(ctx).Addons
	sub := ""
	if h.d.SubBase != nil {
		sub = h.d.SubBase(ctx)
	}
	for _, id := range slices.Sorted(maps.Keys(st.Adapters)) {
		a := st.Adapters[id]
		av := AddonView{ID: id, Version: a.Version, Status: a.Status, Error: a.Error, Name: map[string]string{}, Help: map[string]string{}, Settings: []AddonField{},
			Available: slices.Contains(available, id), WebhookURL: h.d.Billing.AddonWebhookURL(ctx, id, sub)}
		cfg, err := h.d.Billing.AddonConfig(ctx, id)
		if err != nil {
			return v, err
		}
		av.Enabled = cfg.Enabled
		if a.Status == "running" {
			info, err := m.Info(ctx, id)
			if err != nil {
				h.d.Log.Warn("addons: info", "addon", id, "err", err)
				av.InfoError = "addon_unreachable"
			} else {
				av.Name, av.Help = orEmpty(info.Name), orEmpty(info.Help)
				for _, f := range info.Settings {
					val, set := cfg.Values[f.Key]
					field := AddonField{Key: f.Key, Label: orEmpty(f.Label), Type: f.Type, Secret: f.Secret, Required: f.Required, Pattern: f.Pattern, Set: set}
					if !f.Secret {
						field.Value = val
					}
					av.Settings = append(av.Settings, field)
				}
			}
		}
		v.Installed = append(v.Installed, av)
	}
	return v, nil
}

func orEmpty(t addons.Text) map[string]string {
	if t == nil {
		return map[string]string{}
	}
	return t
}

func (h *handlers) listAddons(ctx context.Context, _ *struct{}) (*addonsOutput, error) {
	v, err := h.addonsView(ctx)
	if err != nil {
		return nil, err
	}
	return &addonsOutput{Body: v}, nil
}

// askErr turns what the manager says into the API's answer.
func askErr(err error) error {
	switch {
	case errors.Is(err, addons.ErrBusy):
		return huma.Error409Conflict("addon_busy")
	case errors.Is(err, addons.ErrUnavailable):
		return huma.Error409Conflict("addons_unavailable")
	}
	return err
}

func (h *handlers) installAddon(ctx context.Context, in *addonIDInput) (*addonsOutput, error) {
	if h.d.Addons == nil {
		return nil, huma.Error409Conflict("addons_unavailable")
	}
	c, err := h.catalog(ctx)
	if err != nil {
		return nil, huma.Error502BadGateway("catalog_unavailable")
	}
	i := slices.IndexFunc(c.Adapters, func(e addons.Entry) bool { return e.ID == in.ID })
	if i < 0 {
		return nil, huma.Error404NotFound("addon_unknown")
	}
	st, err := h.d.Addons.State()
	if err != nil {
		return nil, err
	}
	if a, ok := st.Adapters[in.ID]; ok && a.Status == "running" && a.Digest == c.Adapters[i].Digest {
		return nil, huma.Error409Conflict("addon_current")
	}
	if err := h.d.Addons.Ask("install", in.ID); err != nil {
		return nil, askErr(err)
	}
	h.audit(ctx, sessionOf(ctx).AdminID, "addon.install", "addon", in.ID, map[string]any{"version": c.Adapters[i].Version, "digest": c.Adapters[i].Digest})
	return h.listAddons(ctx, nil)
}

// removeAddon keeps the adapter's settings and its payments: installed again, it takes up
// where it was.
func (h *handlers) removeAddon(ctx context.Context, in *addonIDInput) (*addonsOutput, error) {
	if h.d.Addons == nil {
		return nil, huma.Error409Conflict("addons_unavailable")
	}
	st, err := h.d.Addons.State()
	if err != nil {
		return nil, err
	}
	if _, ok := st.Adapters[in.ID]; !ok {
		return nil, huma.Error404NotFound("addon_not_installed")
	}
	if err := h.d.Addons.Ask("remove", in.ID); err != nil {
		return nil, askErr(err)
	}
	h.audit(ctx, sessionOf(ctx).AdminID, "addon.remove", "addon", in.ID, nil)
	return h.listAddons(ctx, nil)
}

func (h *handlers) patchAddon(ctx context.Context, in *patchAddonInput) (*addonsOutput, error) {
	cfg, err := h.d.Billing.AddonConfig(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	enabled := cfg.Enabled
	if in.Body.Enabled != nil {
		enabled = *in.Body.Enabled
	}
	next, err := h.d.Billing.SetAddonConfig(ctx, in.ID, enabled, in.Body.Settings)
	var se *billing.AddonSettingError
	var ae *addons.Error
	switch {
	case errors.As(err, &se):
		return nil, huma.Error422UnprocessableEntity("validation", &huma.ErrorDetail{Location: "body.settings." + se.Key, Message: "addon_setting_invalid"})
	case errors.As(err, &ae) && ae.Status < 500:
		return nil, huma.Error422UnprocessableEntity("validation", &huma.ErrorDetail{Location: "body.settings", Message: "addon_settings_refused", Value: ae.Code})
	case errors.Is(err, addons.ErrNotInstalled):
		return nil, huma.Error404NotFound("addon_not_installed")
	case errors.Is(err, addons.ErrUnavailable):
		return nil, huma.Error409Conflict("addons_unavailable")
	case errors.As(err, &ae), errors.Is(err, addons.ErrUnreachable):
		h.d.Log.Warn("addons: settings check", "addon", in.ID, "err", err)
		return nil, huma.Error502BadGateway("addon_unreachable")
	case err != nil:
		return nil, err
	}
	// What changed, never the values: some are keys.
	changed := []string{}
	for k := range in.Body.Settings {
		if _, now := next.Values[k]; now {
			changed = append(changed, k)
		} else if _, was := cfg.Values[k]; was {
			changed = append(changed, k)
		}
	}
	slices.Sort(changed)
	h.audit(ctx, sessionOf(ctx).AdminID, "addon.settings", "addon", in.ID, map[string]any{"enabled": enabled, "changed": changed})
	return h.listAddons(ctx, nil)
}
