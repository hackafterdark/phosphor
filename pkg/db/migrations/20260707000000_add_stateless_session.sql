-- +goose Up
ALTER TABLE sessions ADD COLUMN is_stateless INTEGER NOT NULL DEFAULT 0;
ALTER TABLE sessions ADD COLUMN service TEXT NOT NULL DEFAULT '';

-- Backfill existing sessions as stateful (named).
UPDATE sessions SET is_stateless = 0 WHERE is_stateless IS NULL;

-- +goose Down
ALTER TABLE sessions DROP COLUMN is_stateless;
ALTER TABLE sessions DROP COLUMN service;
