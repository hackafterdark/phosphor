# ADR-0001: Context Window Token Tracking and Auto-Summarization

## Status

Proposed

## Context

Phosphor's TUI displayed a context window usage percentage in the header and sidebar panels. The old implementation computed this percentage from `session.PromptTokens + session.CompletionTokens` — cumulative counters that the API returns at the end of each request. This caused two problems:

1. **Stale display**: The UI always showed the token count from the *previous* request. After a new user message was sent but before the API responded, the displayed percentage was out of date.

2. **Auto-summarization was unreliable**: The auto-summarization trigger used the same cumulative counters (`PromptTokens + CompletionTokens`) to decide whether to summarize. This meant a large incoming prompt could push the context window past the model's limit before the next API response corrected the counters, resulting in 400 Bad Request errors from the provider.

3. **Not user-configurable**: The summarization threshold used a fixed buffer strategy (different thresholds for large vs. small context windows) rather than a percentage-based approach the user could tune.

## Decision

We introduced a new `CurrentTokens` field on the session model that tracks the *estimated* token count of the active conversation context in real time, independent of the API's cumulative counters.

### Changes

1. **New `CurrentTokens` field on `Session`** (`internal/db/models.go`, `internal/session/session.go`, `internal/db/sql/sessions.sql`, migration `20260608000000_add_current_tokens.sql`)

   - Stored in SQLite as `INTEGER NOT NULL DEFAULT 0`.
   - Propagated through all sqlc-generated queries (insert, select, update).

2. **Real-time estimation at session start** (`internal/agent/agent.go:263`)

   - Before each API request, `estimateMessageTokensForMessage()` walks all session messages and sums token estimates (text via `approxTokenCount`, tool calls, media, etc.).
   - The result is saved to `session.CurrentTokens`.

3. **Post-response update** (`internal/agent/agent.go:573`)

   - After the API response, `CurrentTokens` is set to `PromptTokens + CompletionTokens` from the API's usage report, keeping the estimate in sync with the provider's actual count.

4. **Percentage-based auto-summarization threshold** (`internal/config/config.go`)

   - New `SummarizeThreshold` config option (default 0.8, range 0–1).
   - Values of 0 or negative fall back to the default (80%).
   - Replaces the old fixed-buffer strategy with `float64(CurrentTokens) / float64(ContextWindow) >= threshold`.
   - Wired through from config to the agent in `coordinator.go` and `agentic_fetch_tool.go`.

5. **Hard overflow guard** (`internal/agent/agent.go:304-325`)

   - After adding the user message (before sending to the API), total tokens are re-estimated.
   - If `totalTokens > ContextWindow`, summarization is forced regardless of the threshold setting.
   - This prevents 400 errors from context overflow when a large prompt would otherwise slip past the soft threshold.

6. **UI fallback for zero `CurrentTokens`** (`internal/ui/model/header.go`, `internal/ui/model/sidebar.go`)

   - Both the header percentage and the sidebar context bar fall back to `PromptTokens + CompletionTokens` when `CurrentTokens == 0`.
   - This handles two cases: new sessions before the first request (agent hasn't estimated yet), and existing sessions before the migration (DB column default is 0).
   - Once the agent runs, `CurrentTokens` is populated and the UI uses it directly.

### Consequences

**Positive:**
- The UI token count stays accurate through the full request cycle — it reflects the actual context window usage at the moment of display.
- Auto-summarization now triggers based on the real context usage, not stale cumulative counters.
- Users can tune the summarization trigger via `summarize_threshold` in `phosphor.json`.
- The hard overflow guard prevents 400 errors from context overflow that the soft threshold alone couldn't catch.
- The UI fallback (`CurrentTokens == 0 → PromptTokens + CompletionTokens`) ensures the display never shows 0% for sessions before the first request or before the migration.

**Negative:**
- `CurrentTokens` is an *estimate* (based on `approxTokenCount`), not the provider's exact count. The estimate may differ from the API's actual token count, especially for complex content (media, reasoning blocks).
- The migration adds a column to the `sessions` table, which is a one-time schema change.
- The UI fallback means the displayed value can still be stale (one request behind) in the edge case where `CurrentTokens` hasn't been populated yet. This is acceptable for now but could be improved with a backfill migration.

**Open Questions:**
- The estimate could be more accurate by using the provider's token counter directly when available (fantasy provides token counts per step).
- A startup backfill migration to set `CurrentTokens = PromptTokens + CompletionTokens` for all existing sessions would eliminate the fallback entirely and make the UI accurate from the first render.

## Alternatives

### LLM/User-Driven Compaction via `new_session` Tool (PR #2333)

An alternative approach was proposed in [PR #2333](https://github.com/hackafterdark/phosphor/pull/2333) by @taoeffect. It introduced:

1. A **`new_session` tool** that allows the LLM to create a fresh session when context gets full, carrying forward a summary of progress and remaining work.
2. A **`<context_status>` block** injected into the system prompt each turn, so the model can track its own context usage and proactively call `new_session` (default at ~75% context remaining).
3. A **compaction method toggle** (auto vs LLM/user-driven) selectable via the command palette, persisting across sessions in the data config.
4. A **UI renderer** for the tool's pending/completed states and a compaction method switching menu.

### Why This Was Rejected

We chose the simpler auto-summarization approach for now because:

- **Less surface area**: The `new_session` approach requires a new tool, new config fields (`CompactionMethod`), new UI components (toggle, context status renderer), and changes to the system prompt injection layer. Our approach only adds a config option and a few lines in the agent loop.
- **No LLM dependency**: The auto-summarization is fully automated and doesn't rely on the model to recognize context pressure and decide to call a tool. The model can fail to trigger the tool (as seen in PR #2333's own fix: `compactionFlags()` was disabling the auto-summarize safety net in LLM mode).
- **No session fragmentation**: `new_session` creates entirely new sessions, which fragments conversation history and file version tracking (PR #2333 had to fix a UNIQUE constraint violation in file history as a result). Auto-summarization keeps everything in the same session.
- **Faster iteration**: This approach is simpler to test, debug, and iterate on. The `SummarizeThreshold` config option already gives users control over when summarization triggers.

The LLM/user-driven approach may be worth revisiting later as a complementary feature — for example, allowing the model to explicitly request compaction when it detects a complex multi-step task that would benefit from a clean slate, even before hitting the context limit.

## References

- `internal/agent/agent.go` — token estimation, auto-summarization, hard guard
- `internal/config/config.go` — `SummarizeThreshold` option
- `internal/session/session.go` — `CurrentTokens` field
- `internal/db/migrations/20260608000000_add_current_tokens.sql` — schema migration
- `internal/ui/model/header.go` — header percentage display
- `internal/ui/model/sidebar.go` — sidebar context bar display
