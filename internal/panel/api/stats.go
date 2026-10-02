package api

import (
	"context"
	"net/http"
	"slices"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"mikan/internal/nodeapi"
	"mikan/internal/panel/domain"
	"mikan/internal/panel/store/db"
)

type TopUser struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Bytes int64  `json:"bytes"`
}

type overviewOutput struct {
	Body struct {
		Online        int       `json:"online"`
		UsersTotal    int       `json:"users_total"`
		UsersActive   int       `json:"users_active"`
		Expiring7d    int       `json:"expiring_7d"`
		TrafficToday  int64     `json:"traffic_today"`
		TrafficYest   int64     `json:"traffic_yesterday"`
		Top           []TopUser `json:"top_month"`
		SlotsFree     int64     `json:"slots_free"`
		SlotsAssigned int64     `json:"slots_assigned"`
		GeneratedAt   time.Time `json:"generated_at"`
	}
}

type rangeInput struct {
	Range string `query:"range" enum:"24h,7d,30d" default:"24h"`
}

type NodeView struct {
	OK        bool                     `json:"ok"`
	Error     string                   `json:"error,omitempty"`
	Version   string                   `json:"version"`
	Core      string                   `json:"core"`
	StartedAt *time.Time               `json:"started_at,omitempty"`
	Conns     int                      `json:"conns"`
	System    nodeapi.System           `json:"system"`
	Listeners []nodeapi.ListenerStatus `json:"listeners"`
	CheckedAt time.Time                `json:"checked_at"`
}

type nodeOutput struct{ Body NodeView }

func (h *handlers) registerStats() {
	huma.Register(h.api, huma.Operation{OperationID: "stats-overview", Method: http.MethodGet, Path: "/api/v1/stats/overview", Summary: "Показатели дашборда", Tags: []string{"stats"}}, h.overview)
	huma.Register(h.api, huma.Operation{OperationID: "stats-traffic", Method: http.MethodGet, Path: "/api/v1/stats/traffic", Summary: "Трафик сервера", Tags: []string{"stats"}}, h.serverTraffic)
	huma.Register(h.api, huma.Operation{OperationID: "node-health", Method: http.MethodGet, Path: "/api/v1/node", Summary: "Состояние ноды", Tags: []string{"node"}}, h.nodeHealth)
}

func (h *handlers) overview(ctx context.Context, _ *struct{}) (*overviewOutput, error) {
	now := h.d.Now()
	users, err := h.d.Store.Q.ListUsers(ctx)
	if err != nil {
		return nil, err
	}
	out := &overviewOutput{}
	b := &out.Body
	b.GeneratedAt = now.UTC()
	b.UsersTotal = len(users)
	grants, err := domain.LoadGrantsLeft(ctx, h.d.Store.Q, now)
	if err != nil {
		return nil, err
	}
	for _, u := range users {
		switch domain.State(u, grants.Main(u.ID), now) {
		case domain.StateActive:
			b.UsersActive++
		case domain.StateExpiring:
			b.UsersActive++
			b.Expiring7d++
		}
	}
	if h.d.Online != nil {
		// Online counts users: one with several bound devices has several slots online.
		slots, err := h.allUserSlots(ctx)
		if err != nil {
			return nil, err
		}
		online := h.d.Online()
		for _, names := range slots {
			if slices.ContainsFunc(names, func(n string) bool { _, ok := online[n]; return ok }) {
				b.Online++
			}
		}
	}
	today := now.Unix() / 86400
	days, err := h.d.Store.Q.TotalTrafficDaily(ctx, today-1)
	if err != nil {
		return nil, err
	}
	for _, d := range days {
		switch d.Day {
		case today:
			b.TrafficToday = d.Up + d.Down
		case today - 1:
			b.TrafficYest = d.Up + d.Down
		}
	}
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).Unix() / 86400
	top, err := h.d.Store.Q.TopUsersByTraffic(ctx, db.TopUsersByTrafficParams{Day: monthStart, Limit: 5})
	if err != nil {
		return nil, err
	}
	b.Top = make([]TopUser, 0, len(top))
	for _, t := range top {
		b.Top = append(b.Top, TopUser{ID: t.ID, Name: t.Name, Bytes: t.Bytes})
	}
	if h.d.Pool != nil {
		ps, err := h.d.Pool.Stats(ctx)
		if err != nil {
			return nil, err
		}
		b.SlotsFree, b.SlotsAssigned = ps.Free, ps.Assigned
	}
	return out, nil
}

func (h *handlers) serverTraffic(ctx context.Context, in *rangeInput) (*trafficOutput, error) {
	now := h.d.Now()
	out := &trafficOutput{}
	out.Body.Points = []TrafficPoint{}
	if in.Range == "30d" {
		rows, err := h.d.Store.Q.TotalTrafficDaily(ctx, now.Add(-30*24*time.Hour).Unix()/86400)
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			out.Body.Points = append(out.Body.Points, TrafficPoint{T: time.Unix(r.Day*86400, 0).UTC(), Up: r.Up, Down: r.Down})
		}
		return out, nil
	}
	span := 24 * time.Hour
	if in.Range == "7d" {
		span = 7 * 24 * time.Hour
	}
	rows, err := h.d.Store.Q.TotalTrafficHourly(ctx, now.Add(-span).Unix()/3600)
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		out.Body.Points = append(out.Body.Points, TrafficPoint{T: time.Unix(r.Hour*3600, 0).UTC(), Up: r.Up, Down: r.Down})
	}
	return out, nil
}

type nodeHealthInput struct {
	ID int64 `query:"id" default:"1" minimum:"1" doc:"Нода; 1 — своя нода панели"`
}

func (h *handlers) nodeHealth(ctx context.Context, in *nodeHealthInput) (*nodeOutput, error) {
	out := &nodeOutput{}
	if h.d.Nodes == nil {
		out.Body = NodeView{Error: "node sync disabled", Listeners: []nodeapi.ListenerStatus{}}
		return out, nil
	}
	hv, ok := h.d.Nodes.Health(in.ID)
	if !ok {
		hv.Error = "not connected"
	}
	v := NodeView{OK: hv.OK, Error: hv.Error, Version: hv.Health.Version, Core: hv.Health.Core, Conns: hv.Health.Conns,
		System: hv.Health.System, Listeners: hv.Listeners, CheckedAt: hv.CheckedAt}
	if !hv.Health.StartedAt.IsZero() {
		t := hv.Health.StartedAt
		v.StartedAt = &t
	}
	if v.Listeners == nil {
		v.Listeners = []nodeapi.ListenerStatus{}
	}
	out.Body = v
	return out, nil
}
