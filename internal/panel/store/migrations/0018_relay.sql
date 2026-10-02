-- +goose Up
CREATE TABLE IF NOT EXISTS node_relay (
  node_id     INTEGER PRIMARY KEY REFERENCES nodes(id) ON DELETE CASCADE,
  enabled     INTEGER NOT NULL DEFAULT 0,
  protocol    TEXT NOT NULL DEFAULT 'vless',
  server      TEXT NOT NULL DEFAULT '',
  port        INTEGER NOT NULL DEFAULT 443,
  uuid        TEXT NOT NULL DEFAULT '',
  flow        TEXT NOT NULL DEFAULT '',
  tls         INTEGER NOT NULL DEFAULT 1,
  sni         TEXT NOT NULL DEFAULT '',
  public_key  TEXT NOT NULL DEFAULT '',
  short_id    TEXT NOT NULL DEFAULT '',
  spider_x    TEXT NOT NULL DEFAULT '',
  fingerprint TEXT NOT NULL DEFAULT 'firefox',
  inbounds    TEXT NOT NULL DEFAULT '[]',
  created_at  INTEGER NOT NULL,
  updated_at  INTEGER NOT NULL
);

-- +goose Down
DROP TABLE node_relay;
