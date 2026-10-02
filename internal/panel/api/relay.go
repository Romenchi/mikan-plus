package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"mikan/internal/panel/store/db"
)

type RelayView struct {
	Configured  bool     `json:"configured"`
	Enabled     bool     `json:"enabled"`
	Protocol    string   `json:"protocol"`
	Server      string   `json:"server"`
	Port        int      `json:"port"`
	UUID        string   `json:"uuid,omitempty"`
	Flow        string   `json:"flow,omitempty"`
	TLS         bool     `json:"tls"`
	SNI         string   `json:"sni,omitempty"`
	PublicKey   string   `json:"public_key,omitempty"`
	ShortID     string   `json:"short_id,omitempty"`
	SpiderX     string   `json:"spider_x,omitempty"`
	Fingerprint string   `json:"fingerprint,omitempty"`
	Inbounds    []string `json:"inbounds"`
}

type relayOutput struct{ Body RelayView }

type relayInput struct {
	ID   int64 `path:"id" minimum:"1"`
	Body struct {
		Enabled     *bool     `json:"enabled,omitempty"`
		Protocol    string    `json:"protocol,omitempty"`
		Server      string    `json:"server,omitempty"`
		Port        int       `json:"port,omitempty"`
		UUID        string    `json:"uuid,omitempty"`
		Flow        string    `json:"flow,omitempty"`
		TLS         *bool     `json:"tls,omitempty"`
		SNI         string    `json:"sni,omitempty"`
		PublicKey   string    `json:"public_key,omitempty"`
		ShortID     string    `json:"short_id,omitempty"`
		SpiderX     string    `json:"spider_x,omitempty"`
		Fingerprint string    `json:"fingerprint,omitempty"`
		Inbounds    *[]string `json:"inbounds,omitempty"`
		Link        string    `json:"link,omitempty"`
	}
}

func (h *handlers) registerRelay() {
	tags := []string{"node"}
	huma.Register(h.api, huma.Operation{OperationID: "get-node-relay", Method: http.MethodGet, Path: "/api/v1/nodes/{id}/relay", Summary: "Релей ноды (выход через внешний сервер)", Tags: tags}, h.getRelay)
	huma.Register(h.api, huma.Operation{OperationID: "update-node-relay", Method: http.MethodPut, Path: "/api/v1/nodes/{id}/relay", Summary: "Настроить релей ноды", Tags: tags}, h.putRelay)
	huma.Register(h.api, huma.Operation{OperationID: "delete-node-relay", Method: http.MethodDelete, Path: "/api/v1/nodes/{id}/relay", Summary: "Удалить настройки релея ноды", Tags: tags, DefaultStatus: http.StatusNoContent}, h.deleteRelay)
}

func (h *handlers) getRelay(ctx context.Context, input *struct {
	ID int64 `path:"id" minimum:"1"`
}) (*relayOutput, error) {
	if _, err := h.d.Store.Q.GetNode(ctx, input.ID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, huma.Error404NotFound("node not found")
		}
		return nil, huma.Error500InternalServerError(err.Error())
	}
	r, err := h.d.Store.Q.GetNodeRelay(ctx, input.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return &relayOutput{Body: RelayView{Configured: false, Inbounds: []string{}}}, nil
	}
	if err != nil {
		return nil, huma.Error500InternalServerError(err.Error())
	}
	var inNames []string
	if r.Inbounds != "" {
		_ = json.Unmarshal([]byte(r.Inbounds), &inNames)
	}
	if inNames == nil {
		inNames = []string{}
	}
	return &relayOutput{Body: RelayView{
		Configured:  true,
		Enabled:     r.Enabled != 0,
		Protocol:    r.Protocol,
		Server:      r.Server,
		Port:        int(r.Port),
		UUID:        r.Uuid,
		Flow:        r.Flow,
		TLS:         r.Tls != 0,
		SNI:         r.Sni,
		PublicKey:   r.PublicKey,
		ShortID:     r.ShortID,
		SpiderX:     r.SpiderX,
		Fingerprint: r.Fingerprint,
		Inbounds:    inNames,
	}}, nil
}

func parseVlessLink(raw string) (server, uuid, flow, sni, pbk, sid, spx, fp string, port int, ok bool) {
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(raw, "vless://") {
		return
	}
	u, err := url.Parse(raw)
	if err != nil {
		return
	}
	uuid = u.User.Username()
	host := u.Hostname()
	portStr := u.Port()
	if portStr == "" {
		port = 443
	} else {
		p, err := strconv.Atoi(portStr)
		if err == nil && p > 0 && p <= 65535 {
			port = p
		} else {
			port = 443
		}
	}
	server = host
	q := u.Query()
	flow = q.Get("flow")
	sni = q.Get("sni")
	pbk = q.Get("pbk")
	sid = q.Get("sid")
	spx = q.Get("spx")
	fp = q.Get("fp")
	ok = server != "" && uuid != ""
	return
}

func (h *handlers) putRelay(ctx context.Context, input *relayInput) (*relayOutput, error) {
	if _, err := h.d.Store.Q.GetNode(ctx, input.ID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, huma.Error404NotFound("node not found")
		}
		return nil, huma.Error500InternalServerError(err.Error())
	}

	b := input.Body
	existing, err := h.d.Store.Q.GetNodeRelay(ctx, input.ID)
	hasExisting := err == nil

	enabled := int64(1)
	if b.Enabled != nil && !*b.Enabled {
		enabled = 0
	} else if b.Enabled == nil && hasExisting {
		enabled = existing.Enabled
	}

	protocol := b.Protocol
	if protocol == "" {
		if hasExisting && existing.Protocol != "" {
			protocol = existing.Protocol
		} else {
			protocol = "vless"
		}
	}

	server := b.Server
	port := int64(b.Port)
	uuid := b.UUID
	flow := b.Flow
	tls := int64(1)
	if b.TLS != nil && !*b.TLS {
		tls = 0
	}
	sni := b.SNI
	pbk := b.PublicKey
	sid := b.ShortID
	spx := b.SpiderX
	fp := b.Fingerprint
	if fp == "" {
		fp = "firefox"
	}

	// If link is provided, parse it
	if b.Link != "" {
		if s, uid, fl, sn, pk, sd, sx, fprint, p, parsed := parseVlessLink(b.Link); parsed {
			server = s
			port = int64(p)
			uuid = uid
			if fl != "" {
				flow = fl
			}
			if sn != "" {
				sni = sn
			}
			if pk != "" {
				pbk = pk
			}
			if sd != "" {
				sid = sd
			}
			if sx != "" {
				spx = sx
			}
			if fprint != "" {
				fp = fprint
			}
		}
	} else if server == "" && hasExisting {
		server = existing.Server
		port = existing.Port
		uuid = existing.Uuid
		flow = existing.Flow
		tls = existing.Tls
		sni = existing.Sni
		pbk = existing.PublicKey
		sid = existing.ShortID
		spx = existing.SpiderX
		fp = existing.Fingerprint
	}

	var inboundsJSON string
	if b.Inbounds != nil {
		raw, _ := json.Marshal(*b.Inbounds)
		inboundsJSON = string(raw)
	} else if hasExisting {
		inboundsJSON = existing.Inbounds
	} else {
		inboundsJSON = "[]"
	}

	now := time.Now().Unix()
	created := now
	if hasExisting {
		created = existing.CreatedAt
	}

	if err := h.d.Store.Q.SaveNodeRelay(ctx, db.SaveNodeRelayParams{
		NodeID:      input.ID,
		Enabled:     enabled,
		Protocol:    protocol,
		Server:      server,
		Port:        port,
		Uuid:        uuid,
		Flow:        flow,
		Tls:         tls,
		Sni:         sni,
		PublicKey:   pbk,
		ShortID:     sid,
		SpiderX:     spx,
		Fingerprint: fp,
		Inbounds:    inboundsJSON,
		CreatedAt:   created,
		UpdatedAt:   now,
	}); err != nil {
		return nil, huma.Error500InternalServerError(err.Error())
	}

	h.d.Nodes.MarkDirty()

	var inNames []string
	_ = json.Unmarshal([]byte(inboundsJSON), &inNames)
	if inNames == nil {
		inNames = []string{}
	}

	return &relayOutput{Body: RelayView{
		Configured:  true,
		Enabled:     enabled != 0,
		Protocol:    protocol,
		Server:      server,
		Port:        int(port),
		UUID:        uuid,
		Flow:        flow,
		TLS:         tls != 0,
		SNI:         sni,
		PublicKey:   pbk,
		ShortID:     sid,
		SpiderX:     spx,
		Fingerprint: fp,
		Inbounds:    inNames,
	}}, nil
}

func (h *handlers) deleteRelay(ctx context.Context, input *struct {
	ID int64 `path:"id" minimum:"1"`
}) (*struct{}, error) {
	if err := h.d.Store.Q.DeleteNodeRelay(ctx, input.ID); err != nil {
		return nil, huma.Error500InternalServerError(err.Error())
	}
	h.d.Nodes.MarkDirty()
	return nil, nil
}
