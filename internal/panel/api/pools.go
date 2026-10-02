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
	"mikan/internal/panel/store"
	"mikan/internal/panel/store/db"
)

// Traffic pools (GitHub issue #6): chosen inbounds count to a pool with its own limit per
// user, apart from the main quota.

type PoolView struct {
	ID       int64    `json:"id"`
	Name     string   `json:"name"`
	Inbounds []string `json:"inbounds" doc:"Подключения, которые считаются в этот пул"`
}

type poolsOutput struct{ Body []PoolView }
type poolOutput struct{ Body PoolView }

type poolInput struct {
	Body struct {
		Name string `json:"name" minLength:"1" maxLength:"40"`
	}
}

type poolPatchInput struct {
	ID   int64 `path:"id" minimum:"1"`
	Body struct {
		Name string `json:"name" minLength:"1" maxLength:"40"`
	}
}

// UserPoolView is one pool of a user: the limit and what is used this period.
type UserPoolView struct {
	PoolID       int64  `json:"pool_id"`
	Name         string `json:"name"`
	TrafficLimit *int64 `json:"traffic_limit" doc:"Байты за период; null — без лимита"`
	UsedUp       int64  `json:"used_up"`
	UsedDown     int64  `json:"used_down"`
	Extra        int64  `json:"extra" doc:"Байты, оставшиеся в пакетах трафика пула: тратятся после лимита"`
	Exhausted    bool   `json:"exhausted" doc:"Лимит пула и его пакеты исчерпаны: подключения пула не работают до сброса"`
}

type userPoolsOutput struct{ Body []UserPoolView }

type PoolLimit struct {
	PoolID       int64  `json:"pool_id" minimum:"1"`
	TrafficLimit *int64 `json:"traffic_limit" minimum:"1" doc:"Байты; null — без лимита"`
}

type userPoolsInput struct {
	ID   int64 `path:"id" minimum:"1"`
	Body struct {
		Pools []PoolLimit `json:"pools" maxItems:"100"`
	}
}

func (h *handlers) registerPools() {
	tags := []string{"inbounds"}
	huma.Register(h.api, huma.Operation{OperationID: "list-pools", Method: http.MethodGet, Path: "/api/v1/pools", Summary: "Пулы трафика", Tags: tags}, h.listPools)
	huma.Register(h.api, huma.Operation{OperationID: "create-pool", Method: http.MethodPost, Path: "/api/v1/pools", Summary: "Создать пул трафика", Tags: tags, DefaultStatus: http.StatusCreated}, h.createPool)
	huma.Register(h.api, huma.Operation{OperationID: "rename-pool", Method: http.MethodPatch, Path: "/api/v1/pools/{id}", Summary: "Переименовать пул", Tags: tags}, h.renamePool)
	huma.Register(h.api, huma.Operation{OperationID: "delete-pool", Method: http.MethodDelete, Path: "/api/v1/pools/{id}", Summary: "Удалить пул: его подключения вернутся в основной трафик; пока в нём есть оплаченный трафик, пул остаётся", Tags: tags, DefaultStatus: http.StatusNoContent}, h.deletePool)
	huma.Register(h.api, huma.Operation{OperationID: "user-pools", Method: http.MethodGet, Path: "/api/v1/users/{id}/pools", Summary: "Пулы трафика пользователя", Tags: []string{"users"}}, h.userPools)
	huma.Register(h.api, huma.Operation{OperationID: "set-user-pools", Method: http.MethodPut, Path: "/api/v1/users/{id}/pools", Summary: "Лимиты пулов пользователя", Tags: []string{"users"}}, h.setUserPools)
}

func (h *handlers) pools(ctx context.Context) ([]PoolView, error) {
	ps, err := h.d.Store.Q.ListTrafficPools(ctx)
	if err != nil {
		return nil, err
	}
	ins, err := h.d.Store.Q.ListInbounds(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]PoolView, 0, len(ps))
	for _, p := range ps {
		v := PoolView{ID: p.ID, Name: p.Name, Inbounds: []string{}}
		for _, in := range ins {
			if in.PoolID.Valid && in.PoolID.Int64 == p.ID {
				v.Inbounds = append(v.Inbounds, in.Name)
			}
		}
		out = append(out, v)
	}
	return out, nil
}

func (h *handlers) listPools(ctx context.Context, _ *struct{}) (*poolsOutput, error) {
	ps, err := h.pools(ctx)
	if err != nil {
		return nil, err
	}
	return &poolsOutput{Body: ps}, nil
}

func poolNameError(err error) error {
	if store.IsUnique(err) {
		return huma.Error409Conflict("pool_name_taken", &huma.ErrorDetail{Location: "body.name", Message: "pool_name_taken"})
	}
	return err
}

func (h *handlers) createPool(ctx context.Context, in *poolInput) (*poolOutput, error) {
	name := strings.TrimSpace(in.Body.Name)
	if name == "" {
		return nil, huma.Error422UnprocessableEntity("validation", &huma.ErrorDetail{Location: "body.name", Message: "name_blank"})
	}
	p, err := h.d.Store.Q.CreateTrafficPool(ctx, db.CreateTrafficPoolParams{Name: name, CreatedAt: h.d.Now().Unix()})
	if err != nil {
		return nil, poolNameError(err)
	}
	h.audit(ctx, sessionOf(ctx).AdminID, "pool.create", "pool", strconv.FormatInt(p.ID, 10), map[string]any{"name": name})
	return &poolOutput{Body: PoolView{ID: p.ID, Name: p.Name, Inbounds: []string{}}}, nil
}

func (h *handlers) renamePool(ctx context.Context, in *poolPatchInput) (*poolOutput, error) {
	name := strings.TrimSpace(in.Body.Name)
	if name == "" {
		return nil, huma.Error422UnprocessableEntity("validation", &huma.ErrorDetail{Location: "body.name", Message: "name_blank"})
	}
	n, err := h.d.Store.Q.RenameTrafficPool(ctx, db.RenameTrafficPoolParams{Name: name, ID: in.ID})
	if err != nil {
		return nil, poolNameError(err)
	}
	if n == 0 {
		return nil, huma.Error404NotFound("not_found")
	}
	ps, err := h.pools(ctx)
	if err != nil {
		return nil, err
	}
	for _, p := range ps {
		if p.ID == in.ID {
			return &poolOutput{Body: p}, nil
		}
	}
	return nil, huma.Error404NotFound("not_found")
}

// deletePool refuses while the pool holds what users paid for: deleting it would cascade
// to their grants and to the catalog's packages, and a paid invoice of a package would
// lose it. The traffic stays theirs; the admin waits for it to run out, or archives the
// packages first.
func (h *handlers) deletePool(ctx context.Context, in *userIDInput) (*struct{}, error) {
	err := h.d.Store.Tx(ctx, func(q *db.Queries) error {
		use, err := q.PoolUsage(ctx, db.PoolUsageParams{PoolID: sql.NullInt64{Int64: in.ID, Valid: true}, Now: h.d.Now().Unix()})
		if err != nil {
			return err
		}
		var details []error
		for _, c := range []struct {
			n    int64
			code string
		}{{use.Grants, "pool_has_grants"}, {use.Packages, "pool_has_packages"}, {use.Payments, "pool_has_payments"}} {
			if c.n > 0 {
				details = append(details, &huma.ErrorDetail{Location: "path.id", Message: c.code, Value: c.n})
			}
		}
		if len(details) > 0 {
			return huma.Error409Conflict("pool_in_use", details...)
		}
		n, err := q.DeleteTrafficPool(ctx, in.ID)
		if err != nil {
			return err
		}
		if n == 0 {
			return huma.Error404NotFound("not_found")
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	// Its inbounds count to the main quota again; nodes and policies must know.
	h.d.Changes.SlotsChanged()
	h.d.Changes.PoliciesChanged()
	h.audit(ctx, sessionOf(ctx).AdminID, "pool.delete", "pool", strconv.FormatInt(in.ID, 10), nil)
	return nil, nil
}

func (h *handlers) userPools(ctx context.Context, in *userIDInput) (*userPoolsOutput, error) {
	if _, err := h.d.Store.Q.GetUser(ctx, in.ID); errors.Is(err, sql.ErrNoRows) {
		return nil, huma.Error404NotFound("not_found")
	} else if err != nil {
		return nil, err
	}
	ps, err := h.d.Store.Q.ListTrafficPools(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := h.d.Store.Q.ListUserPools(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	mine := map[int64]db.UserPool{}
	for _, r := range rows {
		mine[r.PoolID] = r
	}
	grants, err := domain.UserGrantsLeft(ctx, h.d.Store.Q, in.ID, h.d.Now())
	if err != nil {
		return nil, err
	}
	out := &userPoolsOutput{Body: make([]UserPoolView, 0, len(ps))}
	for _, p := range ps {
		r := mine[p.ID]
		v := UserPoolView{PoolID: p.ID, Name: p.Name, TrafficLimit: ptrInt(r.TrafficLimit.Int64, r.TrafficLimit.Valid), UsedUp: r.UsedUp, UsedDown: r.UsedDown,
			Extra: grants.Pool(in.ID, p.ID)}
		v.Exhausted = domain.PoolExhausted(r, v.Extra)
		out.Body = append(out.Body, v)
	}
	return out, nil
}

func (h *handlers) setUserPools(ctx context.Context, in *userPoolsInput) (*userPoolsOutput, error) {
	if _, err := h.d.Store.Q.GetUser(ctx, in.ID); errors.Is(err, sql.ErrNoRows) {
		return nil, huma.Error404NotFound("not_found")
	} else if err != nil {
		return nil, err
	}
	err := h.d.Store.Tx(ctx, func(q *db.Queries) error {
		for _, p := range in.Body.Pools {
			if _, err := q.GetTrafficPool(ctx, p.PoolID); errors.Is(err, sql.ErrNoRows) {
				return huma.Error422UnprocessableEntity("validation", &huma.ErrorDetail{Location: "body.pools", Message: "pool_not_found", Value: p.PoolID})
			} else if err != nil {
				return err
			}
			limit := sql.NullInt64{}
			if p.TrafficLimit != nil {
				limit = sql.NullInt64{Int64: *p.TrafficLimit, Valid: true}
			}
			if err := q.SetUserPoolLimit(ctx, db.SetUserPoolLimitParams{UserID: in.ID, PoolID: p.PoolID, TrafficLimit: limit}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	h.d.Changes.PoliciesChanged()
	h.audit(ctx, sessionOf(ctx).AdminID, "user.pools", "user", strconv.FormatInt(in.ID, 10), nil)
	return h.userPools(ctx, &userIDInput{ID: in.ID})
}

// setTariffPools replaces a tariff's pool limits on q's transaction. The list is checked
// whole (a pool twice, an unknown pool) before the old limits go.
func setTariffPools(ctx context.Context, q *db.Queries, tariffID int64, limits []PoolLimit) error {
	seen := make(map[int64]bool, len(limits))
	for _, p := range limits {
		if seen[p.PoolID] {
			return huma.Error422UnprocessableEntity("validation", &huma.ErrorDetail{Location: "body.pools", Message: "pool_duplicate", Value: p.PoolID})
		}
		seen[p.PoolID] = true
		if _, err := q.GetTrafficPool(ctx, p.PoolID); errors.Is(err, sql.ErrNoRows) {
			return huma.Error422UnprocessableEntity("validation", &huma.ErrorDetail{Location: "body.pools", Message: "pool_not_found", Value: p.PoolID})
		} else if err != nil {
			return err
		}
	}
	if err := q.ClearTariffPools(ctx, tariffID); err != nil {
		return err
	}
	for _, p := range limits {
		if p.TrafficLimit == nil {
			continue // unlimited: no row
		}
		if err := q.AddTariffPool(ctx, db.AddTariffPoolParams{TariffID: tariffID, PoolID: p.PoolID, TrafficLimit: *p.TrafficLimit}); err != nil {
			return err
		}
	}
	return nil
}

// tariffPoolsOf lists a tariff's pool limits for its view.
func tariffPoolsOf(all []db.TariffPool, tariffID int64) []PoolLimit {
	out := []PoolLimit{}
	for _, p := range all {
		if p.TariffID == tariffID {
			l := p.TrafficLimit
			out = append(out, PoolLimit{PoolID: p.PoolID, TrafficLimit: &l})
		}
	}
	return out
}
