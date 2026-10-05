-- +goose Up
CREATE TABLE IF NOT EXISTS tg_trials (
  tg_id      INTEGER PRIMARY KEY,
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  created_at INTEGER NOT NULL
);

-- +goose Down
DROP TABLE IF EXISTS tg_trials;
