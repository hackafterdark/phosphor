# FIX-0002: Agent Stuck in Thinking Loop — Infinite Reasoning Without Tool Calls

## Status

**Applied** — Two-layer defense against models stuck producing identical reasoning content:

1. **Context engineering** (`deduplicateReasoning()`) — Strips duplicated reasoning from conversation history before sending to the API, preventing wasted context tokens and stopping the model from seeing its own repeated output.
2. **Loop detection** (`hasRepeatedThinking()`) — Detects when the same reasoning text repeats >2 times in a 5-step window and stops the streaming loop.

## Problem

When using models with extended reasoning/thinking output (e.g. OpenAI o-series, Claude with thinking, or local models via vLLM), the agent can get stuck in an infinite loop producing the same thinking text:

1. The model produces a large block of reasoning/thinking content (via `OnReasoningDelta`)
2. The model does not produce tool calls or final text
3. The agent sends the conversation (including the thinking) back to the model
4. The model produces the same or very similar thinking again
5. Steps 2-4 repeat indefinitely

The existing loop detection (`hasRepeatedToolCalls`) only triggers when tool calls are present. Steps with only reasoning content are silently skipped, so the agent loops forever.

Users cannot stop it — the Escape key doesn't work because the loop is within a single streaming response from the provider. The only option is to kill the process.

## Why It Occurred

The `StopWhen` conditions in `agent.go` had two checks:

1. **Context window threshold** — Stops when token usage exceeds 80%
2. **`hasRepeatedToolCalls()`** — Only triggers when tool calls exist

When the model is stuck in a thinking loop:
- No tool calls are produced → `hasRepeatedToolCalls` returns `false`
- Token usage may not reach 80% quickly (especially with large context windows like 128K+) → context window check doesn't trigger
- The model keeps producing thinking content indefinitely
- Each iteration adds the full reasoning text to the conversation history, consuming context window tokens

## When Users Encounter It

Users hit this when:

- Using models with extended reasoning/thinking capabilities (o1, o3, o4-mini, Claude, etc.)
- The model gets stuck in a reasoning loop (e.g. unable to decide on a tool call)
- The model produces identical or near-identical thinking text across multiple turns
- The user sees thinking output being printed repeatedly without any tool execution
- The Escape key does not stop the loop

## Fix

### `isReasoningOnlyStep()` (`internal/agent/loop_detection.go`)

A new helper function that identifies steps containing only reasoning content:

```go
func isReasoningOnlyStep(content fantasy.ResponseContent) (string, bool)
```

Returns the combined reasoning text and `true` if the step contains:

- One or more `fantasy.ReasoningContent` parts
- **No** `fantasy.ToolCallContent` or `fantasy.ToolResultContent` parts
- **No** `fantasy.TextContent` parts (final response text)

A step with reasoning + tool calls is considered "progress" (the model is attempting a tool).
A step with reasoning + final text is also "progress" (the model decided on an answer).

Only pure reasoning steps (no tools, no final text) are flagged.

### `deduplicateReasoning()` (`internal/agent/agent.go`)

A context engineering function that strips duplicated reasoning from the conversation history before sending to the API:

```go
func (a *sessionAgent) deduplicateReasoning(messages []fantasy.Message) []fantasy.Message
```

When the model produces identical reasoning across multiple assistant messages, this function:

1. Keeps the first occurrence of each reasoning block
2. Strips duplicates from subsequent assistant messages
3. Resets tracking when encountering a non-assistant message (user/tool)

This prevents the same reasoning text from bloating the context window across multiple turns, even if the loop detection hasn't triggered yet.

Called in `preparePrompt()` after building the history, before messages are sent to the provider.

**Example transformation:**

```
BEFORE (sent to API):
  [assistant: reasoning="looping thinking"]
  [assistant: reasoning="looping thinking"]  ← duplicate
  [assistant: reasoning="looping thinking"]  ← duplicate

AFTER (sent to API):
  [assistant: reasoning="looping thinking"]
  [assistant: ]                              ← reasoning stripped
  [assistant: ]                              ← reasoning stripped
```

### `hasRepeatedThinking()` (`internal/agent/loop_detection.go`)

```go
func hasRepeatedThinking(steps []fantasy.StepResult) bool
```

Detects thinking-only loops by:

1. Looking at the last 5 steps (window size)
2. Collecting reasoning-only step texts (via `isReasoningOnlyStep`)
3. Checking if any reasoning text repeats more than 2 times
4. Requiring at least 2 reasoning-only steps in the window

Parameters:

- **Window size**: 5 steps
- **Max repeats**: 2 (triggers on 3rd identical step)
- **Minimum reasoning steps**: 2 (allows single legitimate variation)

This is intentionally tighter than `hasRepeatedToolCalls` (10-step window, >5 repeats) because sending identical reasoning text back to the model skews the context window and can cause the model to double down on the loop.

### `StopWhen` condition (`internal/agent/agent.go`)

Added a third `StopWhen` condition:

```go
StopWhen: []fantasy.StopCondition{
    func(_ []fantasy.StepResult) bool { /* context window */ },
    func(steps []fantasy.StepResult) bool { return hasRepeatedToolCalls(...) },
    func(steps []fantasy.StepResult) bool { return hasRepeatedThinking(steps) },
},
```

The `hasRepeatedThinking` check is evaluated after the other two, so it only triggers when:

- Context window is not exceeded
- No repeated tool calls detected

### Test coverage (`internal/agent/loop_detection_test.go`)

Comprehensive tests for all three new functions:

- `TestIsReasoningOnlyStep`: 6 test cases — empty content, reasoning-only, reasoning+tools, reasoning+text, text-only, multi-reasoning
- `TestHasRepeatedThinking`: 8 test cases — window size, no reasoning, different texts, mixed steps, reasoning+tools, reasoning+text, loop detection, threshold edge cases
- `TestDeduplicateReasoning`: 7 test cases — no assistants, single assistant, consecutive identical, different reasoning, reasoning with other parts, user message reset, empty reasoning

## How the Two Layers Work Together

The fix uses a two-layer defense that operates at different stages of the pipeline:

### Layer 1: Context Engineering (Prevention)

`deduplicateReasoning()` runs in `preparePrompt()` **before** messages are sent to the API. Every turn, duplicated reasoning is stripped from the conversation history. This means:

- The model never sees its own repeated output
- Context window tokens are preserved
- The loop is silently prevented from accumulating in the context

### Layer 2: Loop Detection (Termination)

`hasRepeatedThinking()` runs as a `StopWhen` condition **during** the streaming loop. When the same reasoning text repeats >2 times in a 5-step window, the streaming loop is terminated:

- The step is saved to the DB (visible in the UI)
- The streaming response is stopped
- The next turn starts with a clean context (duplicates already stripped by Layer 1)

### Why Both Layers Are Needed

- **Layer 1 alone** would silently strip duplicates but the model might still loop indefinitely (just with clean context each time).
- **Layer 2 alone** would stop the loop but the accumulated duplicates would already be in the context window by the time detection triggers.
- **Together** they prevent accumulation (Layer 1) and terminate the loop (Layer 2).

## Where It Could Grow (If Needed)

### Similarity-based detection

Currently, only exact string matches are detected. A future enhancement could use:

- Levenshtein distance to detect near-duplicate reasoning
- Embedding-based similarity for semantic matching
- This would catch models that produce slightly different but effectively identical thinking

### Configurable thresholds

Users could configure:

- `reasoningLoopWindowSize` — default 5
- `reasoningLoopMaxRepeats` — default 2
- `reasoningLoopMinSteps` — default 2

### Logging

Add `slog.Warn` in `hasRepeatedThinking` when a loop is detected, including:

- Session ID
- Number of repeated steps
- First 200 chars of the repeated reasoning text

This would help debug which models/patterns trigger loops most often.

### Graceful degradation

When a thinking loop is detected, instead of just stopping, the agent could:

1. Inject a "stop thinking, pick a tool" system message
2. Lower the temperature to reduce variability
3. Force a tool call with a default tool

This would give the model a chance to recover rather than just terminating the turn.
