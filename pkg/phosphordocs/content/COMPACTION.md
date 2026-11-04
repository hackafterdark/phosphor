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


| Value                               | Behavior                                                          |
| ----------------------------------- | ----------------------------------------------------------------- |
| `"main"` (default, alias for large) | Use the session's large model                                     |
| `"large"`                           | Use the configured large model                                    |
| `"small"`                           | Use the configured small model                                    |
| `"embedded"`                        | Use the local dlgo GGUF model (requires `embedded_models` config) |


These live in `phosphor.json` under the `options` key:

```
{
  "options": {
    "disable_auto_summarize": false,
    "summarize_threshold": 0.8,
    "summarize_model": "main",
    "summarize_tool_output_chars": 200,
    "summarize_tool_output_aggressive_chars": 50
  }
}
```

### `summarize_tool_output_chars`

Number of characters to keep for each tool output during standard pre-compaction
pruning. Defaults to **200**. Tool results longer than this value are truncated
and appended with `\n... [truncated]`. Non-tool messages are passed through
unchanged. Lower values produce more aggressive context reduction; higher values
retain more tool output detail.

### `summarize_tool_output_aggressive_chars`

Number of characters to keep during overflow recovery pruning. Defaults to **50**.
Used as a fallback when the standard pruned input still exceeds the model's context
window — the engine retries with this more aggressive truncation.

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
2. **Prunes tool outputs** — verbose tool results are truncated to
 `summarize_tool_output_chars` (default 200) characters (`pkg/agent/prune.go`) to
 reduce context pressure before summarization.
3. **Loads summary prompt** from the profile system
 (`pkg/agent/prompt/templates/summarization.md.tpl`), with optional iterative
 context (previous summary prepended).
4. **Creates a summary message** (`IsSummaryMessage: true`) in the session.
5. **Sends the pruned history** to the configured model (main/large, small, or embedded).
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
retries with **aggressive pruning** (tool outputs truncated to
'summarize_tool_output_aggressive_chars', default 50). This handles edge cases where the
standard pruning wasn't enough.

### Iterative Summaries

On subsequent compaction cycles, the previous summary is prepended to the prompt,
allowing the model to compound knowledge across multiple summarization passes.

### Embedded Model Path

When `summarize_model: "embedded"`, the `summarizeEmbedded()` path handles:

1. **Model download**: Auto-downloads GGUF models from HuggingFace at startup
 (`ensureEmbeddedModel`).
2. **GPU acceleration**: If `gpu: true`, attempts Vulkan GPU offload; falls back
 to CPU if no GPU is available.
3. **Otel instrumentation**: Tracks `gen_ai.usage.input_tokens` and
 `gen_ai.usage.output_tokens` for observability.

## Microcompaction (Deterministic Tool-Output Elision)

Microcompaction is a cheap, LLM-free pass that runs **before** summarization and
delays (or avoids) it. It shrinks the *outbound* prompt by eliding stale tool
outputs, never by truncating them and never by touching the conversation itself.

### Configuration

```json
{
  "options": {
    "agent": {
      "microcompaction": {
        "enabled": false,
        "threshold": 0.50,
        "retention_turns": 8
      }
    }
  }
}
```

| Field              | Default | Meaning                                                                 |
| ------------------ | ------- | ----------------------------------------------------------------------- |
| `enabled`          | `false` | Opt-in switch; when `false` the pass never runs.                        |
| `threshold`        | `0.50`  | Fraction of the context window at which elision activates.              |
| `retention_turns`  | `8`     | Number of most recent turns whose tool outputs are never touched.       |

Zero-valued `threshold`/`retention_turns` — and `threshold` values outside
`(0, 1]` — fall back to the defaults above.

### How It Works

The transcript is split into two zones:

- **Hot zone** — the last `retention_turns` user turns. Kept 100% untouched,
  which preserves recent context and keeps provider prompt-cache prefixes
  stable.
- **Cold zone** — everything older. Its tool results are rewritten according
  to fixed, deterministic rules:

1. **File view deduplication (latest-wins).** If the same file path was
   `view`ed several times with the same parameters, only the most recent
   view survives intact; earlier ones are replaced with
   `[Elided: superseded by later file view]`. Partial views (explicit
   `offset`/`limit`) only supersede earlier calls with the identical range,
   and `view_node` calls are keyed by file path plus node name, so a later
   view of a different part of the file never hides the only copy of
   different lines. Failed view calls never supersede an earlier successful
   one. Applies to `view`, `view_node`, and `phosphor_docs` (path mode).
2. **Stale search collapse.** Verbose navigation output — `grep`, `glob`,
   `ls`, `workspace_search`, `structural_search`, `semantic_search`,
   `sourcegraph`, `web_search`, `lsp_references`, `memory_search`, and
   `phosphor_docs` (query mode) — longer than 200 characters is replaced with
   a one-line summary, e.g.
   `[Elided search output: "pkg/sandbox" — 12 matches found]`.
3. **Duplicate command deduplication (latest-wins).** Commands run through
   `bash` whose canonical form matches a later identical call are elided to
   `[Elided: superseded by identical later search command]`. Search binaries
   (`rg`, `grep`, `find`, `ls`, ...) elide on the canonical command alone —
   their output is navigation data re-derivable at any time. File-reading
   binaries (`cat`, `tail`, `head`) and any other canonicalizable command
   additionally require byte-identical output, so `cat` of a growing log or
   a re-run test with new failures is never elided — only repetition that
   adds nothing is. Commands are only canonicalized when single-line and
   unquoted (quoted whitespace is significant, and multi-line commands are
   compound). Compound commands (pipes, `&&`, redirections other than
   `/dev/null`, command substitution) are never canonicalized, and failed
   or missing results never supersede an earlier call's output — failed
   results themselves are never elided.

### Guarantees

- **Only tool outputs are ever modified.** User messages, assistant text, and
  reasoning/thinking content are never rewritten, elided, or reordered.
- **Evidence is preserved.** Tool results that carry verification information —
  errors, `lsp_diagnostics` output, `edit`/`write` results — are never elided.
  For `bash`, duplicate commands are elided only when the repetition adds
  nothing: search binaries on an identical canonical command, and any other
  canonicalizable command only when its output is byte-identical to a later
  run. Builds, tests, and compound pipelines with new output stay verbatim.
- **Non-destructive.** The pass rewrites an outbound copy for the current
  request only; stored session messages are untouched, so the transcript stays
  append-only and the UI keeps showing full history.
- **Per-request evaluation.** Elision is recomputed for every request from the
  stored history. An elided result can return to its full form when the
  activation threshold is no longer met, or when a later run of the same
  non-search `bash` command returns different output. The outbound prompt
  prefix can therefore change between requests.
- **Sub-agents skip the pass** — their sessions are short and single-purpose.

Microcompaction complements (and never replaces) auto-summarization: elision
buys context headroom silently; once usage crosses `summarize_threshold`,
summarization still fires as described above.

## Profile-Based Prompt Customization

The summarization prompt is loaded via the 3-tier profile system:

1. **Workspace**: `.phosphor/profiles/<profile>/summarization.md.tpl`
2. **Global**: `%LOCALAPPDATA%/phosphor/profiles/<profile>/summarization.md.tpl`
3. **Default**: `pkg/agent/prompt/templates/summarization.md.tpl` (embedded)

Users can customize the prompt per-profile to tailor summary structure or focus.