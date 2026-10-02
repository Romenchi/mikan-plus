-- name: CreateAPIKey :one
INSERT INTO api_keys (admin_id, name, prefix, hash, scope, created_at, expires_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: GetAPIKeyByHash :one
SELECT * FROM api_keys WHERE hash = ?;

-- name: ListAPIKeys :many
SELECT * FROM api_keys ORDER BY id;

-- name: CountAPIKeys :one
SELECT count(*) FROM api_keys;

-- name: DeleteAPIKey :execrows
DELETE FROM api_keys WHERE id = ?;

-- name: DeleteAPIKeysOf :execrows
DELETE FROM api_keys WHERE admin_id = ?;

-- name: TouchAPIKey :exec
UPDATE api_keys SET last_used_at = ?, last_ip = ?
WHERE id = ? AND (last_used_at IS NULL OR last_used_at < ?);
