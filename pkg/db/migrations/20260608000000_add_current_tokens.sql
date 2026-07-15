-- +goose Up
ALTER TABLE sessions ADD COLUMN current_tokens INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE sessions DROP COLUMN current_tokens;
