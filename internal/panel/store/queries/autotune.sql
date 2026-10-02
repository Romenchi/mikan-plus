-- name: SetInboundAuto :exec
-- The switches do not change what clients get, so updated_at stays.
UPDATE inbounds SET auto_port = ?, auto_sni = ? WHERE id = ?;

-- name: RecordSubFetch :exec
INSERT INTO sub_fetches (user_id, ip, fetched_at) VALUES (?, ?, ?)
ON CONFLICT (user_id, ip) DO UPDATE SET fetched_at = excluded.fetched_at;

-- name: ListSubFetchesSince :many
SELECT * FROM sub_fetches WHERE fetched_at >= ?;

-- name: PruneSubFetches :exec
DELETE FROM sub_fetches WHERE fetched_at < ?;

-- name: AddInboundEvent :exec
INSERT INTO inbound_events (inbound_id, node_id, kind, network, old_value, new_value, reason, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?);

-- name: LastInboundEvents :many
-- The latest automatic change of every inbound that had one.
SELECT * FROM inbound_events WHERE id IN (SELECT MAX(id) FROM inbound_events GROUP BY inbound_id);

-- name: ListInboundEventsSince :many
SELECT * FROM inbound_events WHERE created_at >= ? ORDER BY id;

-- name: PruneInboundEvents :exec
DELETE FROM inbound_events WHERE created_at < ?;

-- name: UpsertInboundReach :exec
INSERT INTO inbound_reach (slot, inbound_id, at) VALUES (?, ?, ?)
ON CONFLICT (slot, inbound_id) DO UPDATE SET at = max(at, excluded.at);

-- name: ListInboundReachSince :many
SELECT * FROM inbound_reach WHERE at >= ?;

-- name: PruneInboundReach :exec
DELETE FROM inbound_reach WHERE at < ?;

-- name: SetInboundListen :exec
-- The listen address is the node's business, clients get nothing new: updated_at stays.
UPDATE inbounds SET listen = ? WHERE id = ?;
