# RFC: Per-Model Token Usage Tracking and Display

## Status

**Proposed**

## Summary

Add persistent, per-model token usage tracking (input tokens, output tokens,
cache read tokens, cache creation tokens) across all sessions within a
workspace, and surface the aggregated totals in the Phosphor UI so users can
see their lifetime model usage at a glance.

## Motivation

Phosphor already tracks `prompt_tokens` and `completion_tokens` at the **session**
level (the `sessions` table in `phosphor.db`). The `phosphor stats` CLI command
aggregates these into an HTML report, and the UI sidebar shows the current
session's context usage and cost.

However, there is no **per-model** lifetime view. Users who work across
multiple sessions with different models (e.g., `claude-sonnet-4.5` for coding,
`claude-haiku-3.5` for fast queries, `gpt-4.1` for OpenAI-specific tasks) have
no way to answer:

- "How many tokens have I used with model X total?"
- "Which model costs me the most?"
- "What's my total token budget across all sessions?"

This RFC proposes a lightweight, per-workspace solution.

## Storage Location: Per-Workspace vs. Global

### Decision: Per-workspace `.phosphor/phosphor.db`

**Recommendation: store in the per-workspace SQLite database** (the existing
`phosphor.db` inside each project's `.phosphor` directory).

### Rationale

The per-workspace approach aligns with Phosphor's existing data model:

| Aspect | Per-Workspace (`.phosphor/phosphor.db`) | Global (`~/.local/share/phosphor/`) |
|---|---|---|
| **Consistency** | Matches existing sessions, messages, files tables | New separate DB or separate table |
| **Data locality** | One DB per workspace, easy to reason about | Cross-workspace joins needed |
| **Migration** | Simple: one new table, one migration file | Requires global DB setup, lock management |
| **Privacy** | Project-local, no cross-project data leakage | Aggregated data leaves project boundaries |
| **Server mode** | Works per-workspace; server aggregates naturally | Requires server to collect from all workspaces |
| **Existing pattern** | `phosphor stats` already reads from this DB | No precedent for global stats |
| **Cleanup** | `rm -rf .phosphor` clears everything | Manual cleanup of global state |
| **Monorepo** | Shared `.phosphor` across sub-packages | Global view across monorepo sub-packages |

### Why not global?

A global view (one DB in `~/.local/share/phosphor/`) would aggregate across all
workspaces. This has some appeal for power users who want a "total lifetime"
view, but:

1. **No precedent** — Phosphor stores all persistent data per-workspace.
2. **Privacy** — Global aggregation means Phosphor implicitly correlates all
   projects.
3. **Complexity** — Requires a separate DB connection pool, migration system,
   and potentially a server-side component to aggregate across workspaces.
4. **Monorepo ambiguity** — In a monorepo, should each sub-package have its
   own `.phosphor` with its own stats? Or should they share? The per-workspace
   model already handles this via the parent-directory search.

### Future: Global aggregation (optional follow-up)

A global stats view could be added later as a `phosphor stats --global` flag that
scans all known workspaces and aggregates. This is a natural extension, not a
prerequisite.

## Design

### Database Changes

Add a new table to the per-workspace `phosphor.db`:

```sql
-- internal/db/migrations/YYYYMMDDHHMMSS_add_model_usage_table.sql

-- +goose Up
CREATE TABLE IF NOT EXISTS model_usage (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id TEXT NOT NULL,
    model TEXT NOT NULL,
    provider TEXT NOT NULL,
    input_tokens INTEGER NOT NULL DEFAULT 0 CHECK (input_tokens >= 0),
    output_tokens INTEGER NOT NULL DEFAULT 0 CHECK (output_tokens >= 0),
    cache_read_tokens INTEGER NOT NULL DEFAULT 0 CHECK (cache_read_tokens >= 0),
    cache_creation_tokens INTEGER NOT NULL DEFAULT 0 CHECK (cache_creation_tokens >= 0),
    created_at INTEGER NOT NULL,
    FOREIGN KEY (session_id) REFERENCES sessions (id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_model_usage_session_id ON model_usage (session_id);
CREATE INDEX IF NOT EXISTS idx_model_usage_model ON model_usage (model);
CREATE INDEX IF NOT EXISTS idx_model_usage_provider ON model_usage (provider);

-- +goose Down
DROP INDEX IF EXISTS idx_model_usage_session_id;
DROP INDEX IF EXISTS idx_model_usage_model;
DROP INDEX IF EXISTS idx_model_usage_provider;
DROP TABLE IF EXISTS model_usage;
```

### Data Flow

1. **Recording** — In `internal/agent/agent.go`, at the end of each LLM call
   (after `OnStepFinish` or `OnTurnComplete`), extract `stepResult.Usage`
   (or `resp.TotalUsage`) and insert a row into `model_usage` for the current
   session and model. This is a simple `INSERT` — no need for upsert since each
   session-model pair is unique.

2. **Existing token tracking preserved** — The current `sessions.prompt_tokens`
   and `sessions.completion_tokens` columns remain unchanged. The new table
   provides **additional granularity** (per-model, per-call, cache tokens).

3. **Backfill not needed** — The table is append-only from this point forward.
   Existing sessions have no rows in `model_usage`, which is fine — the UI
   query just sums what's available.

### Multi-Model Example

A single workspace may use multiple models across and within sessions. Each
LLM call inserts one row, so the table naturally handles model switches:

| session_id | model | provider | input | output | cache_read | cache_create |
|---|---|---|---|---|---|---|
| sess-aaa | claude-sonnet-4.5 | anthropic | 5000 | 1200 | 3000 | 0 |
| sess-aaa | claude-haiku-3.5 | anthropic | 800 | 200 | 100 | 0 |
| sess-bbb | claude-sonnet-4.5 | anthropic | 12000 | 3400 | 8000 | 0 |
| sess-bbb | gpt-4.1-mini | openai | 2000 | 500 | 0 | 0 |

The `GetModelUsageTotals` query groups by `model, provider` and sums across
all rows, producing:

| model | provider | input | output | cache_read | calls |
|---|---|---|---|---|---|
| claude-sonnet-4.5 | anthropic | 17,000 | 4,600 | 11,000 | 2 |
| claude-haiku-3.5 | anthropic | 800 | 200 | 100 | 1 |
| gpt-4.1-mini | openai | 2,000 | 500 | 0 | 1 |

This is exactly what the sidebar panel would display — one row per model,
aggregated across every session in the workspace.

### SQL Queries (sqlc)

Add to `internal/db/sql/model_usage.sql`:

```sql
-- name: RecordModelUsage :exec
INSERT INTO model_usage (session_id, model, provider, input_tokens, output_tokens, cache_read_tokens, cache_creation_tokens, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, strftime('%s', 'now'));

-- name: GetModelUsageTotals :many
SELECT
    model,
    provider,
    SUM(input_tokens) AS total_input_tokens,
    SUM(output_tokens) AS total_output_tokens,
    SUM(cache_read_tokens) AS total_cache_read_tokens,
    SUM(cache_creation_tokens) AS total_cache_creation_tokens,
    SUM(input_tokens + output_tokens + cache_read_tokens + cache_creation_tokens) AS total_tokens,
    COUNT(*) AS call_count
FROM model_usage
GROUP BY model, provider
ORDER BY total_tokens DESC;

-- name: GetModelUsageBySession :many
SELECT
    model,
    provider,
    input_tokens,
    output_tokens,
    cache_read_tokens,
    cache_creation_tokens,
    input_tokens + output_tokens + cache_read_tokens + cache_creation_tokens AS total_tokens
FROM model_usage
WHERE session_id = ?
ORDER BY created_at ASC;

-- name: GetTotalTokenUsage :one
SELECT
    SUM(input_tokens) AS total_input_tokens,
    SUM(output_tokens) AS total_output_tokens,
    SUM(cache_read_tokens) AS total_cache_read_tokens,
    SUM(cache_creation_tokens) AS total_cache_creation_tokens,
    SUM(input_tokens + output_tokens + cache_read_tokens + cache_creation_tokens) AS total_tokens
FROM model_usage;

-- name: GetModelUsageByDay :many
SELECT
    date(created_at, 'unixepoch') AS day,
    model,
    provider,
    SUM(input_tokens) AS total_input_tokens,
    SUM(output_tokens) AS total_output_tokens,
    SUM(cache_read_tokens) AS total_cache_read_tokens,
    SUM(cache_creation_tokens) AS total_cache_creation_tokens,
    SUM(input_tokens + output_tokens + cache_read_tokens + cache_creation_tokens) AS total_tokens,
    COUNT(*) AS call_count
FROM model_usage
GROUP BY day, model, provider
ORDER BY day ASC;

-- name: GetCumulativeUsageByDay :many
SELECT
    date(created_at, 'unixepoch') AS day,
    SUM(input_tokens) AS daily_input_tokens,
    SUM(output_tokens) AS daily_output_tokens,
    SUM(input_tokens + output_tokens + cache_read_tokens + cache_creation_tokens) AS daily_total_tokens,
    SUM(SUM(input_tokens + output_tokens + cache_read_tokens + cache_creation_tokens))
        OVER (ORDER BY date(created_at, 'unixepoch')) AS cumulative_total_tokens
FROM model_usage
GROUP BY day
ORDER BY day ASC;
```

### UI Integration

#### Sidebar Model Usage Panel

Add a new collapsible section in the sidebar (below model info, below goal
info) showing per-model token usage:

```
┌─────────────────────────────────┐
│ MODEL USAGE                     │
│                                 │
│ claude-sonnet-4.5               │
│   in:  1.2M   out: 340K        │
│   cache: 890K                    │
│                                 │
│ gpt-4.1-mini                    │
│   in:  45K    out: 12K         │
│                                 │
│ claude-haiku-3.5                │
│   in:  230K   out: 67K         │
│                                 │
│ ─────────────────              │
│ total: 1.5M in / 419K out      │
└─────────────────────────────────┘
```

The section is collapsible (like the existing file/LSP/MCP sections) and only
renders when `model_usage` has data for the current workspace.

#### Stats Page Charts

The `phosphor stats` HTML page should include:

1. **Cumulative token usage line chart** — `GetCumulativeUsageByDay` produces
   a running total of tokens per day. This is the primary "lifetime usage"
   chart, showing growth over time.

2. **Per-model token breakdown** — `GetModelUsageByDay` grouped by model,
   rendered as a stacked bar chart (input / output / cache read / cache
   creation) per day, or a line chart with one line per model.

3. **Model usage table** — below the charts, the existing `GetModelUsageTotals`
   results displayed as a sortable table with columns: model, provider,
   input tokens, output tokens, cache tokens, total tokens, call count.

The existing stats HTML template (`internal/cmd/stats/index.html`) and its
JavaScript (`index.js`) already use client-side charting. The same approach
should be reused — no new JS library needed.

#### Token Formatting

Reuse the existing `common.FormatTokensAndCost()` function (already in
`internal/ui/common/elements.go`) for consistent token display with K/M/B
suffixes.

### Stats CLI Integration

Update `phosphor stats` to include model-level token breakdowns. The existing
`GetUsageByModel` query only counts messages — replace or augment it with
the new `GetModelUsageTotals` query that sums actual token counts.

## Implementation Checklist

- [ ] **DB migration** — Add `model_usage` table in
  `internal/db/migrations/`
- [ ] **sqlc queries** — Add `model_usage.sql` with `RecordModelUsage`,
  `GetModelUsageTotals`, `GetModelUsageBySession`, `GetTotalTokenUsage`
- [ ] **sqlc generate** — Run `sqlc generate` to produce Go code
- [ ] **Agent recording** — In `internal/agent/agent.go`, record usage after
  each LLM call (in `OnStepFinish` or the turn completion handler)
- [ ] **Stats SQL** — Add `GetModelUsageTotals` to `internal/db/sql/stats.sql`
  (or use the model_usage.sql version)
- [ ] **Stats CLI** — Update `internal/cmd/stats.go` to include per-model
  token totals in the HTML report
- [ ] **UI model** — Add model usage data fetching in the UI model
  (`internal/ui/model/ui.go`)
- [ ] **UI sidebar** — Add the model usage panel to the sidebar
  (`internal/ui/model/sidebar.go`)
- [ ] **UI styles** — Add sidebar styles for the model usage section
  (`internal/ui/styles/styles.go`)
- [ ] **Stats charts** — Add cumulative token chart and per-model breakdown
  to `internal/cmd/stats/index.html` / `index.js`
- [ ] **Keybinding** — Add toggle keybinding (`internal/ui/model/keys.go`)
- [ ] **Help text** — Update help overlay to document the new binding
- [ ] **Tests** — Add unit tests for the recording logic and SQL queries

## Edge Cases

- **Zero usage** — If no LLM calls have been made, the panel is hidden.
- **Multiple models per session** — Each model call gets its own row; the UI
  aggregates by model.
- **Session deletion** — CASCADE delete removes `model_usage` rows automatically.
- **No model column on messages** — The `model_usage` table stores the model
  name directly at insert time, so it doesn't depend on the messages table.
- **Provider changes** — The `provider` column is stored per-row; if a provider
  name changes, historical data is preserved.
- **Backward compatibility** — Existing databases have no `model_usage` rows.
  The UI gracefully handles empty results.

## Open Questions

1. **Keybinding** — What key should toggle the model usage panel? `ctrl+u` is
   available but could conflict with future features. `ctrl+shift+u` is less
   likely to conflict but is less discoverable.
2. **Cost estimation** — Should we also display estimated cost per model? This
   would require looking up the model's pricing from the config.
3. **Session-level granularity** — Should the model usage panel show a
   per-session breakdown (expandable rows) or just the aggregate?
4. **Global aggregation** — Should we plan for a `--global` flag on `phosphor
   stats` from the start, or leave it as a future enhancement?
5. **Cache token display** — Should cache read/creation tokens be shown
   separately or combined with input tokens in the UI?

## Alternatives Considered

1. **Extend the `messages` table** — Add token columns to `messages`.
   - **Cons**: Messages already have a complex `parts` JSON blob; adding
     token columns would duplicate session-level tracking. The session-level
     approach is simpler and already works.
   - **Decision**: Separate `model_usage` table is cleaner.

2. **Store in `sessions` table** — Add model-specific token columns to
   `sessions`.
   - **Cons**: A session can use multiple models (tool calls, fallbacks).
     Storing per-model data in a flat session row is awkward.
   - **Decision**: Separate table handles one-to-many model-per-session.

3. **Global stats DB** — Store usage in `~/.local/share/phosphor/stats.db`.
   - **Cons**: No precedent in Phosphor, privacy concerns, complexity.
   - **Decision**: Per-workspace first; global can be added later.
