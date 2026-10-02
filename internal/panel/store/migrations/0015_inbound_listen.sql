-- +goose Up
-- Inbounds behind a TCP proxy (GitHub issue #11): nginx stream or HAProxy holds the public
-- port and forwards by SNI to inbounds on 127.0.0.1. '' listens on every address.
ALTER TABLE inbounds ADD COLUMN listen TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE inbounds DROP COLUMN listen;
