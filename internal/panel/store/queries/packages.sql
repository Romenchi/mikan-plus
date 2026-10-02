-- name: ListTrafficPackages :many
SELECT * FROM traffic_packages WHERE archived = 0 ORDER BY sort, id;

-- name: ListAllTrafficPackages :many
-- Archived ones too: grants and payments keep naming them.
SELECT * FROM traffic_packages ORDER BY id;

-- name: ListTrafficPackagesOnSale :many
SELECT * FROM traffic_packages WHERE archived = 0 AND on_sale = 1 ORDER BY sort, id;

-- name: GetTrafficPackage :one
SELECT * FROM traffic_packages WHERE id = ?;

-- name: CreateTrafficPackage :one
INSERT INTO traffic_packages (name, bytes, pool_id, lifetime, days, price_stars, price_rub, on_sale, sort, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: UpdateTrafficPackage :one
UPDATE traffic_packages
SET name = ?, bytes = ?, pool_id = ?, lifetime = ?, days = ?, price_stars = ?, price_rub = ?, on_sale = ?, sort = ?
WHERE id = ? AND archived = 0
RETURNING *;

-- name: ArchiveTrafficPackage :execrows
UPDATE traffic_packages SET archived = 1, on_sale = 0 WHERE id = ? AND archived = 0;

-- name: CreateTrafficGrant :one
INSERT INTO traffic_grants (user_id, pool_id, bytes, remaining, lifetime, expires_at, source, payment_id, package_id, note, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: ListUserGrants :many
SELECT * FROM traffic_grants WHERE user_id = ? ORDER BY created_at DESC, id DESC;

-- name: ListSpendableGrants :many
-- The order traffic past the base quota is taken in: the soonest to expire first, then
-- the oldest. Expired and used-up grants are left out.
SELECT * FROM traffic_grants
WHERE user_id = sqlc.arg(user_id) AND pool_id IS sqlc.narg(pool_id) AND remaining > 0
  AND (expires_at IS NULL OR expires_at > CAST(sqlc.arg(now) AS INTEGER))
ORDER BY expires_at IS NULL, expires_at, created_at, id;

-- name: SpendGrant :exec
UPDATE traffic_grants SET remaining = remaining - sqlc.arg(spent) WHERE id = sqlc.arg(id);

-- name: SumGrantsLeft :many
-- What is left of the active grants per user and target (pool 0: the main traffic).
SELECT user_id, CAST(IFNULL(pool_id, 0) AS INTEGER) AS pool_id, CAST(SUM(remaining) AS INTEGER) AS left_bytes
FROM traffic_grants
WHERE remaining > 0 AND (expires_at IS NULL OR expires_at > CAST(sqlc.arg(now) AS INTEGER))
GROUP BY user_id, IFNULL(pool_id, 0);

-- name: SumUserGrantsLeft :many
SELECT user_id, CAST(IFNULL(pool_id, 0) AS INTEGER) AS pool_id, CAST(SUM(remaining) AS INTEGER) AS left_bytes
FROM traffic_grants
WHERE user_id = sqlc.arg(user_id) AND remaining > 0 AND (expires_at IS NULL OR expires_at > CAST(sqlc.arg(now) AS INTEGER))
GROUP BY user_id, IFNULL(pool_id, 0);

-- name: EndPeriodGrants :exec
-- A new traffic period ends the grants that last one period.
UPDATE traffic_grants SET expires_at = CAST(sqlc.arg(now) AS INTEGER)
WHERE user_id = sqlc.arg(user_id) AND lifetime = 'period' AND remaining > 0
  AND (expires_at IS NULL OR expires_at > CAST(sqlc.arg(now) AS INTEGER));

-- name: GetUserPool :one
SELECT * FROM user_pools WHERE user_id = ? AND pool_id = ?;

-- name: CreatePackagePayment :one
INSERT INTO payments (provider, payload, tg_id, kind, user_id, package_id, tariff_name, amount, currency, status, created_at)
VALUES (?, ?, ?, 'package', ?, ?, ?, ?, ?, 'pending', ?)
RETURNING *;

-- name: FindOpenPackagePayment :one
SELECT * FROM payments
WHERE tg_id = ? AND package_id = ? AND provider = ? AND kind = 'package' AND user_id = ?
  AND status = 'pending' AND pay_url <> '' AND created_at > sqlc.arg(since)
ORDER BY id DESC LIMIT 1;
