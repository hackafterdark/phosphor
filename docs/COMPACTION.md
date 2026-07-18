# Context Window Compaction (Auto-Summarization)

## Overview

Phosphor automatically summarizes a session's conversation history when the
context window is approaching capacity. This "compaction" prevents context overflow
by replacing the full message history with a concise summary produced by the model
itself.

## Configuration

### `summarize_model`

The `summarize_model` option in `phosphor.json` controls which model handles
compaction. Valid values:

| Value | Behavior |
|---|---|
| `"main"` (default) | Use the session's large model |
| `"large"` | Use the configured large model |
| `"small"` | Use the configured small model |
| `"embedded"` | Use the local dlgo GGUF model (requires `embedded_models` config) |

These live in `phosphor.json` under the `options` key:

```json
{
  "options": {
    "disable_auto_summarize": false,
    "summarize_threshold": 0.8,
    "summarize_model": "main"
  }
}
```

### `embedded_models`

```json
{
  "embedded_models": {
    "inference": {
      "enabled": true,
      "model_repo": "Qwen3.5-0.8B",
      "gpu": false
    }
  }
}
```

- `model_repo`: HuggingFace repo ID for auto-download (e.g., `"Qwen3.5-0.8B"`,
  `"Phi-3-mini"`, `"Gemma-2-2B"`)
- `model_path`: Local path to a GGUF file (alternative to `model_repo`)
- `gpu`: Enable GPU acceleration (requires build with Vulkan support); falls
  back to CPU if GPU is unavailable.

## Trigger Conditions

Auto-summarization is triggered at **two checkpoints** during `SessionAgent.Run()`:

### 1. Pre-Request Threshold Check

Before sending the LLM request, the agent estimates the current token count of all
messages and checks whether it exceeds the configured threshold fraction of the model's
context window:

```
fraction = currentTokens / contextWindow
```

If `fraction >= threshold` (default **0.8**, i.e. 80%), summarization fires.

### 2. Post-User-Message Overflow Check

After adding the new user message to the session, the agent re-estimates total
tokens. If the total would **exceed** the model's context window, it force-summarizes
before sending the request:

```
if totalTokens > contextWindow:
    force summarize
```

This is a safety net — it catches cases where a single large user prompt would push
the conversation past the model's limit.

## How Summarization Works

When triggered, `SessionAgent.Summarize()` executes:

1. **Loads conversation history** via `getSessionMessages()`.
2. **Prunes tool outputs** — verbose tool results are truncated to 200 characters
   (`pkg/agent/prune.go`) to reduce context pressure before summarization.
3. **Loads summary prompt** from the profile system
   (`pkg/agent/prompt/templates/summarization.md.tpl`), with optional iterative
   context (previous summary prepended).
4. **Creates a summary message** (`IsSummaryMessage: true`) in the session.
5. **Sends the pruned history** to the configured model (main, embedded, or cloud).
6. **Streams the summary** — text deltas are appended to the summary message in
   real-time.
7. **Updates session state**:
   - `SummaryMessageID` is set to the new summary message's ID.
   - `CurrentTokens` is recalculated from the summary content using
     `approxTokenCount()`.
   - `CompletionTokens` is updated from the response usage.
   - `PromptTokens` is reset to 0 (the summary replaces the full history).
8. **Processes queued messages** — if user sent new prompts during summarization,
   the first queued message is processed immediately after.

### Overflow Recovery

If the model rejects the request due to context overflow, the compaction engine
retries with **aggressive pruning** (tool outputs truncated to 50 characters). This
handles edge cases where the standard pruning wasn't enough.

### Iterative Summaries

On subsequent compaction cycles, the previous summary is prepended to the prompt,
allowing the model to compound knowledge across multiple summarization passes.

### Embedded Model Path

When `summarize_model: "embedded"`, the `summarizeEmbedded()` path handles:
handles:

1. **Model download**: Auto-downloads GGUF models from HuggingFace at startup
   (`ensureEmbeddedModel`).
2. **GPU acceleration**: If `gpu: true`, attempts Vulkan GPU offload; falls back
   to CPU if no GPU is available.
3. **Otel instrumentation**: Tracks `gen_ai.usage.input_tokens` and
   `gen_ai.usage.output_tokens` for observability.

## Profile-Based Prompt Customization

The summarization prompt is loaded via the 3-tier profile system:

1. **Workspace**: `.phosphor/profiles/<profile>/summarization.md.tpl`
2. **Global**: `%LOCALAPPDATA%/phosphor/profiles/<profile>/summarization.md.tpl`
3. **Default**: `pkg/agent/prompt/templates/summarization.md.tpl` (embedded)

Users can customize the prompt per-profile to tailor summary structure or focus.