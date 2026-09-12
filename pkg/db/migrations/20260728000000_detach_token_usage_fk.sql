-- +goose Up
-- Detach token usage from session lifetime, record sub-agent origin, and
-- track reasoning tokens.
--
-- token_usage previously declared session_id as NOT NULL with an
-- ON DELETE CASCADE foreign key, so pruning or deleting a session also
-- erased its recorded token usage and the usage reports lost their totals.
-- SQLite cannot alter a foreign key in place, so rebuild the table with a
-- nullable session_id and ON DELETE SET NULL. Usage rows survive their
-- session; a NULL session_id marks a row whose session has been removed.
--
-- is_subagent captures, at write time, whether the usage came from a
-- sub-agent (task/tool) session rather than the primary session. It is
-- derived from sessions.parent_session_id during the copy so historical rows
-- are attributed correctly, and it survives the session_id being nulled.
--
-- reasoning_tokens tracks provider-reported thinking/reasoning output
-- separately from completion_tokens so the reporting can show it without
-- inflating the "out" figure (whether a provider already folds reasoning
-- into its output count is provider specific, so it stays a separate series).
-- Historical rows backfill to 0 because the value was never persisted before.
--
-- token_usage is a leaf table (nothing references it), so the rebuild is
-- safe with foreign keys enabled and no PRAGMA toggling is required.
CREATE TABLE token_usage_new (
    id TEXT PRIMARY KEY,
    session_id TEXT,
    model TEXT NOT NULL,
    provider TEXT NOT NULL,
    prompt_tokens INTEGER NOT NULL DEFAULT 0,
    completion_tokens INTEGER NOT NULL DEFAULT 0,
    cost REAL NOT NULL DEFAULT 0.0,
    created_at INTEGER NOT NULL,
    is_subagent INTEGER NOT NULL DEFAULT 0,
    reasoning_tokens INTEGER NOT NULL DEFAULT 0,
    FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE SET NULL
);

INSERT INTO token_usage_new (
    id,
    session_id,
    model,
    provider,
    prompt_tokens,
    completion_tokens,
    cost,
    created_at,
    is_subagent,
    reasoning_tokens
)
SELECT
    tu.id,
    tu.session_id,
    tu.model,
    tu.provider,
    tu.prompt_tokens,
    tu.completion_tokens,
    tu.cost,
    tu.created_at,
    EXISTS (
        SELECT 1
        FROM sessions s
        WHERE s.id = tu.session_id
          AND s.parent_session_id IS NOT NULL
    ),
    0
FROM token_usage tu;

DROP TABLE token_usage;
ALTER TABLE token_usage_new RENAME TO token_usage;

CREATE INDEX IF NOT EXISTS idx_token_usage_session_id ON token_usage(session_id);
CREATE INDEX IF NOT EXISTS idx_token_usage_created_at ON token_usage(created_at);

-- +goose Down
-- Restore the original schema: NOT NULL session_id with ON DELETE CASCADE.
-- Rows orphaned by SET NULL cannot satisfy NOT NULL, so they are dropped on
-- rollback.
DELETE FROM token_usage WHERE session_id IS NULL;

CREATE TABLE token_usage_old (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL,
    model TEXT NOT NULL,
    provider TEXT NOT NULL,
    prompt_tokens INTEGER NOT NULL DEFAULT 0,
    completion_tokens INTEGER NOT NULL DEFAULT 0,
    cost REAL NOT NULL DEFAULT 0.0,
    created_at INTEGER NOT NULL,
    is_subagent INTEGER NOT NULL DEFAULT 0,
    reasoning_tokens INTEGER NOT NULL DEFAULT 0,
    FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE CASCADE
);

INSERT INTO token_usage_old (
    id,
    session_id,
    model,
    provider,
    prompt_tokens,
    completion_tokens,
    cost,
    created_at,
    is_subagent,
    reasoning_tokens
)
SELECT
    id,
    session_id,
    model,
    provider,
    prompt_tokens,
    completion_tokens,
    cost,
    created_at,
    is_subagent,
    reasoning_tokens
FROM token_usage;

DROP TABLE token_usage;
ALTER TABLE token_usage_old RENAME TO token_usage;

CREATE INDEX IF NOT EXISTS idx_token_usage_session_id ON token_usage(session_id);
CREATE INDEX IF NOT EXISTS idx_token_usage_created_at ON token_usage(created_at);
