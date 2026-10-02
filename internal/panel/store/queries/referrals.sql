-- name: RecordReferral :exec
INSERT OR IGNORE INTO tg_referrals (referrer_tg_id, referee_tg_id, created_at)
VALUES (?, ?, ?);

-- name: GetReferralByReferee :one
SELECT id, referrer_tg_id, referee_tg_id, bonus_applied, reward_days, created_at, applied_at
FROM tg_referrals
WHERE referee_tg_id = ?;

-- name: ApplyReferralReward :execrows
UPDATE tg_referrals
SET bonus_applied = 1, reward_days = ?, applied_at = ?
WHERE referee_tg_id = ? AND bonus_applied = 0;

-- name: CountReferrals :one
SELECT COUNT(*) FROM tg_referrals WHERE referrer_tg_id = ?;

-- name: SumReferralDays :one
SELECT COALESCE(SUM(reward_days), 0) FROM tg_referrals WHERE referrer_tg_id = ?;

-- name: ListReferralsOf :many
SELECT r.id, r.referrer_tg_id, r.referee_tg_id, r.bonus_applied, r.reward_days, r.created_at, r.applied_at,
       COALESCE(c.username, '') AS referee_username, COALESCE(c.first_name, '') AS referee_first_name
FROM tg_referrals r
LEFT JOIN tg_chats c ON c.tg_id = r.referee_tg_id
WHERE r.referrer_tg_id = ?
ORDER BY r.created_at DESC
LIMIT 50;
