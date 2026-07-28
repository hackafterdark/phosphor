-- +goose Up
-- Stores mermaid diagram syntax, keyed by session so they're scoped to a session.
CREATE TABLE IF NOT EXISTS diagrams (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id TEXT NOT NULL,
    syntax TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE CASCADE
);

-- +goose Down
DROP TABLE IF EXISTS diagrams;