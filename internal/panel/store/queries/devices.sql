-- name: GetBoundDevice :one
SELECT * FROM bound_devices WHERE user_id = ? AND hwid = ?;

-- name: GetBoundDeviceByID :one
SELECT * FROM bound_devices WHERE id = ? AND user_id = ?;

-- name: ListBoundDevices :many
SELECT * FROM bound_devices WHERE user_id = ? ORDER BY created_at, id;

-- name: ListIdleBoundDevices :many
SELECT * FROM bound_devices WHERE last_seen < ?;

-- name: CountBoundDevices :one
SELECT count(*) FROM bound_devices WHERE user_id = ?;

-- name: CreateBoundDevice :one
INSERT INTO bound_devices (user_id, hwid, slot_id, os, os_version, model, app, last_ip, created_at, last_seen)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: TouchBoundDevice :exec
UPDATE bound_devices SET os = ?, os_version = ?, model = ?, app = ?, last_ip = ?, last_seen = ? WHERE id = ?;

-- name: DeleteBoundDevice :exec
DELETE FROM bound_devices WHERE id = ?;

-- name: DeleteBoundDevicesOf :exec
DELETE FROM bound_devices WHERE user_id = ?;

-- name: SetUserSlot :exec
-- A new own slot for the user, same subscription link (the shared device was unbound).
UPDATE users SET slot_id = ?, updated_at = ? WHERE id = ?;

-- name: SetUserUnboundAt :exec
UPDATE users SET unbound_at = ? WHERE id = ?;

-- name: ListDeviceSlots :many
-- Slots of bound devices with an id: keys of their own, profile fetches of their own.
SELECT s.name AS slot_name, d.user_id, d.last_seen FROM bound_devices d JOIN slots s ON s.id = d.slot_id WHERE d.hwid != '';
