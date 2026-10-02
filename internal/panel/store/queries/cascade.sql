-- name: GetNodeRelay :one
SELECT * FROM node_relays WHERE node_id = ?;

-- name: ListNodeRelays :many
SELECT * FROM node_relays ORDER BY node_id;

-- name: CreateNodeRelay :one
INSERT INTO node_relays (node_id, port, config, created_at) VALUES (?, ?, ?, ?)
ON CONFLICT (node_id) DO UPDATE SET node_id = excluded.node_id
RETURNING *;

-- name: SetNodeRelayRoute :exec
UPDATE node_relays SET outbound = ?, exit_node_id = ? WHERE node_id = ?;

-- name: GetRelayUser :one
SELECT uuid FROM relay_users WHERE exit_node_id = ? AND src_node_id = ?;

-- name: AddRelayUser :exec
INSERT INTO relay_users (exit_node_id, src_node_id, uuid) VALUES (?, ?, ?)
ON CONFLICT (exit_node_id, src_node_id) DO NOTHING;

-- name: ListRelayUsers :many
SELECT * FROM relay_users WHERE exit_node_id = ? ORDER BY src_node_id;

-- name: SetInboundExit :exec
UPDATE inbounds SET exit_node_id = ?, outbound = ? WHERE id = ?;
