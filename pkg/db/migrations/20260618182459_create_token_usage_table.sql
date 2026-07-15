-- +goose Up
CREATE TABLE IF NOT EXISTS token_usage (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL,
    model TEXT NOT NULL,
    provider TEXT NOT NULL,
    prompt_tokens INTEGER NOT NULL DEFAULT 0,
    completion_tokens INTEGER NOT NULL DEFAULT 0,
    cost REAL NOT NULL DEFAULT 0.0,
    created_at INTEGER NOT NULL,
    FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_token_usage_session_id ON token_usage(session_id);
CREATE INDEX IF NOT EXISTS idx_token_usage_created_at ON token_usage(created_at);

-- Backfill from existing sessions
INSERT INTO token_usage (id, session_id, model, provider, prompt_tokens, completion_tokens, cost, created_at)
SELECT 
    'backfill-' || id,
    id,
    'unknown',
    'unknown',
    prompt_tokens,
    completion_tokens,
    cost,
    created_at
FROM sessions
WHERE prompt_tokens > 0 OR completion_tokens > 0 OR cost > 0;

-- +goose Down
DROP INDEX IF EXISTS idx_token_usage_created_at;
DROP INDEX IF EXISTS idx_token_usage_session_id;
DROP TABLE IF EXISTS token_usage;
