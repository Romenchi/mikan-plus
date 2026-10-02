package api

import (
	"context"
	"database/sql"
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"mikan/internal/hostname"
	"mikan/internal/panel/domain"
	"mikan/internal/panel/settings"
	"mikan/internal/panel/store/db"
	"mikan/internal/panel/subs"
	"mikan/internal/panel/tgbot"
	"mikan/internal/release"
)

type NodeInfo struct {
	ID          int64      `json:"id"`
	Name        string     `json:"name" doc:"Группа в подписке, например «🇳🇱 Нидерланды»; её флаг — префикс имён подключений"`
	Local       bool       `json:"local" doc:"Своя нода панели"`
	Address     string     `json:"address" doc:"host:port API ноды; пусто у своей ноды"`
	Host        string     `json:"host" doc:"Адрес для клиентов"`
	Domain      string     `json:"domain"`
	Enabled     bool       `json:"enabled"`
	Inbounds    int        `json:"inbounds"`
	Status      string     `json:"status" enum:"ok,error,unknown"`
	Error       string     `json:"error,omitempty"`
	Version     string     `json:"version,omitempty"`
	Listeners   int        `json:"listeners"`
	ListenersOK int        `json:"listeners_ok"`
	Conns       int        `json:"conns"`
	CPUPercent  float64    `json:"cpu_percent"`
	MemUsed     uint64     `json:"mem_used"`
	MemTotal    uint64     `json:"mem_total"`
	CheckedAt   *time.Time `json:"checked_at,omitempty"`
	// Certificate is the node's own one for its protocols on the node's TLS; nil: the
	// node uses its self-signed certificate.
	Certificate *NodeCertView `json:"certificate,omitempty"`
}

type nodesOutput struct{ Body []NodeInfo }
type nodeInfoOutput struct{ Body NodeInfo }

type nodeKeyOutput struct {
	Body struct {
		Node    NodeInfo `json:"node"`
		Key     string   `json:"key" doc:"Ключ подключения ноды: показывается один раз"`
		Command string   `json:"command" doc:"Команда установки на сервере ноды"`
	}
}

type createNodeInput struct {
	Body struct {
		Name    string `json:"name" maxLength:"200"`
		Host    string `json:"host" maxLength:"253" doc:"IP или имя сервера ноды"`
		Domain  string `json:"domain,omitempty" maxLength:"253" doc:"Домен для Hysteria2/TUIC и ссылок (необязательно)"`
		APIPort int    `json:"api_port,omitempty" minimum:"1" maximum:"65535" doc:"Порт API ноды; по умолчанию случайный"`
	}
}

type patchNodeInput struct {
	ID   int64 `path:"id" minimum:"1"`
	Body struct {
		Name    *string `json:"name,omitempty" maxLength:"200"`
		Host    *string `json:"host,omitempty" maxLength:"253"`
		Domain  *string `json:"domain,omitempty" maxLength:"253"`
		Enabled *bool   `json:"enabled,omitempty"`
	}
}

type nodeIDInput struct {
	ID int64 `path:"id" minimum:"1"`
}

func (h *handlers) registerNodes() {
	huma.Register(h.api, huma.Operation{OperationID: "list-nodes", Method: http.MethodGet, Path: "/api/v1/nodes", Summary: "Ноды", Tags: []string{"node"}}, h.listNodes)
	huma.Register(h.api, huma.Operation{OperationID: "create-node", Metadata: sessionOnly, Extensions: sessionOnlyExt, Method: http.MethodPost, Path: "/api/v1/nodes", Summary: "Добавить ноду", Tags: []string{"node"}, DefaultStatus: http.StatusCreated}, h.createNode)
	huma.Register(h.api, huma.Operation{OperationID: "update-node", Metadata: sessionOnly, Extensions: sessionOnlyExt, Method: http.MethodPatch, Path: "/api/v1/nodes/{id}", Summary: "Изменить ноду", Tags: []string{"node"}}, h.updateNode)
	huma.Register(h.api, huma.Operation{OperationID: "rekey-node", Metadata: sessionOnly, Extensions: sessionOnlyExt, Method: http.MethodPost, Path: "/api/v1/nodes/{id}/key", Summary: "Выпустить новый ключ ноды (старый перестаёт работать)", Tags: []string{"node"}}, h.rekeyNode)
	huma.Register(h.api, huma.Operation{OperationID: "delete-node", Method: http.MethodDelete, Path: "/api/v1/nodes/{id}", Summary: "Удалить ноду", Tags: []string{"node"}, DefaultStatus: http.StatusNoContent}, h.deleteNode)
}

func (h *handlers) viewNode(ctx context.Context, n db.Node, inbounds []db.Inbound) NodeInfo {
	v := NodeInfo{ID: n.ID, Name: n.Name, Local: n.Address == "", Address: n.Address, Host: domain.NodeHost(n), Domain: n.Domain,
		Enabled: n.Enabled != 0, Inbounds: len(domain.NodeInbounds(inbounds, n.ID)), Status: "unknown"}
	if v.Local {
		// The panel's own node is reached at the panel's address.
		ep, _ := h.d.Settings.Endpoint(ctx)
		v.Host = ep.Host
		v.Domain, _ = h.d.Settings.String(ctx, settings.KeyDomain)
	}
	v.Certificate = h.nodeCertView(n.ID, v.Host)
	if h.d.Nodes == nil {
		return v
	}
	hv, ok := h.d.Nodes.Health(n.ID)
	if !ok || hv.CheckedAt.IsZero() {
		return v
	}
	t := hv.CheckedAt
	v.CheckedAt = &t
	if !hv.OK {
		v.Status, v.Error = "error", hv.Error
		return v
	}
	v.Status, v.Version, v.Conns = "ok", hv.Health.Version, hv.Health.Conns
	v.CPUPercent, v.MemUsed, v.MemTotal = hv.Health.System.CPUPercent, hv.Health.System.MemUsed, hv.Health.System.MemTotal
	for _, l := range hv.Listeners {
		v.Listeners++
		if l.OK {
			v.ListenersOK++
		}
	}
	return v
}

func (h *handlers) listNodes(ctx context.Context, _ *struct{}) (*nodesOutput, error) {
	nodes, err := h.d.Store.Q.ListNodes(ctx)
	if err != nil {
		return nil, err
	}
	inbounds, err := h.d.Store.Q.ListInbounds(ctx)
	if err != nil {
		return nil, err
	}
	out := &nodesOutput{Body: make([]NodeInfo, 0, len(nodes))}
	for _, n := range nodes {
		out.Body = append(out.Body, h.viewNode(ctx, n, inbounds))
	}
	return out, nil
}

// checkNodeName: a node's name is its country group in Clash apps, so it follows the
// group rules and must not repeat another node or group.
func (h *handlers) checkNodeName(ctx context.Context, name string, self int64) error {
	bad := func(code string) error {
		return huma.Error422UnprocessableEntity("validation", &huma.ErrorDetail{Location: "body.name", Message: code})
	}
	if err := subs.ValidName(name); err != nil {
		return bad(err.Error())
	}
	g, err := h.groups(ctx)
	if err != nil {
		return err
	}
	for _, x := range []string{g.Main, g.Auto, subs.AliasGroup} {
		if strings.EqualFold(name, x) {
			return bad("name_is_group")
		}
	}
	nodes, err := h.d.Store.Q.ListNodes(ctx)
	if err != nil {
		return err
	}
	for _, n := range nodes {
		if n.ID != self && strings.EqualFold(n.Name, name) {
			return bad("name_in_use")
		}
	}
	return nil
}

func (h *handlers) createNode(ctx context.Context, in *createNodeInput) (*nodeKeyOutput, error) {
	if h.d.PanelCert == nil {
		return nil, huma.Error409Conflict("nodes_disabled")
	}
	b := in.Body
	name, host, dom := strings.TrimSpace(b.Name), strings.TrimSpace(b.Host), strings.TrimSpace(b.Domain)
	if err := h.checkNodeName(ctx, name, 0); err != nil {
		return nil, err
	}
	var details []error
	if !hostname.Valid(host) {
		details = append(details, &huma.ErrorDetail{Location: "body.host", Message: "public_host_invalid"})
	}
	if dom != "" && !hostname.Valid(dom) {
		details = append(details, &huma.ErrorDetail{Location: "body.domain", Message: "domain_invalid"})
	}
	if len(details) == 0 {
		if d := h.domainHere(ctx, "body.domain", dom, h.nodeAddrs(ctx, host)); d != nil {
			details = append(details, d)
		}
	}
	if len(details) > 0 {
		return nil, huma.Error422UnprocessableEntity("validation", details...)
	}
	panel, err := h.d.PanelCert()
	if err != nil {
		return nil, err
	}
	n, key, err := domain.AddNode(ctx, h.d.Store, panel, domain.NodeInput{Name: name, Host: host, Domain: dom, APIPort: b.APIPort}, h.d.Now())
	if err != nil {
		return nil, err
	}
	h.forgetNode(n.ID)
	h.nodesChanged()
	h.audit(ctx, sessionOf(ctx).AdminID, "node.create", "node", strconv.FormatInt(n.ID, 10), map[string]any{"name": n.Name, "address": n.Address})
	inbounds, err := h.d.Store.Q.ListInbounds(ctx)
	if err != nil {
		return nil, err
	}
	out := &nodeKeyOutput{}
	out.Body.Node, out.Body.Key, out.Body.Command = h.viewNode(ctx, n, inbounds), key, release.JoinCommand(key)
	return out, nil
}

func (h *handlers) getNode(ctx context.Context, id int64) (db.Node, error) {
	n, err := h.d.Store.Q.GetNode(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return n, huma.Error404NotFound("not_found")
	}
	return n, err
}

func (h *handlers) updateNode(ctx context.Context, in *patchNodeInput) (*nodeInfoOutput, error) {
	n, err := h.getNode(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	b := in.Body
	local := n.Address == ""
	if b.Name != nil {
		name := strings.TrimSpace(*b.Name)
		if err := h.checkNodeName(ctx, name, n.ID); err != nil {
			return nil, err
		}
		n.Name = name
	}
	if local && (b.Host != nil || b.Domain != nil) {
		// The panel's own node follows the panel's address in the settings.
		return nil, huma.Error422UnprocessableEntity("local_node")
	}
	if b.Host != nil {
		host := strings.TrimSpace(*b.Host)
		if !hostname.Valid(host) {
			return nil, huma.Error422UnprocessableEntity("validation", &huma.ErrorDetail{Location: "body.host", Message: "public_host_invalid"})
		}
		_, port, err := net.SplitHostPort(n.Address)
		if err != nil {
			return nil, err
		}
		n.PublicHost, n.Address = host, net.JoinHostPort(host, port)
	}
	if b.Domain != nil {
		dom := strings.TrimSpace(*b.Domain)
		if dom != "" && !hostname.Valid(dom) {
			return nil, huma.Error422UnprocessableEntity("validation", &huma.ErrorDetail{Location: "body.domain", Message: "domain_invalid"})
		}
		n.Domain = dom
	}
	// The node's domain must lead to the node: checked when it or the node's address changes.
	if n.Domain != "" && (b.Domain != nil || b.Host != nil) {
		if d := h.domainHere(ctx, "body.domain", n.Domain, h.nodeAddrs(ctx, n.PublicHost)); d != nil {
			return nil, huma.Error422UnprocessableEntity("validation", d)
		}
	}
	if b.Enabled != nil {
		n.Enabled = 0
		if *b.Enabled {
			n.Enabled = 1
		}
	}
	n, err = h.d.Store.Q.UpdateNode(ctx, db.UpdateNodeParams{Name: n.Name, Address: n.Address, PublicHost: n.PublicHost, Domain: n.Domain,
		Enabled: n.Enabled, UpdatedAt: h.d.Now().Unix(), ID: n.ID})
	if err != nil {
		return nil, err
	}
	h.nodesChanged()
	h.d.Changes.SlotsChanged()
	h.audit(ctx, sessionOf(ctx).AdminID, "node.update", "node", strconv.FormatInt(n.ID, 10), map[string]any{"name": n.Name, "enabled": n.Enabled != 0})
	return h.nodeInfo(ctx, n.ID)
}

// nodeInfo is one node as the Nodes page shows it.
func (h *handlers) nodeInfo(ctx context.Context, id int64) (*nodeInfoOutput, error) {
	n, err := h.getNode(ctx, id)
	if err != nil {
		return nil, err
	}
	inbounds, err := h.d.Store.Q.ListInbounds(ctx)
	if err != nil {
		return nil, err
	}
	return &nodeInfoOutput{Body: h.viewNode(ctx, n, inbounds)}, nil
}

func (h *handlers) rekeyNode(ctx context.Context, in *nodeIDInput) (*nodeKeyOutput, error) {
	if h.d.PanelCert == nil {
		return nil, huma.Error409Conflict("nodes_disabled")
	}
	panel, err := h.d.PanelCert()
	if err != nil {
		return nil, err
	}
	key, err := domain.RekeyNode(ctx, h.d.Store, panel, in.ID, h.d.Now())
	switch {
	case errors.Is(err, domain.ErrUnknownNode):
		return nil, huma.Error404NotFound("not_found")
	case errors.Is(err, domain.ErrLocalNode):
		return nil, huma.Error409Conflict("local_node")
	case err != nil:
		return nil, err
	}
	h.nodesChanged()
	h.audit(ctx, sessionOf(ctx).AdminID, "node.rekey", "node", strconv.FormatInt(in.ID, 10), nil)
	n, err := h.getNode(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	inbounds, err := h.d.Store.Q.ListInbounds(ctx)
	if err != nil {
		return nil, err
	}
	out := &nodeKeyOutput{}
	out.Body.Node, out.Body.Key, out.Body.Command = h.viewNode(ctx, n, inbounds), key, release.JoinCommand(key)
	return out, nil
}

// nodeUnused refuses to delete a node something still goes through: a cascade would
// quietly go straight out, the bot would lose its way to Telegram. The details list
// what to switch first.
func (h *handlers) nodeUnused(ctx context.Context, q *db.Queries, id int64) error {
	uses, err := domain.ExitUsesOf(ctx, q, id)
	if err != nil {
		return err
	}
	set := settings.New(q)
	route, _, err := settings.Get[tgbot.Route](ctx, set, tgbot.KeyRoute)
	if err != nil {
		return err
	}
	panelHost, err := set.String(ctx, settings.KeyPublicHost)
	if err != nil {
		return err
	}
	nodes, err := q.ListNodes(ctx)
	if err != nil {
		return err
	}
	// A node shows by its name, else by its address: the panel's own node by the panel's.
	labels := map[int64]string{}
	for _, n := range nodes {
		switch {
		case n.Name != "":
			labels[n.ID] = n.Name
		case n.PublicHost != "":
			labels[n.ID] = n.PublicHost
		default:
			labels[n.ID] = panelHost
		}
	}
	var details []error
	if len(uses.Inbounds) > 0 {
		var list []string
		for _, in := range uses.Inbounds {
			list = append(list, in.Name+" ("+labels[in.NodeID]+")")
		}
		details = append(details, &huma.ErrorDetail{Location: "path.id", Message: "node_in_use_inbounds", Value: strings.Join(list, ", ")})
	}
	if len(uses.Relays) > 0 {
		var list []string
		for _, src := range uses.Relays {
			list = append(list, labels[src])
		}
		details = append(details, &huma.ErrorDetail{Location: "path.id", Message: "node_in_use_relays", Value: strings.Join(list, ", ")})
	}
	if route.Mode == tgbot.RouteNode && route.NodeID == id {
		details = append(details, &huma.ErrorDetail{Location: "path.id", Message: "node_in_use_telegram"})
	}
	if len(details) > 0 {
		return huma.Error409Conflict("node_in_use", details...)
	}
	return nil
}

func (h *handlers) deleteNode(ctx context.Context, in *nodeIDInput) (*struct{}, error) {
	n, err := h.getNode(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	if n.Address == "" {
		return nil, huma.Error409Conflict("local_node")
	}
	err = h.d.Store.Tx(ctx, func(q *db.Queries) error {
		if err := h.nodeUnused(ctx, q, n.ID); err != nil {
			return err
		}
		if err := q.DeleteNode(ctx, n.ID); err != nil {
			return err
		}
		return q.DeleteNodeStateOf(ctx, strconv.FormatInt(n.ID, 10))
	})
	if err != nil {
		return nil, err
	}
	if h.d.Nodes != nil {
		// Best effort: an unreachable node keeps serving until it is reinstalled.
		if err := h.d.Nodes.Retire(ctx, n.ID); err != nil {
			h.d.Log.Warn("retire node", "node", n.ID, "err", err)
		}
		h.d.Nodes.NodesChanged()
	}
	// The row is gone, and with it the id may be given to the next node: it must not
	// inherit this one's certificates and private keys from disk.
	h.forgetNode(n.ID)
	h.d.Changes.SlotsChanged()
	h.audit(ctx, sessionOf(ctx).AdminID, "node.delete", "node", strconv.FormatInt(n.ID, 10), map[string]any{"name": n.Name})
	return nil, nil
}

// nodesChanged tells the node syncer to look at the nodes again; the panel may run
// without nodes (tests, development).
func (h *handlers) nodesChanged() {
	if h.d.Nodes != nil {
		h.d.Nodes.NodesChanged()
	}
}

// forgetNode drops the certificates and keys the panel keeps for a node id: the admin's
// own certificate and the node's self-signed pair. Node ids are reused (the table has no
// AUTOINCREMENT), so a new node also clears what an id left behind.
func (h *handlers) forgetNode(id int64) {
	if h.d.ForgetNode == nil {
		return
	}
	if err := h.d.ForgetNode(id); err != nil {
		h.d.Log.Warn("remove node certificates", "node", id, "err", err)
	}
}
