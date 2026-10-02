-- +goose Up
-- Cascades: an inbound may leave the internet through another node of the panel
-- (client → node A → node B → internet). B then runs a hidden relay listener with a key
-- per source node; the relay's own traffic may go on direct, through B's WARP or to a
-- further node.
ALTER TABLE inbounds ADD COLUMN exit_node_id INTEGER REFERENCES nodes(id) ON DELETE SET NULL; -- set: outbound is ignored

CREATE TABLE node_relays (
  node_id      INTEGER PRIMARY KEY REFERENCES nodes(id) ON DELETE CASCADE,
  port         TEXT NOT NULL,
  config       TEXT NOT NULL,                                    -- VLESS REALITY template (YAML)
  outbound     TEXT NOT NULL DEFAULT 'direct' CHECK (outbound IN ('direct', 'warp')),
  exit_node_id INTEGER REFERENCES nodes(id) ON DELETE SET NULL,  -- set: the relay goes on to that node
  created_at   INTEGER NOT NULL
);

CREATE TABLE relay_users (
  exit_node_id INTEGER NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
  src_node_id  INTEGER NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
  uuid         TEXT NOT NULL,
  PRIMARY KEY (exit_node_id, src_node_id)
) WITHOUT ROWID;

-- +goose Down
DROP TABLE relay_users;
DROP TABLE node_relays;
ALTER TABLE inbounds DROP COLUMN exit_node_id;
