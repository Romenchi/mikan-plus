-- name: ListTrafficPools :many
SELECT * FROM traffic_pools ORDER BY name;

-- name: GetTrafficPool :one
SELECT * FROM traffic_pools WHERE id = ?;

-- name: CreateTrafficPool :one
INSERT INTO traffic_pools (name, created_at) VALUES (?, ?) RETURNING *;

-- name: RenameTrafficPool :execrows
UPDATE traffic_pools SET name = ? WHERE id = ?;

-- name: DeleteTrafficPool :execrows
DELETE FROM traffic_pools WHERE id = ?;

-- name: SetInboundPool :exec
UPDATE inbounds SET pool_id = ? WHERE id = ?;

-- name: ListTariffPools :many
SELECT * FROM tariff_pools WHERE tariff_id = ? ORDER BY pool_id;

-- name: ListAllTariffPools :many
SELECT * FROM tariff_pools ORDER BY tariff_id, pool_id;

-- name: ClearTariffPools :exec
DELETE FROM tariff_pools WHERE tariff_id = ?;

-- name: AddTariffPool :exec
INSERT INTO tariff_pools (tariff_id, pool_id, traffic_limit) VALUES (?, ?, ?);

-- name: ListUserPools :many
SELECT * FROM user_pools WHERE user_id = ? ORDER BY pool_id;

-- name: ListAllUserPools :many
SELECT * FROM user_pools ORDER BY user_id, pool_id;

-- name: ClearUserPoolLimits :exec
UPDATE user_pools SET traffic_limit = NULL WHERE user_id = ?;

-- name: SetUserPoolLimit :exec
INSERT INTO user_pools (user_id, pool_id, traffic_limit) VALUES (?, ?, ?)
ON CONFLICT (user_id, pool_id) DO UPDATE SET traffic_limit = excluded.traffic_limit;

-- name: AddUserPoolTraffic :exec
INSERT INTO user_pools (user_id, pool_id, used_up, used_down) VALUES (?, ?, ?, ?)
ON CONFLICT (user_id, pool_id) DO UPDATE SET used_up = used_up + excluded.used_up, used_down = used_down + excluded.used_down;

-- name: AddUserTotalTraffic :exec
-- Pool traffic: the all-time totals take it, the main period's counters do not.
UPDATE users SET total_up = total_up + sqlc.arg(up), total_down = total_down + sqlc.arg(down) WHERE id = sqlc.arg(id);

-- name: ResetUserPools :exec
-- Pools reset with the main traffic: a new period, a renewal, the admin's reset.
UPDATE user_pools SET used_up = 0, used_down = 0 WHERE user_id = ?;

-- name: PoolUsage :one
-- What a pool still holds that deleting it would destroy (the cascade takes grants and
-- packages with it, and a paid invoice of a package loses the package): traffic users
-- paid for and have left, packages of the catalog, invoices not closed yet.
SELECT
  (SELECT COUNT(*) FROM traffic_grants g
    WHERE g.pool_id = sqlc.arg(pool_id) AND g.remaining > 0 AND (g.expires_at IS NULL OR g.expires_at > CAST(sqlc.arg(now) AS INTEGER))) AS grants,
  (SELECT COUNT(*) FROM traffic_packages k WHERE k.pool_id = sqlc.arg(pool_id) AND k.archived = 0) AS packages,
  (SELECT COUNT(*) FROM payments p JOIN traffic_packages k ON k.id = p.package_id
    WHERE k.pool_id = sqlc.arg(pool_id) AND p.status IN ('pending', 'paid')) AS payments;
