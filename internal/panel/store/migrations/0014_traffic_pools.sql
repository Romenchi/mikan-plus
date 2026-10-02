-- +goose Up
-- Traffic pools (GitHub issue #6): chosen inbounds count to a pool with its own limit per
-- user, apart from the main quota. A WL node may have 100 GB a month while the others
-- stay unlimited, in one subscription.
CREATE TABLE traffic_pools (
  id         INTEGER PRIMARY KEY,
  name       TEXT NOT NULL UNIQUE,
  created_at INTEGER NOT NULL
);

ALTER TABLE inbounds ADD COLUMN pool_id INTEGER REFERENCES traffic_pools(id) ON DELETE SET NULL;

-- A tariff's limit per pool; a pool it does not list is unlimited for its users.
CREATE TABLE tariff_pools (
  tariff_id     INTEGER NOT NULL REFERENCES tariffs(id) ON DELETE CASCADE,
  pool_id       INTEGER NOT NULL REFERENCES traffic_pools(id) ON DELETE CASCADE,
  traffic_limit INTEGER NOT NULL, -- bytes
  PRIMARY KEY (tariff_id, pool_id)
) WITHOUT ROWID;

-- A user's limit and use per pool. No row, or a NULL limit: unlimited.
CREATE TABLE user_pools (
  user_id       INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  pool_id       INTEGER NOT NULL REFERENCES traffic_pools(id) ON DELETE CASCADE,
  traffic_limit INTEGER,
  used_up       INTEGER NOT NULL DEFAULT 0,
  used_down     INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (user_id, pool_id)
) WITHOUT ROWID;

-- +goose Down
DROP TABLE user_pools;
DROP TABLE tariff_pools;
ALTER TABLE inbounds DROP COLUMN pool_id;
DROP TABLE traffic_pools;
