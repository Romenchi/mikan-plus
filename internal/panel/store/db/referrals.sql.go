package db

import (
	"context"
)

const recordReferral = `-- name: RecordReferral :exec
INSERT OR IGNORE INTO tg_referrals (referrer_tg_id, referee_tg_id, created_at)
VALUES (?, ?, ?)
`

type RecordReferralParams struct {
	ReferrerTgID int64
	RefereeTgID  int64
	CreatedAt    int64
}

func (q *Queries) RecordReferral(ctx context.Context, arg RecordReferralParams) error {
	_, err := q.db.ExecContext(ctx, recordReferral, arg.ReferrerTgID, arg.RefereeTgID, arg.CreatedAt)
	return err
}

const getReferralByReferee = `-- name: GetReferralByReferee :one
SELECT id, referrer_tg_id, referee_tg_id, bonus_applied, reward_days, created_at, applied_at
FROM tg_referrals
WHERE referee_tg_id = ?
`

func (q *Queries) GetReferralByReferee(ctx context.Context, refereeTgID int64) (TgReferral, error) {
	row := q.db.QueryRowContext(ctx, getReferralByReferee, refereeTgID)
	var i TgReferral
	err := row.Scan(
		&i.ID,
		&i.ReferrerTgID,
		&i.RefereeTgID,
		&i.BonusApplied,
		&i.RewardDays,
		&i.CreatedAt,
		&i.AppliedAt,
	)
	return i, err
}

const applyReferralReward = `-- name: ApplyReferralReward :execrows
UPDATE tg_referrals
SET bonus_applied = 1, reward_days = ?, applied_at = ?
WHERE referee_tg_id = ? AND bonus_applied = 0
`

type ApplyReferralRewardParams struct {
	RewardDays  int64
	AppliedAt   int64
	RefereeTgID int64
}

func (q *Queries) ApplyReferralReward(ctx context.Context, arg ApplyReferralRewardParams) (int64, error) {
	result, err := q.db.ExecContext(ctx, applyReferralReward, arg.RewardDays, arg.AppliedAt, arg.RefereeTgID)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

const countReferrals = `-- name: CountReferrals :one
SELECT COUNT(*) FROM tg_referrals WHERE referrer_tg_id = ?
`

func (q *Queries) CountReferrals(ctx context.Context, referrerTgID int64) (int64, error) {
	row := q.db.QueryRowContext(ctx, countReferrals, referrerTgID)
	var count int64
	err := row.Scan(&count)
	return count, err
}

const sumReferralDays = `-- name: SumReferralDays :one
SELECT COALESCE(SUM(reward_days), 0) FROM tg_referrals WHERE referrer_tg_id = ?
`

func (q *Queries) SumReferralDays(ctx context.Context, referrerTgID int64) (int64, error) {
	row := q.db.QueryRowContext(ctx, sumReferralDays, referrerTgID)
	var sum int64
	err := row.Scan(&sum)
	return sum, err
}

const listReferralsOf = `-- name: ListReferralsOf :many
SELECT r.id, r.referrer_tg_id, r.referee_tg_id, r.bonus_applied, r.reward_days, r.created_at, r.applied_at,
       COALESCE(c.username, '') AS referee_username, COALESCE(c.first_name, '') AS referee_first_name
FROM tg_referrals r
LEFT JOIN tg_chats c ON c.tg_id = r.referee_tg_id
WHERE r.referrer_tg_id = ?
ORDER BY r.created_at DESC
LIMIT 50
`

type ListReferralsOfRow struct {
	ID               int64
	ReferrerTgID     int64
	RefereeTgID      int64
	BonusApplied     int64
	RewardDays       int64
	CreatedAt        int64
	AppliedAt        int64
	RefereeUsername  string
	RefereeFirstName string
}

func (q *Queries) ListReferralsOf(ctx context.Context, referrerTgID int64) ([]ListReferralsOfRow, error) {
	rows, err := q.db.QueryContext(ctx, listReferralsOf, referrerTgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []ListReferralsOfRow
	for rows.Next() {
		var i ListReferralsOfRow
		if err := rows.Scan(
			&i.ID,
			&i.ReferrerTgID,
			&i.RefereeTgID,
			&i.BonusApplied,
			&i.RewardDays,
			&i.CreatedAt,
			&i.AppliedAt,
			&i.RefereeUsername,
			&i.RefereeFirstName,
		); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}
