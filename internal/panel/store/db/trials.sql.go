package db

import "context"

const hasTgTrial = `-- name: HasTgTrial :one
SELECT COUNT(*) FROM tg_trials WHERE tg_id = ?
`

func (q *Queries) HasTgTrial(ctx context.Context, tgID int64) (int64, error) {
	row := q.db.QueryRowContext(ctx, hasTgTrial, tgID)
	var count int64
	err := row.Scan(&count)
	return count, err
}

const recordTgTrial = `-- name: RecordTgTrial :exec
INSERT INTO tg_trials (tg_id, user_id, created_at) VALUES (?, ?, ?)
`

type RecordTgTrialParams struct {
	TgID      int64
	UserID    int64
	CreatedAt int64
}

func (q *Queries) RecordTgTrial(ctx context.Context, arg RecordTgTrialParams) error {
	_, err := q.db.ExecContext(ctx, recordTgTrial, arg.TgID, arg.UserID, arg.CreatedAt)
	return err
}

const getTgTrial = `-- name: GetTgTrial :one
SELECT tg_id, user_id, created_at FROM tg_trials WHERE tg_id = ?
`

func (q *Queries) GetTgTrial(ctx context.Context, tgID int64) (TgTrial, error) {
	row := q.db.QueryRowContext(ctx, getTgTrial, tgID)
	var i TgTrial
	err := row.Scan(&i.TgID, &i.UserID, &i.CreatedAt)
	return i, err
}
