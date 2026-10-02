-- +goose Up
CREATE TABLE IF NOT EXISTS tg_referrals (
  id             INTEGER PRIMARY KEY AUTOINCREMENT,
  referrer_tg_id INTEGER NOT NULL,
  referee_tg_id  INTEGER NOT NULL UNIQUE,
  bonus_applied  INTEGER NOT NULL DEFAULT 0,
  reward_days    INTEGER NOT NULL DEFAULT 0,
  created_at     INTEGER NOT NULL,
  applied_at     INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_tg_referrals_referrer ON tg_referrals(referrer_tg_id);

-- +goose Down
DROP TABLE IF EXISTS tg_referrals;
