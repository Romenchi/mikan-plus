package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"mikan/internal/panel/domain"
	"mikan/internal/panel/store/db"
)

type TariffView struct {
	ID            int64       `json:"id"`
	Name          string      `json:"name"`
	TrafficLimit  *int64      `json:"traffic_limit" doc:"Байты; null — без лимита"`
	DurationDays  int64       `json:"duration_days" doc:"0 — бессрочно"`
	DeviceLimit   *int64      `json:"device_limit"`
	ResetStrategy string      `json:"reset_strategy" enum:"none,month_start,period"`
	BillingDay    *int64      `json:"billing_day" doc:"День месяца, в который заканчивается срок; null — срок в днях"`
	PriceLabel    string      `json:"price_label"`
	PriceStars    *int64      `json:"price_stars" doc:"Цена в Telegram Stars; null — не продаётся за Stars"`
	PriceRub      *int64      `json:"price_rub" doc:"Цена в копейках (ЮKassa, CryptoBot); null — не продаётся за рубли"`
	OnSale        bool        `json:"on_sale" doc:"Продаётся в боте и Mini App"`
	Pools         []PoolLimit `json:"pools" doc:"Лимиты пулов трафика; пул не в списке — без лимита"`
	Sort          int64       `json:"sort"`
}

func viewTariff(t db.Tariff) TariffView {
	return TariffView{ID: t.ID, Name: t.Name, TrafficLimit: ptrInt(t.TrafficLimit.Int64, t.TrafficLimit.Valid),
		DurationDays: t.DurationDays, DeviceLimit: ptrInt(t.DeviceLimit.Int64, t.DeviceLimit.Valid),
		ResetStrategy: t.ResetStrategy, BillingDay: ptrInt(t.BillingDay.Int64, t.BillingDay.Valid), PriceLabel: t.PriceLabel, Sort: t.Sort,
		PriceStars: ptrInt(t.PriceStars.Int64, t.PriceStars.Valid), PriceRub: ptrInt(t.PriceRub.Int64, t.PriceRub.Valid), OnSale: t.OnSale != 0}
}

type tariffBody struct {
	Name          string      `json:"name" minLength:"1" maxLength:"60"`
	TrafficLimit  *int64      `json:"traffic_limit,omitempty" minimum:"1"`
	DurationDays  int64       `json:"duration_days" minimum:"0" maximum:"3650"`
	DeviceLimit   *int64      `json:"device_limit,omitempty" minimum:"1" maximum:"100"`
	ResetStrategy string      `json:"reset_strategy" enum:"none,month_start,period" default:"none"`
	BillingDay    *int64      `json:"billing_day,omitempty" minimum:"1" maximum:"31" doc:"Срок до этого числа месяца: месяц = от дня оплаты до дня оплаты"`
	PriceLabel    string      `json:"price_label,omitempty" maxLength:"40"`
	PriceStars    *int64      `json:"price_stars,omitempty" minimum:"1" maximum:"10000" doc:"Цена в Telegram Stars"`
	PriceRub      *int64      `json:"price_rub,omitempty" minimum:"100" maximum:"100000000" doc:"Цена в копейках: 19900 — 199 ₽"`
	OnSale        bool        `json:"on_sale,omitempty" doc:"Продавать в боте и Mini App; нужна хотя бы одна цена"`
	Pools         []PoolLimit `json:"pools,omitempty" maxItems:"100" doc:"Лимиты пулов трафика; не передан — без изменений"`
	Sort          int64       `json:"sort,omitempty"`
}

type tariffInput struct{ Body tariffBody }
type tariffUpdateInput struct {
	ID   int64 `path:"id" minimum:"1"`
	Body tariffBody
}
type tariffOutput struct{ Body TariffView }
type tariffsOutput struct{ Body []TariffView }

func (h *handlers) registerCatalog() {
	huma.Register(h.api, huma.Operation{OperationID: "list-tariffs", Method: http.MethodGet, Path: "/api/v1/tariffs", Summary: "Тарифы", Tags: []string{"tariffs"}}, h.listTariffs)
	huma.Register(h.api, huma.Operation{OperationID: "create-tariff", Method: http.MethodPost, Path: "/api/v1/tariffs", Summary: "Создать тариф", Tags: []string{"tariffs"}, DefaultStatus: http.StatusCreated}, h.createTariff)
	huma.Register(h.api, huma.Operation{OperationID: "update-tariff", Method: http.MethodPut, Path: "/api/v1/tariffs/{id}", Summary: "Изменить тариф", Tags: []string{"tariffs"}}, h.updateTariff)
	huma.Register(h.api, huma.Operation{OperationID: "archive-tariff", Method: http.MethodDelete, Path: "/api/v1/tariffs/{id}", Summary: "Убрать тариф в архив", Tags: []string{"tariffs"}, DefaultStatus: http.StatusNoContent}, h.archiveTariff)
}

func (h *handlers) listTariffs(ctx context.Context, _ *struct{}) (*tariffsOutput, error) {
	rows, err := h.d.Store.Q.ListTariffs(ctx)
	if err != nil {
		return nil, err
	}
	pools, err := h.d.Store.Q.ListAllTariffPools(ctx)
	if err != nil {
		return nil, err
	}
	out := &tariffsOutput{Body: make([]TariffView, 0, len(rows))}
	for _, t := range rows {
		v := viewTariff(t)
		v.Pools = tariffPoolsOf(pools, t.ID)
		out.Body = append(out.Body, v)
	}
	return out, nil
}

func nullable(p *int64) sql.NullInt64 {
	if p == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: *p, Valid: true}
}

// createTariff and updateTariff write the tariff and its pool limits in one transaction: a
// refused pool list leaves the tariff, and the limits it had, as they were.
func (h *handlers) createTariff(ctx context.Context, in *tariffInput) (*tariffOutput, error) {
	b := in.Body
	if err := b.check(); err != nil {
		return nil, err
	}
	var t db.Tariff
	err := h.d.Store.Tx(ctx, func(q *db.Queries) error {
		var err error
		t, err = q.CreateTariff(ctx, db.CreateTariffParams{Name: strings.TrimSpace(b.Name), TrafficLimit: nullable(b.TrafficLimit),
			DurationDays: b.DurationDays, DeviceLimit: nullable(b.DeviceLimit), ResetStrategy: b.ResetStrategy, PriceLabel: b.PriceLabel,
			Sort: b.Sort, CreatedAt: h.d.Now().Unix(), BillingDay: nullable(b.BillingDay), PriceStars: nullable(b.PriceStars), PriceRub: nullable(b.PriceRub), OnSale: domain.Flag(b.OnSale)})
		if err != nil || b.Pools == nil {
			return err
		}
		return setTariffPools(ctx, q, t.ID, b.Pools)
	})
	if err != nil {
		return nil, err
	}
	h.audit(ctx, sessionOf(ctx).AdminID, "tariff.create", "tariff", strconv.FormatInt(t.ID, 10), nil)
	return h.tariffOut(ctx, t)
}

func (h *handlers) updateTariff(ctx context.Context, in *tariffUpdateInput) (*tariffOutput, error) {
	b := in.Body
	if err := b.check(); err != nil {
		return nil, err
	}
	var t db.Tariff
	err := h.d.Store.Tx(ctx, func(q *db.Queries) error {
		var err error
		t, err = q.UpdateTariff(ctx, db.UpdateTariffParams{Name: strings.TrimSpace(b.Name), TrafficLimit: nullable(b.TrafficLimit),
			DurationDays: b.DurationDays, DeviceLimit: nullable(b.DeviceLimit), ResetStrategy: b.ResetStrategy, PriceLabel: b.PriceLabel,
			Sort: b.Sort, BillingDay: nullable(b.BillingDay), PriceStars: nullable(b.PriceStars), PriceRub: nullable(b.PriceRub), OnSale: domain.Flag(b.OnSale), ID: in.ID})
		if err != nil || b.Pools == nil {
			return err
		}
		return setTariffPools(ctx, q, t.ID, b.Pools)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, huma.Error404NotFound("not_found")
	}
	if err != nil {
		return nil, err
	}
	h.audit(ctx, sessionOf(ctx).AdminID, "tariff.update", "tariff", strconv.FormatInt(t.ID, 10), nil)
	return h.tariffOut(ctx, t)
}

func (h *handlers) archiveTariff(ctx context.Context, in *userIDInput) (*struct{}, error) {
	n, err := h.d.Store.Q.ArchiveTariff(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, huma.Error404NotFound("not_found")
	}
	h.audit(ctx, sessionOf(ctx).AdminID, "tariff.archive", "tariff", strconv.FormatInt(in.ID, 10), nil)
	return nil, nil
}

// check: a tariff on sale needs a price to sell it for.
func (b tariffBody) check() error {
	if b.OnSale && b.PriceStars == nil && b.PriceRub == nil {
		return huma.Error422UnprocessableEntity("validation", &huma.ErrorDetail{Location: "body.on_sale", Message: "on_sale_no_price"})
	}
	return nil
}

func (h *handlers) tariffOut(ctx context.Context, t db.Tariff) (*tariffOutput, error) {
	pools, err := h.d.Store.Q.ListTariffPools(ctx, t.ID)
	if err != nil {
		return nil, err
	}
	v := viewTariff(t)
	v.Pools = tariffPoolsOf(pools, t.ID)
	return &tariffOutput{Body: v}, nil
}
