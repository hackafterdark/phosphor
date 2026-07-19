-- +goose Up
-- Add is_pinned column to protect sessions from bulk deletion.
ALTER TABLE sessions ADD COLUMN is_pinned INTEGER NOT NULL DEFAULT 0;

-- Backfill: ensure existing sessions default to not pinned.
UPDATE sessions SET is_pinned = 0 WHERE is_pinned IS NULL;

-- +goose Down
ALTER TABLE sessions DROP COLUMN is_pinned;