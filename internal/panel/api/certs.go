package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/danielgtaylor/huma/v2"

	"mikan/internal/panel/tlscert"
)

// The admin's own certificates (GitHub issue #9): the panel's instead of Let's Encrypt,
// and a node's instead of its self-signed one. A private key comes in and never goes
// out: not in a response, a log or the audit log. API keys cannot reach these.

type certInput struct {
	Body struct {
		Cert string `json:"cert" minLength:"1" maxLength:"65536" doc:"Цепочка в PEM: сначала сертификат, за ним промежуточные (fullchain.pem)"`
		Key  string `json:"key" minLength:"1" maxLength:"65536" doc:"Закрытый ключ в PEM (privkey.pem): RSA от 2048 бит, ECDSA P-256/384/521 или Ed25519"`
	}
}

type nodeCertInput struct {
	ID int64 `path:"id" minimum:"1"`
	certInput
}

// NodeCertView is a node's own certificate as the admin panel shows it.
type NodeCertView struct {
	tlscert.Info
	Error string `json:"error,omitempty" doc:"custom_expired, custom_invalid — сертификат не используется, нода на своём самоподписанном"`
}

func (h *handlers) registerCerts() {
	tags := []string{"settings"}
	huma.Register(h.api, huma.Operation{OperationID: "set-certificate", Method: http.MethodPut, Path: "/api/v1/settings/certificate", Summary: "Поставить свой сертификат панели",
		Tags: tags, Metadata: sessionOnly, Extensions: sessionOnlyExt}, h.setCertificate)
	huma.Register(h.api, huma.Operation{OperationID: "clear-certificate", Method: http.MethodDelete, Path: "/api/v1/settings/certificate", Summary: "Вернуть сертификат Let's Encrypt",
		Tags: tags, Metadata: sessionOnly, Extensions: sessionOnlyExt, DefaultStatus: http.StatusNoContent}, h.clearCertificate)
	nodeTags := []string{"node"}
	huma.Register(h.api, huma.Operation{OperationID: "set-node-certificate", Method: http.MethodPut, Path: "/api/v1/nodes/{id}/certificate", Summary: "Поставить ноде свой сертификат",
		Tags: nodeTags, Metadata: sessionOnly, Extensions: sessionOnlyExt}, h.setNodeCertificate)
	huma.Register(h.api, huma.Operation{OperationID: "clear-node-certificate", Method: http.MethodDelete, Path: "/api/v1/nodes/{id}/certificate", Summary: "Вернуть ноде самоподписанный сертификат",
		Tags: nodeTags, Metadata: sessionOnly, Extensions: sessionOnlyExt, DefaultStatus: http.StatusNoContent}, h.clearNodeCertificate)
}

// certError maps a refused certificate to the field to fix.
func certError(err error, host string) error {
	field, value := "body.cert", ""
	switch {
	case errors.Is(err, tlscert.ErrKeyPEM), errors.Is(err, tlscert.ErrKeyMismatch), errors.Is(err, tlscert.ErrKeyWeak):
		field = "body.key"
	case errors.Is(err, tlscert.ErrWrongHost):
		value = host
	case errors.Is(err, tlscert.ErrCertPEM), errors.Is(err, tlscert.ErrExpired), errors.Is(err, tlscert.ErrNotYet):
	default:
		return err
	}
	return huma.Error422UnprocessableEntity("bad_certificate", &huma.ErrorDetail{Location: field, Message: err.Error(), Value: value})
}

func (h *handlers) setCertificate(ctx context.Context, in *certInput) (*settingsOutput, error) {
	if h.d.SetCert == nil {
		return nil, huma.Error409Conflict("acme_disabled")
	}
	if err := h.d.SetCert(ctx, []byte(in.Body.Cert), []byte(in.Body.Key)); err != nil {
		ep, _ := h.d.Settings.Endpoint(ctx)
		return nil, certError(err, ep.Host)
	}
	st := h.d.Cert()
	h.audit(ctx, sessionOf(ctx).AdminID, "settings.certificate", "", "", map[string]any{"kind": "custom", "names": st.Names, "not_after": st.NotAfter})
	v, err := h.readSettings(ctx)
	if err != nil {
		return nil, err
	}
	return &settingsOutput{Body: v}, nil
}

func (h *handlers) clearCertificate(ctx context.Context, _ *struct{}) (*struct{}, error) {
	if h.d.ClearCert == nil {
		return nil, huma.Error409Conflict("acme_disabled")
	}
	if err := h.d.ClearCert(); err != nil {
		return nil, err
	}
	h.audit(ctx, sessionOf(ctx).AdminID, "settings.certificate", "", "", map[string]any{"kind": "automatic"})
	return nil, nil
}

func (h *handlers) setNodeCertificate(ctx context.Context, in *nodeCertInput) (*nodeInfoOutput, error) {
	if h.d.NodeCerts == nil {
		return nil, huma.Error409Conflict("node_certs_disabled")
	}
	n, err := h.getNode(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	c, err := h.d.NodeCerts.Set(n.ID, []byte(in.Body.Cert), []byte(in.Body.Key))
	if err != nil {
		return nil, certError(err, "")
	}
	// The node gets the new certificate with its next state; links get the new pin.
	if h.d.Changes != nil {
		h.d.Changes.SlotsChanged()
	}
	h.audit(ctx, sessionOf(ctx).AdminID, "node.certificate", "node", strconv.FormatInt(n.ID, 10), map[string]any{"kind": "custom", "names": c.Leaf.DNSNames, "not_after": c.Leaf.NotAfter})
	return h.nodeInfo(ctx, n.ID)
}

func (h *handlers) clearNodeCertificate(ctx context.Context, in *nodeIDInput) (*struct{}, error) {
	if h.d.NodeCerts == nil {
		return nil, huma.Error409Conflict("node_certs_disabled")
	}
	n, err := h.getNode(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	if err := h.d.NodeCerts.Clear(n.ID); err != nil {
		return nil, err
	}
	if h.d.Changes != nil {
		h.d.Changes.SlotsChanged()
	}
	h.audit(ctx, sessionOf(ctx).AdminID, "node.certificate", "node", strconv.FormatInt(n.ID, 10), map[string]any{"kind": "self-signed"})
	return nil, nil
}

// nodeCertView is what a node's own certificate is now, nil without one.
func (h *handlers) nodeCertView(id int64, host string) *NodeCertView {
	if h.d.NodeCerts == nil {
		return nil
	}
	c, trusted, err := h.d.NodeCerts.Get(id, host)
	switch {
	case c != nil:
		v := &NodeCertView{Info: tlscert.Describe(c, host, h.d.Now())}
		v.Trusted = trusted
		return v
	case errors.Is(err, tlscert.ErrExpired):
		return &NodeCertView{Error: "custom_expired"}
	case err != nil:
		return &NodeCertView{Error: "custom_invalid"}
	}
	return nil
}
