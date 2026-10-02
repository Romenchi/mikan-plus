-- name: GetSetting :one
SELECT value FROM settings WHERE key = ?;

-- name: SetSetting :exec
INSERT INTO settings (key, value) VALUES (?, ?)
ON CONFLICT (key) DO UPDATE SET value = excluded.value;

-- name: CountAdmins :one
SELECT count(*) FROM admins;

-- name: ListAdmins :many
SELECT * FROM admins ORDER BY id;

-- name: GetAdmin :one
SELECT * FROM admins WHERE id = ?;

-- name: GetAdminByUsername :one
SELECT * FROM admins WHERE username = ?;

-- name: CreateAdmin :one
INSERT INTO admins (username, password_hash, created_at) VALUES (?, ?, ?)
RETURNING *;

-- name: SetAdminPassword :exec
UPDATE admins SET password_hash = ? WHERE id = ?;

-- name: SetAdminTOTP :exec
UPDATE admins SET totp_secret = ?, recovery_codes = ? WHERE id = ?;

-- name: SpendAdminRecoveryCodes :execrows
-- Takes a recovery code out of the list it was found in: 0 rows means another login spent
-- a code from the same list first, and this one is refused.
UPDATE admins SET recovery_codes = sqlc.arg(rest) WHERE id = sqlc.arg(id) AND recovery_codes = sqlc.arg(was);

-- name: SetAdminLastLogin :exec
UPDATE admins SET last_login_at = ? WHERE id = ?;

-- name: CreateSession :exec
INSERT INTO sessions (id_hash, admin_id, csrf_token, created_at, last_seen_at, expires_at, ip, user_agent)
VALUES (?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetSession :one
SELECT * FROM sessions WHERE id_hash = ?;

-- name: TouchSession :exec
UPDATE sessions SET last_seen_at = ? WHERE id_hash = ?;

-- name: DeleteSession :exec
DELETE FROM sessions WHERE id_hash = ?;

-- name: DeleteAdminSessions :exec
DELETE FROM sessions WHERE admin_id = ?;

-- name: DeleteAdminSessionsExcept :exec
DELETE FROM sessions WHERE admin_id = ? AND id_hash <> ?;

-- name: ListAdminSessions :many
SELECT * FROM sessions WHERE admin_id = ? ORDER BY last_seen_at DESC;

-- name: DeleteStaleSessions :exec
DELETE FROM sessions WHERE expires_at < sqlc.arg(now) OR last_seen_at < sqlc.arg(idle_before);

-- name: InsertAudit :exec
INSERT INTO audit_log (ts, admin_id, action, target_type, target_id, ip, details)
VALUES (?, ?, ?, ?, ?, ?, ?);

-- name: ListAudit :many
SELECT * FROM audit_log WHERE id < sqlc.arg(before_id) ORDER BY id DESC LIMIT sqlc.arg(lim);

-- name: PruneAudit :execrows
DELETE FROM audit_log WHERE ts < ?;
