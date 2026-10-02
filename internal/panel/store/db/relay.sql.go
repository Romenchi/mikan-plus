package db

import (
	"context"
)

const deleteNodeUpstreamRelay = `-- name: DeleteNodeUpstreamRelay :exec
DELETE FROM node_relay WHERE node_id = ?
`

func (q *Queries) DeleteNodeUpstreamRelay(ctx context.Context, nodeID int64) error {
	_, err := q.db.ExecContext(ctx, deleteNodeUpstreamRelay, nodeID)
	return err
}

const getNodeUpstreamRelay = `-- name: GetNodeUpstreamRelay :one
SELECT node_id, enabled, protocol, server, port, uuid, flow, tls, sni, public_key, short_id, spider_x, fingerprint, inbounds, created_at, updated_at FROM node_relay WHERE node_id = ?
`

func (q *Queries) GetNodeUpstreamRelay(ctx context.Context, nodeID int64) (NodeUpstreamRelay, error) {
	row := q.db.QueryRowContext(ctx, getNodeUpstreamRelay, nodeID)
	var i NodeUpstreamRelay
	err := row.Scan(
		&i.NodeID,
		&i.Enabled,
		&i.Protocol,
		&i.Server,
		&i.Port,
		&i.Uuid,
		&i.Flow,
		&i.Tls,
		&i.Sni,
		&i.PublicKey,
		&i.ShortID,
		&i.SpiderX,
		&i.Fingerprint,
		&i.Inbounds,
		&i.CreatedAt,
		&i.UpdatedAt,
	)
	return i, err
}

const saveNodeUpstreamRelay = `-- name: SaveNodeUpstreamRelay :exec
INSERT INTO node_relay (node_id, enabled, protocol, server, port, uuid, flow, tls, sni, public_key, short_id, spider_x, fingerprint, inbounds, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (node_id) DO UPDATE SET enabled = excluded.enabled, protocol = excluded.protocol, server = excluded.server,
  port = excluded.port, uuid = excluded.uuid, flow = excluded.flow, tls = excluded.tls, sni = excluded.sni,
  public_key = excluded.public_key, short_id = excluded.short_id, spider_x = excluded.spider_x, fingerprint = excluded.fingerprint,
  inbounds = excluded.inbounds, updated_at = excluded.updated_at
`

type SaveNodeUpstreamRelayParams struct {
	NodeID      int64
	Enabled     int64
	Protocol    string
	Server      string
	Port        int64
	Uuid        string
	Flow        string
	Tls         int64
	Sni         string
	PublicKey   string
	ShortID     string
	SpiderX     string
	Fingerprint string
	Inbounds    string
	CreatedAt   int64
	UpdatedAt   int64
}

func (q *Queries) SaveNodeUpstreamRelay(ctx context.Context, arg SaveNodeUpstreamRelayParams) error {
	_, err := q.db.ExecContext(ctx, saveNodeUpstreamRelay,
		arg.NodeID,
		arg.Enabled,
		arg.Protocol,
		arg.Server,
		arg.Port,
		arg.Uuid,
		arg.Flow,
		arg.Tls,
		arg.Sni,
		arg.PublicKey,
		arg.ShortID,
		arg.SpiderX,
		arg.Fingerprint,
		arg.Inbounds,
		arg.CreatedAt,
		arg.UpdatedAt,
	)
	return err
}

