-- +goose Up
-- Remove FK constraint on session_id so diagrams can exist without a session.
CREATE TABLE IF NOT EXISTS diagrams_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id TEXT,
    syntax TEXT NOT NULL,
    created_at INTEGER NOT NULL
);
INSERT INTO diagrams_new (id, session_id, syntax, created_at)
    SELECT id, session_id, syntax, created_at FROM diagrams;
DROP TABLE diagrams;
ALTER TABLE diagrams_new RENAME TO diagrams;

-- +goose Down
DROP TABLE IF EXISTS diagrams;