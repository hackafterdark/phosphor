# Context Window Management Fix — Session Notes

## Date

6/8/2026

## Issues Discussed

### 1. Auto-Summarization Was Broken

**Problem:** The auto-summarization feature was using cumulative session token counters (`PromptTokens + CompletionTokens`) which only ever increased. After the first summarization cycle, these values would exceed the context window threshold on every single turn, causing:

- Auto-summarization to fire on every request (unnecessarily)
- The UI display to show &gt;100% context usage
- The displayed token count to keep growing past the actual context window

**Root cause:** `PromptTokens` and `CompletionTokens` in the session record are cumulative counters (for billing/estimation), not snapshots of the active context window. The auto-summarization check at `internal/agent/agent.go:558` compared these cumulative values against the context window size.

### 2. No Pre-Request Context Check

**Problem:** The auto-summarization check was only a `StopWhen` condition — it fired *after* the model had already been called. This meant:

- A large user prompt could push the context past the model's limit
- vLLM would reject the request with "Bad Request" (context overflow)
- The agent would crash or enter a failure loop

### 3. No Configurable Threshold

**Problem:** The summarization thresholds were hardcoded:

- Context window &gt; 200k: summarize when 20k tokens remain
- Context window ≤ 200k: summarize when 20% remains

Users had no way to adjust when summarization should kick in.

### 4. UI Display Was Misleading

**Problem:** The right panel showed cumulative tokens (e.g., "26.1K") as if it were current context usage, and the percentage could exceed 100%. Users couldn't tell actual context window usage from cumulative session totals.

## Changes Made

### 1. New DB Field: `current_tokens`

**Migration:** `internal/db/migrations/20260608000000_add_current_tokens.sql`

```sql
ALTER TABLE sessions ADD COLUMN current_tokens INTEGER NOT NULL DEFAULT 0;
```

**Updated SQL queries:** `internal/db/sql/sessions.sql`

- `CreateSession` — inserts `current_tokens` with default 0
- `UpdateSession` — includes `current_tokens` in the SET clause

**Generated Go code:** `internal/db/models.go`, `internal/db/sessions.sql.go`

- Added `CurrentTokens int64` to `Session` struct
- Updated all query functions to read/write `current_tokens`

**Session service:** `internal/session/session.go`

- Added `CurrentTokens int64` to `Session` struct
- Updated `Save()` to pass `CurrentTokens` to DB
- Updated `fromDBItem()` to read `CurrentTokens` from DB

### 2. Config Option: `summarize_threshold`

**File:** `internal/config/config.go`

```go
SummarizeThreshold float64 `json:"summarize_threshold,omitempty" jsonschema:"description=Percentage of context window at which to trigger auto-summarization (0-100,default=80),default=80"`
```

Users can set this in `phosphor.json`:

```json
{
  "options": {
    "summarize_threshold": 75
  }
}
```

### 3. Agent: Current Context Token Tracking

**File:** `internal/agent/agent.go`

Added `summarizeThreshold` field to `sessionAgent` and `SessionAgentOptions`.

**New helper functions:**

- `shouldSummarize(session, model, threshold, disabled)` — returns true if context usage &gt;= threshold
- `estimateMessageTokensForMessage(msgs)` — estimates tokens from active `message.Message` list
- `estimateMessagePartTokensForMessage(part)` — estimates tokens per content part
- `estimateMediaTokensForMessage(mediaType, text, dataBytes)` — estimates tokens for media

**Pre-request check in `Run()`:**

```go
// 1. Compute current context tokens from active messages
currentTokens := estimateMessageTokensForMessage(msgs)
if currentSession.CurrentTokens != currentTokens {
    currentSession.CurrentTokens = currentTokens
    a.sessions.Save(ctx, currentSession)
}

// 2. Check threshold before sending request
if shouldSummarize(currentSession, largeModel, a.summarizeThreshold, a.disableAutoSummarize) {
    a.Summarize(ctx, call.SessionID, opts)
    // Re-fetch session and messages after summarization
}
```

**Updated `StopWhen` check:**

Uses `CurrentTokens` (not cumulative) with the configurable threshold percentage:

```go
percentage := float64(tokens) / float64(cw) * 100
if percentage >= a.summarizeThreshold && !a.disableAutoSummarize {
    shouldSummarize = true
    return true
}
```

**Updated `Summarize()`:**

After summarization, sets `CurrentTokens` to the truncated context size:

```go
currentSession.CurrentTokens = currentSession.CompletionTokens + approxTokenCount(summaryMessage.Content().Text)
```

### 4. Display: Use `CurrentTokens`

**Sidebar:** `internal/ui/model/sidebar.go`

```go
ContextUsed: m.session.CurrentTokens,  // was: m.session.CompletionTokens + m.session.PromptTokens
```

**Header:** `internal/ui/model/header.go`

```go
percentage := (float64(session.CurrentTokens) / float64(model.ContextWindow)) * 100
```

### 5. Config Propagation

**Coordinator:** `internal/agent/coordinator.go`

```go
SummarizeThreshold: c.cfg.Config().Options.SummarizeThreshold,
```

**Agentic fetch tool:** `internal/agent/agentic_fetch_tool.go`

```go
SummarizeThreshold: c.cfg.Config().Options.SummarizeThreshold,
```

## Known Issues / Potential Problems

### Bad Request Responses After Changes

After applying these changes, vLLM started returning "Bad Request" errors on `/v1/chat/completions`. Two possible causes:

1. **Token estimation is undercounting:** The `estimateMessageTokensForMessage()` function uses a rough chars/4 heuristic. If it undercounts the actual context size, the pre-request check may not trigger summarization when it should, causing the request to exceed vLLM's context limit.
2. **Token estimation is overcounting:** If it overcounts, summarization fires too aggressively, potentially creating empty or near-empty summaries that still cause issues.
3. **vLLM context limit mismatch:** The configured `context_window` (262144) may not match vLLM's actual model context limit. vLLM may have a smaller effective limit (e.g., 32768 or 65536 tokens) despite the config saying 262144.

### Debugging Steps for Next Session

1. **Check vLLM logs** for the exact error message on the Bad Request — it should indicate context overflow or invalid parameters.
2. **Verify token estimation accuracy:**
  - Add debug logging in `estimateMessageTokensForMessage()` to see computed values
  - Compare against vLLM's actual token counts in response headers
  - The chars/4 heuristic is rough — consider using a proper tokenizer
3. **Check context window mismatch:**
  - The config says 262144 but vLLM may enforce a smaller limit
  - Try setting `summarize_threshold: 50` to trigger summarization earlier
  - Verify the actual model context limit in vLLM config
4. **Check the `current_tokens` values in the DB:**
  - Query the sessions table to see if `current_tokens` is being updated correctly
  - Compare against `prompt_tokens` and `completion_tokens`
5. **Check for edge cases in message estimation:**
  - Tool call inputs/outputs may not be estimated accurately
  - File attachments may have different token counts
  - The `estimateMessagePartTokensForMessage()` switch may be missing some variant types

## Files Changed


| File                                                           | Change                                           |
| -------------------------------------------------------------- | ------------------------------------------------ |
| `internal/db/migrations/20260608000000_add_current_tokens.sql` | **New** — DB migration                           |
| `internal/db/sql/sessions.sql`                                 | Updated SQL queries                              |
| `internal/db/models.go`                                        | Added `CurrentTokens` field                      |
| `internal/db/sessions.sql.go`                                  | Regenerated with `current_tokens`                |
| `internal/session/session.go`                                  | Added `CurrentTokens` to service layer           |
| `internal/config/config.go`                                    | Added `SummarizeThreshold` option                |
| `internal/agent/agent.go`                                      | Core logic: tracking, pre-request check, helpers |
| `internal/agent/coordinator.go`                                | Pass `SummarizeThreshold` to agent               |
| `internal/agent/agentic_fetch_tool.go`                         | Pass `SummarizeThreshold` to fetch sub-agent     |
| `internal/ui/model/sidebar.go`                                 | Display uses `CurrentTokens`                     |
| `internal/ui/model/header.go`                                  | Display uses `CurrentTokens`                     |


## Rollback Plan

If the changes cause issues, the minimal rollback is:

1. Revert `internal/agent/agent.go` — remove the pre-request check and helper functions, restore the original `StopWhen` logic
2. Revert `internal/ui/model/sidebar.go` and `header.go` — restore `CompletionTokens + PromptTokens`
3. The DB migration is additive (new column) so it doesn't need rollback
4. The config option can be left in place with the default value (80)

## References

[https://github.com/hackafterdark/phosphor/issues/2213](https://github.com/hackafterdark/phosphor/issues/2213)

[https://github.com/hackafterdark/phosphor/issues/824](https://github.com/hackafterdark/phosphor/issues/824)



&nbsp;

&nbsp;