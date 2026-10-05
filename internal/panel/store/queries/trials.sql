-- name: RecordTgTrial :exec
INSERT INTO tg_trials (tg_id, user_id, created_at) VALUES (?, ?, ?);

-- name: HasTgTrial :one
SELECT COUNT(*) FROM tg_trials WHERE tg_id = ?;

-- name: GetTgTrial :one
SELECT * FROM tg_trials WHERE tg_id = ?;
