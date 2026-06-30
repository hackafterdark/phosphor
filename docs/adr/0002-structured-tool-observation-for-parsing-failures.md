# ADR-0002: Structured Tool Observation for Parsing Failures

## Status

Accepted

## Context

### Original Behavior: Raw Errors in Session Messages

Phosphor's agent harness originally included the raw error message and 400 Bad Request response from the inference API directly in the session's message history. This caused a critical failure: when a framework-level tool call validation error occurred (e.g., missing parameters, invalid JSON, malformed patterns), the error response was stored as a tool result in the session. The presence of this error message in the conversation history corrupted the session state — the session would become unusable and refuse further input, forcing the user to force-quit the application.

### The Protective Strip

To prevent sessions from becoming unusable, the error was handled by silently stripping the failed tool call and its error response from the conversation history before the next API request. This approach prevented the session corruption issue, but introduced a critical problem: the agent was left "blind" to why its tool call failed.

### The Blind Agent Problem

When the TUI displayed a tool error to the user, the model had no corresponding context in the conversation history to explain the failure. This led to the model confidently repeating the same broken tool call, producing repetitive and incorrect error messages, and in some cases entering retry loops that degraded the user experience.

The core challenge was balancing two competing concerns:

1. **Session integrity**: The conversation history must not contain raw error responses that corrupt the session and make it unusable.
2. **Agent awareness**: The model must understand *why* a tool call failed so it can self-correct.

## Decision

We implemented a **Structured Tool Observation** pattern that intercepts framework-level validation errors, translates them into human-readable, actionable messages, and injects them as synthetic tool result messages into the conversation history — rather than silently stripping the failed call.

### Architecture

The implementation spans three layers:

#### 1. Error Translation Layer (`internal/agent/tool_observation.go`)

The `translateToObservation` function converts framework-level validation errors into semantic, model-friendly messages. It matches error message patterns and produces structured "Tool Observation" messages that explain the failure:

```
Tool Observation: Your previous attempt failed because the required parameter "pattern" was missing for tool "glob". Please review the tool definition and provide all necessary inputs.
```

The translation layer recognizes specific error categories:

| Error Pattern | Translation |
|---|---|
| `missing required parameter` | Identifies the specific missing parameter (extracted via `extractParamName`) and the tool name. |
| `invalid pattern` | Instructs the model to use standard regex syntax. |
| `invalid json` / `malformed` / `parse error` | Instructs the model to ensure valid JSON with quoted keys. |
| `extra data` | Instructs the model to provide only expected inputs. |
| `context overflow` / `too long` / `overflow` | Instructs the model to provide shorter input. |
| Generic tool errors | Provides the raw error message with guidance to review the tool definition. |

The `injectToolObservation` method on `sessionAgent` creates a synthetic `message.Tool` message with `IsError: true` and appends it to the session's message store, preserving the original tool call ID so it passes the orphan filter.

#### 2. Recovery Flow in the Coordinator (`internal/agent/coordinator.go:270-314`)

The coordinator's `Run` method implements a three-phase recovery strategy for 400 Bad Request errors:

1. **First retry**: On a 400 error, retry once. vLLM's tool call parser can produce malformed output that triggers context overflow; the model often produces valid output on the second attempt.

2. **Strip + Observation**: If the second attempt also fails, the coordinator:
   - Calls `injectToolObservation` to insert the translated error message into the session.
   - Tags the context with `WithStripLastToolCall` so the agent skips the last assistant tool call when building the conversation history.
   - Runs the agent again with the stripped history and the injected observation.

3. **Hard stop**: If the third attempt also fails with 400, the coordinator does **not** retry further. The error is treated as permanent (e.g., model misconfiguration, invalid parameters).

```go
// Pseudocode of the recovery flow:
if c.isBadRequest(originalErr) {
    result, originalErr = run()                    // Attempt 1 (original)
    if c.isBadRequest(originalErr) {
        result, originalErr = run()                // Attempt 2 (retry)
        if c.isBadRequest(originalErr) {
            c.injectToolObservation(stripCtx, ...)  // Inject observation
            result, originalErr = c.currentAgent.Run(stripCtx, ...)  // Attempt 3 (strip + observe)
            // If still 400: do NOT retry further
        }
    }
}
```

The `isBadRequest` function (`coordinator.go:1149`) filters 400 errors to only those that are potentially recoverable — matching on keywords like `tool`, `malformed`, `invalid json`, `extra data`, `context overflow`, and `parse error`. This prevents retrying on permanent errors (e.g., model not found).

#### 3. Conversation History Stripping (`internal/agent/agent.go:1016`)

When the `IsStripLastToolCall` context flag is set, the agent's `preparePrompt` method identifies the last assistant message with tool calls and skips its tool call parts when building the fantasy conversation history. This prevents the malformed tool call from being re-sent to the API while still preserving the rest of the conversation context.

#### 4. System Prompt Constraint (`internal/agent/agent.go:454`)

A `<tool-observation-instructions>` block is appended to the system prompt:

```
If you receive a 'Tool Observation' in a tool result message, you are required to analyze the error and adjust your strategy before retrying. Do not repeat the same failed tool call with the same input. Review the tool definition, correct the input parameters, and ensure valid JSON syntax.
```

This gives the model explicit instructions on how to respond when it receives a Tool Observation.

#### 5. Consecutive Failure Guardrail (`internal/agent/loop_detection.go`)

A separate loop detection mechanism tracks consecutive tool failures per tool name across the last 10 steps (`loopDetectionWindowSize`). If a specific tool fails more than 2 times in a row (`toolFailureMaxCount`), the system treats it as a loop and prevents further retries for that tool. A successful call for the same tool resets the counter.

```go
// Key constants:
loopDetectionWindowSize  = 10
toolFailureMaxCount      = 2
```

The `hasConsecutiveToolFailures` function scans steps backwards, tracking failure counts per tool name. A missing result (no tool result for a call) is also treated as a failure.

### Context Key Design (`internal/agent/runid.go`)

Two unexported context keys coordinate the flow between coordinator and agent:

- `stripLastToolCallContextKey`: Signals the agent to skip the last assistant tool call.
- `toolObservationErrorKey`: Carries the original validation error and tool name from the coordinator to the agent.

These keys are passed via `context.WithValue` and checked via `IsStripLastToolCall` and `ToolObservationErrorFromContext`, keeping the coordinator-agent boundary clean without changing function signatures.

## Consequences

**Positive:**

- **Informed self-correction**: The model understands *why* a tool call failed and can adjust its input on the next attempt. This eliminates the "blind retry" problem that previously caused repetitive, confident errors.
- **Preserved context**: The conversation history reflects reality — the TUI error message matches what the model sees in the context window.
- **Reduced noise**: The model is not forced to re-reason over its own broken input; it receives a clear, translated explanation instead.
- **Loop prevention**: The combination of the hard stop (3 attempts max) and the consecutive failure guardrail (`toolFailureMaxCount = 2`) prevents infinite retry loops.
- **Improved stability**: The structured observation has measurably improved the stability of the Phosphor agent in production, particularly for tools with strict input requirements (e.g., `glob`, `view`, `edit`).

**Negative:**

- **Increased message history**: Injecting synthetic tool result messages adds entries to the session, slightly increasing the context window usage.
- **Translation fidelity**: The `translateToObservation` function relies on string matching against error messages. If the underlying framework changes error message formats, translations may become stale.
- **Complexity**: The three-phase recovery flow (retry → strip + observe → hard stop) is more complex than the previous simple strip approach. This increases the cognitive load for future maintainers.
- **Hard-coded thresholds**: The retry limit (3 attempts) and failure count (2) are hard-coded constants. Different models may recover from errors with varying success rates, making a configurable policy desirable in the future.

**Open Questions:**

- **Configurable policy**: The RFC proposes a `ToolPolicy` struct with `MaxRetries`, `Strict`, and `RequiresHuman` fields to make the failure handling configurable per-tool. This would allow users to define safety profiles based on the tool's risk level.
- **Translation robustness**: The error translation relies on substring matching. A more robust approach might parse structured error types from the framework rather than matching on error message strings.
- **Per-tool failure limits**: The current `toolFailureMaxCount = 2` applies uniformly to all tools. Some tools (e.g., `bash`) may tolerate more retries than others (e.g., `edit`).

## Alternatives

### Silent Strip (Previous Approach)

The original approach silently stripped failed tool calls from the conversation history. This prevented infinite loops but left the agent blind to failures, causing it to repeat the same broken calls with confidence.

**Rejected because**: The blind retry problem caused significant user-facing issues — the agent repeatedly produced the same errors without understanding why, degrading the user experience and making the agent appear unreliable.

### Full Error Propagation to User

An alternative would be to immediately surface tool failures to the user without attempting any retry. This would be the simplest approach but would sacrifice the agent's ability to self-correct on transient errors (e.g., vLLM parser producing malformed output that succeeds on retry).

**Rejected because**: Many 400 errors are transient and recoverable. The three-phase recovery strategy captures these cases while still preventing infinite loops.

### Strip Without Observation

Strip the failed tool call but do not inject a Tool Observation message. This is simpler than the current approach but still leaves the agent without context about why the call failed.

**Rejected because**: Without the observation, the agent still lacks the information needed to self-correct. The system prompt constraint alone is insufficient — the model needs the specific error context.

## References

- `internal/agent/tool_observation.go` — error translation, observation injection
- `internal/agent/coordinator.go:270-314` — three-phase recovery flow
- `internal/agent/coordinator.go:1149-1168` — `isBadRequest` error filtering
- `internal/agent/coordinator.go:1170-1216` — `injectToolObservation` implementation
- `internal/agent/agent.go:1016` — conversation history stripping
- `internal/agent/agent.go:454` — system prompt constraint
- `internal/agent/loop_detection.go` — consecutive failure guardrail
- `internal/agent/runid.go` — context key design
- RFC: `.agents/docs/rfc/structured-observation-layer-for-tool-failures.md`
