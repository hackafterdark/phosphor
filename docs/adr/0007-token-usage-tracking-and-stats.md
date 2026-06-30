# ADR-0007: Token Usage Tracking and Stats

## Status

Accepted

## Context

Users needed visibility into their token consumption patterns — daily usage, costs, and activity trends. Previously, token data lived only in the `sessions` table as cumulative counters (`prompt_tokens`, `completion_tokens`, `cost`), with no way to track usage over time or compare models.

This ADR documents the decision to introduce a dedicated `token_usage` table for per-session token tracking and a `phosphor stats` CLI command for generating usage reports.

## Decision

We introduced a `token_usage` table that records token consumption per session, paired with a `phosphor stats` command that generates an HTML report with multiple views of the data.

### What's Implemented

1. **Token usage table** (`internal/db/migrations/20260618182459_create_token_usage_table.sql`)

   - Schema: `id`, `session_id`, `model`, `provider`, `prompt_tokens`, `completion_tokens`, `cost`, `created_at`
   - Foreign key to `sessions(id)` with CASCADE delete
   - Backfill migration from existing `sessions` data
   - Indexes on `session_id` and `created_at` for query performance

2. **SQL queries** (`internal/db/sql/stats.sql`)

   - `GetTotalStats` — aggregate sessions, tokens, cost, messages
   - `GetUsageByDay` — daily token usage grouped by day
   - `GetUsageByDayRange` — daily usage with date range filter (supports `-30d`, etc.)
   - `GetUsageByModel` — message count grouped by model/provider (from `messages` table)
   - `GetUsageByHour` — session count per hour
   - `GetUsageByDayOfWeek` — token usage by day of week
   - `GetRecentActivity` — last 30 days of session/token/cost data
   - `GetAverageResponseTime` — average response time from `messages` table
   - `GetToolUsage` — tool call frequency from `messages.parts` JSON
   - `GetHourDayHeatmap` — session count by day-of-week × hour

3. **Stats CLI** (`internal/cmd/stats.go`)

   - `phosphor stats` command generates an HTML report
   - Reads from workspace `.phosphor/phosphor.db`
   - Opens browser automatically
   - Uses embedded HTML template, CSS, and JavaScript
   - Chart.js for client-side rendering
   - No external dependencies

4. **HTML report** (`internal/cmd/stats/index.html`, `index.css`, `index.js`)

   - Header with project name and username
   - Token usage chart (daily)
   - Activity trends
   - Model usage (message count only)
   - Tool usage breakdown
   - Hour/day heatmap
   - Responsive design with dark theme

### Consequences

**Positive:**
- Users can see their daily token consumption and costs
- Activity patterns reveal when and how often models are used
- Heatmap helps identify peak usage hours
- Report is self-contained (single HTML file, no server needed)
- Per-workspace storage matches Phosphor's existing data model
- Backfill migration ensures existing sessions have data

**Negative:**
- Model usage view only shows *message count* — not token totals per model
- No cache token tracking (cache_read_tokens, cache_creation_tokens)
- No cumulative usage chart
- No cost estimation breakdown by model
- No sidebar integration (report is only via CLI)

### Future Scope

The following features are planned for subsequent iterations:

1. **Per-model token aggregation** — Aggregate `prompt_tokens` and `completion_tokens` by model across all sessions. Replace the current message-count-based model view with actual token totals. This is the highest-impact improvement for answering "which model costs me the most?"

2. **Cache token tracking** — Add `cache_read_tokens` and `cache_creation_tokens` to the `token_usage` table. Requires changes to the provider layer to extract cache tokens from the API response.

3. **Cumulative usage chart** — Add a running total of tokens over time to the HTML report, showing growth trend across all sessions.

4. **Sidebar model usage panel** — Surface per-model token usage in the TUI sidebar (collapsible section, similar to context bar). Shows aggregated totals for the current workspace.

5. **Cost estimation per model** — Display estimated costs grouped by model using provider pricing configuration.

6. **Global aggregation** — Optional `--global` flag on `phosphor stats` to scan all workspaces and aggregate. Deferred until per-workspace is solid.

## Alternatives

### Extend the `sessions` table

Adding model-specific token columns directly to `sessions` was considered but rejected because a session can use multiple models (tool calls, fallbacks). Storing per-model data in a flat session row is awkward.

### Global stats database

Storing usage in `~/.local/share/phosphor/stats.db` would aggregate across all workspaces. Rejected because:

1. No precedent in Phosphor (all data is per-workspace)
2. Privacy concerns (cross-project data correlation)
3. Complexity (separate DB, migration system, lock management)
4. Monorepo ambiguity (shared vs. separate `.phosphor` dirs)

### Skip the backfill

Without the backfill migration, existing sessions would have zero token data. The backfill ensures historical sessions contribute to the report from day one.

## References

- `internal/db/migrations/20260618182459_create_token_usage_table.sql` — table creation
- `internal/db/sql/stats.sql` — all stats queries
- `internal/cmd/stats.go` — CLI command and data gathering
- `internal/cmd/stats/index.html` — HTML report template
- `.agents/docs/rfc/model-token-usage.md` — parent RFC
