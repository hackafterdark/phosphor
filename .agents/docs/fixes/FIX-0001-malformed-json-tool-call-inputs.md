# FIX-0001: Malformed JSON in Tool Call Inputs and Provider Options Causes Persistent 400 Bad Request

## Status

**Applied** — `sanitizeJSONInput` validates output and falls back to `{}` on failure; coordinator has 3-tier retry with tool call strip fallback; `go-jsons` alpha dependency replaced with custom `jsonmerge` package; enhanced error logging throughout.

## Problem

Two separate JSON-related issues caused persistent `400 Bad Request` errors that abandoned sessions:

1. **Malformed tool call inputs** — After a model produces a tool call with trailing garbage in its JSON input (e.g. `{"command": "ls"} }`), the stored tool call input reaches the provider on subsequent turns, causing a `400 Bad Request: Extra data: line 1 column 101 (char 100)` error.

2. **Malformed provider options** — The `go-jsons` alpha library (`github.com/qjebbs/go-jsons v1.0.0-alpha.5`) produced malformed JSON when merging provider options (concatenated objects instead of deep-merged), causing the same "Extra data" errors from the LLM provider.

## Why It Occurred

The problem has two root causes:

1. **`sanitizeJSONInput` did not validate its output.** It found the matching `}`/`]` that balanced the braces, but never verified the resulting string was actually valid JSON. Models can produce balanced-but-invalid JSON (e.g. `{command: "ls"}` with unquoted keys, or `{"key": "value",}` with trailing commas). The sanitized output was stored in the message and replayed back to the provider unchanged.

2. **The coordinator's single retry replayed the same state.** When the first attempt failed with a 400, the retry sent the same conversation history with the same malformed tool call input. If the sanitized input was still invalid, the retry failed identically, wasting a turn and abandoning the session.

3. **`go-jsons` alpha library produced malformed JSON.** The library concatenated JSON objects instead of deep-merging them, producing output like `{"a":1}{"b":2}` instead of `{"a":1,"b":2}`. This caused the provider to reject requests with "Extra data" errors.

## When Users Encounter It

Users hit this when:

- Using models with vLLM's `--tool-call-parser qwen3_xml` (or similar parsers), which are known to produce trailing `}`/`]`/text after tool call JSON
- The model produces unquoted keys or other non-standard JSON in tool call arguments
- The conversation has accumulated tool calls and the malformed one is replayed on every subsequent turn
- The error manifests as `bad request: Extra data: line 1 column X (char Y)` in logs
- Provider options are configured in `phosphor.json` (model-level, provider-level, or catwalk-level)

## Fix

### `sanitizeJSONInput` validation and fallback (`internal/agent/agent.go`)

After stripping trailing characters, the function calls `json.Valid()` on the candidate. If the result is not valid JSON (e.g. unquoted keys, trailing commas, single quotes, or no closing brace at all), the function returns a minimal valid JSON object (`"{}"`) instead of the original malformed string. This ensures the retry always sends valid JSON to the provider, allowing the model to recover without needing the strip fallback in most cases.

### 3-tier retry (`internal/agent/coordinator.go`)

1. **Tier 1:** First attempt — stores sanitized tool call input
2. **Tier 2:** Retry with same state — the stored input is now sanitized (or `{}` if sanitization failed), so the provider should accept it
3. **Tier 3:** Strip last tool call — if tier 2 also fails with a 400, the last assistant tool call is removed from the conversation history and the agent retries. The model can recover from the stripped context on this third attempt.

The `isBadRequest` function filters which 400 errors are recoverable (containing "tool", "malformed", "invalid json", "extra data", "context overflow", "too long", "overflow") and only retries those. Non-recoverable 400s (model not found, invalid parameters) are logged and surfaced without retry, preventing infinite loops.

### Context-based stripping (`internal/agent/runid.go`, `internal/agent/agent.go`)

A new context key (`stripLastToolCallContextKey`) carries the strip signal from the coordinator into `preparePrompt`, which identifies and removes the last assistant tool call from the fantasy message list before sending to the provider.

### Replace `go-jsons` with custom `jsonmerge` package (`internal/jsonmerge/`)

Replaced the alpha `go-jsons` library with a custom zero-dependency deep-merge implementation:

- **New:** `internal/jsonmerge/jsonmerge.go` — simple deep-merge of JSON objects, no external dependencies
- **New:** `internal/jsonmerge/jsonmerge_test.go` — comprehensive tests covering nested objects, arrays, primitives, and the coordinator config merge pattern
- **Modified:** `internal/agent/coordinator.go` — uses `jsonmerge.Merge()` instead of `jsons.Merge()`
- **Modified:** `internal/config/load.go` — uses `jsonmerge.Merge()` instead of `jsons.Merge()`
- **Removed:** `github.com/qjebbs/go-jsons` from go.mod/go.sum

The custom implementation provides deterministic, well-tested JSON merging without the unpredictability of an alpha library.

### Enhanced error logging

- **Modified:** `internal/agent/coordinator.go` — retry logs now include `session_id` for tracking
- **Modified:** `internal/agent/coordinator.go` — merge error logs include all input JSON for debugging
- **Modified:** `internal/config/load.go` — merge error logs include input count
- **Modified:** `internal/jsonmerge/jsonmerge.go` — parse errors include input index

## Where It Could Grow (If Needed)

### General-purpose JSON repair

Currently, `sanitizeJSONInput` only truncates trailing garbage. If the truncated result is not valid JSON (unquoted keys, trailing commas, single quotes), it falls back to `{}`. This avoids wasting a turn on the strip fallback for most cases, but the model receives an empty tool call input rather than the intended arguments.

A more sophisticated repair strategy could:

- Attempt to parse with a lenient parser first
- Fix common issues (unquoted keys, trailing commas)
- Fall back to `{}` only if repair fails

This would allow the model to receive the correct tool call arguments even when the JSON is malformed.

### Per-model/provider sanitization rules

Different parsers produce different artifacts:

| Parser | Typical artifact |
|---|---|
| vLLM `--tool-call-parser qwen3_xml` | `{"key":"val"}}` or `{"key":"val"}\n</tool_use>` |
| Other parsers | May produce different trailing content |

A future enhancement could register sanitization rules per model or parser type, rather than using a single universal approach.

### Configurable tolerance level

A config option could let users choose between:

- **Strict:** Only accept fully valid JSON; strip on failure
- **Best-effort:** Attempt repair before stripping
- **Aggressive:** Strip the tool call immediately without retry

This would give users control over the tradeoff between session survival and correctness.

### Multiple strip attempts

The current strip fallback removes only one tool call. If a conversation has multiple malformed tool calls, it may need multiple strip attempts. A loop that strips one at a time up to N times would be more resilient.

### Logging

Add `slog.Info` in `preparePrompt` when stripping a tool call, including the tool call ID and name, to help debug which tool call caused the 400 and whether the strip was effective.
