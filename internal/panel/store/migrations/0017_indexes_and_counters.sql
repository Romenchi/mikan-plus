-- +goose Up
-- The tables below are keyed by (user_id, time) and read or pruned by time alone, which
-- the primary key cannot serve: the dashboard, the top users and every prune scanned the
-- whole table (a year of 10 000 users is millions of rows). One index per such filter.
CREATE INDEX traffic_hourly_hour ON traffic_hourly(hour);
CREATE INDEX traffic_daily_day ON traffic_daily(day);
CREATE INDEX devices_last_seen ON devices(last_seen);
CREATE INDEX audit_log_ts ON audit_log(ts);

-- devices.client was never written: the user agent is not known where devices are noted.
ALTER TABLE devices DROP COLUMN client;

-- A slot is named after a number (s000123), and the node keys its counters and the panel
-- its inbound_reach rows by that name. The next number was the largest id plus one, so once
-- burned slots at the top were purged, new slots got the names of the old ones and
-- inherited their counters. The last number handed out is kept here and only grows.
CREATE TABLE slot_counter (
  id   INTEGER PRIMARY KEY CHECK (id = 1),
  last INTEGER NOT NULL
);
INSERT INTO slot_counter (id, last) SELECT 1, CAST(coalesce(max(id), 0) AS INTEGER) FROM slots;

-- +goose Down
DROP TABLE slot_counter;
ALTER TABLE devices ADD COLUMN client TEXT NOT NULL DEFAULT '';
DROP INDEX audit_log_ts;
DROP INDEX devices_last_seen;
DROP INDEX traffic_daily_day;
DROP INDEX traffic_hourly_hour;
