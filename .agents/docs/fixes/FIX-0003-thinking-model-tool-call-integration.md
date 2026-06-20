# FIX-0003: Thinking/Reasoning Model Integration — Preserve-Thinking Tags and Provider Policy

## Status

**Applied** — Four-layer integration for working with thinking/reasoning models (e.g. OpenAI o-series, Claude with thinking, DeepSeek-R1, and local models via vLLM):

1. **Template Context Injection** — `chat_template_kwargs` injected via `extra_body` to force Jinja templates to wrap reasoning in proper `<thinking>` tags.
2. **Tool Observation Integration** — Framework-level tool validation errors are translated into structured `Tool Observation` messages, preventing the model from silently repeating broken tool calls.
3. **Single-Tool Constraint** — System prompt enforces one-at-a-time tool calls, bypassing the vLLM XML parser bug that fails on concurrent tool blocks.
4. **Provider Policy Pattern** — `ProviderPolicy` struct with hardcoded safe defaults, structured for future config-driven overrides.

## Problem

When using models with extended reasoning/thinking output, two interrelated issues degraded the agent reliability:

1. **Reasoning-leak / malformed tool calls**: The models thinking content was not wrapped in proper `<thinking>` tags by the Jinja chat template. Without the tags, the tool-parser could not distinguish reasoning text from tool instructions, causing the model to produce malformed tool calls (e.g., thinking text interpreted as tool parameters, missing required parameters, invalid JSON).

2. **Silent failure on tool errors**: When a tool call failed (validation errors, JSON parse errors, framework-level rejections), the error was silently stripped from the conversation history. The model had no context about why its call failed, leading to confident repetition of the same broken tool call — often in infinite retry loops.

3. **vLLM parser bug**: The vLLM XML parser fails when the model produces multiple concurrent tool blocks in a single response. The model would attempt parallel tool calls, triggering malformed output that cascaded into 400 Bad Request errors.

## Why It Occurred

- **No template context**: The request-building logic in `coordinator.go` did not pass `chat_template_kwargs` to the provider. Jinja-based templates (used by OpenAI-compatible providers, Hyper, and Catwalk backends) had no signal to wrap reasoning in `<thinking>` tags.
- **No error translation**: The `translateToObservation` function and the `injectToolObservation` pipeline did not exist. Failed tool calls were silently stripped, leaving the model blind.
- **No single-tool constraint**: The system prompt allowed the model to issue multiple tool calls in a single response, triggering the vLLM XML parser bug.
- **No policy abstraction**: Provider-specific thinking settings were scattered across provider-specific switch cases without a unified default or override mechanism.

## Fix

### 1. Provider Policy (internal/agent/coordinator.go:83-100)

A new `ProviderPolicy` struct with a `ChatTemplateKwargs` map provides a single source of truth for thinking-related template configuration:

```go
type ProviderPolicy struct {
    ChatTemplateKwargs map[string]any
}

func defaultProviderPolicy() ProviderPolicy {
    return ProviderPolicy{
        ChatTemplateKwargs: map[string]any{
            "preserve_thinking": true,
            "enable_thinking":   true,
        },
    }
}
```

The policy is isolated to the coordinators request-building logic. No existing provider abstractions are modified — the policy values are injected into the provider-specific option maps via the `extra_body` key.

**Extensibility**: The `ChatTemplateKwargs` map is a `map[string]any`, so future config-driven overrides can add or replace keys without changing the struct layout.

### 2. Template Context Injection (`internal/agent/coordinator.go:476-531`)

The `getProviderOptions` switch case for `openaicompat` and `hyper` providers now injects the policy:

```go
case openaicompat.Name, hyper.Name:
    extraBody := make(map[string]any)

    // Inject chat_template_kwargs so Jinja-based providers can
    // preserve and enable thinking tags in the model output.
    policy := defaultProviderPolicy()
    extraBody["chat_template_kwargs"] = policy.ChatTemplateKwargs

    // ... provider-specific thinking/reasoning config follows ...

    mergedOptions["extra_body"] = extraBody
```

**How `chat_template_kwargs` interacts with Jinja templates:**

The `chat_template_kwargs` map is forwarded through the Fantasy provider layer into the OpenAI-compatible HTTP request body under the `extra_body` key. Jinja-based chat templates (used by providers like Hyper, vLLM, and Catwalk backends) read these kwargs to control template behavior:

- **`preserve_thinking: true`** — Tells the Jinja template to preserve the models `<thinking>` tags in the output rather than stripping them. Without this, the template may emit raw reasoning text that the tool-parser cannot distinguish from tool instructions.
- **`enable_thinking: true`** — Tells the template to actively wrap the models reasoning content in `<thinking>` tags. This ensures the parser can correctly identify and separate reasoning from tool call instructions.

Together, these kwargs force the Jinja template to produce output like:

```
<thinking>
I need to search for the file first.
</thinking>
```

This prevents the tool-parser from misinterpreting reasoning text as malformed tool instructions, which was the root cause of missing required parameters, invalid JSON, and extra data errors.

**Provider coverage:**

| Provider | `extra_body` path | Template support |
|---|---|---|
| OpenAI-compatible | `extra_body.chat_template_kwargs` | Yes (via Jinja) |
| Hyper | `extra_body.chat_template_kwargs` + `extra_body.thinking` | Yes (via Jinja) |
| Catwalk (inference.ai) | `extra_body.chat_template_kwargs` + `extra_body.reasoning` / `extra_body.enable_thinking` | Yes (via Jinja) |

For Catwalk backends, the `enable_thinking` field is also set dynamically based on `model.ModelCfg.Think` (e.g., `extraBody["enable_thinking"] = model.ModelCfg.Think` for Alibaba Singapore).

### 3. Tool Observation Integration (`internal/agent/tool_observation.go`, `internal/agent/coordinator.go:1198-1244`)

The `translateToObservation` function converts framework-level validation errors into human-readable, actionable messages:

```go
func translateToObservation(err error, toolName string) string
```

Error categories recognized and translated:

| Error Pattern | Translation |
|---|---|
| `missing required parameter` | Identifies the specific missing parameter (extracted via `extractParamName`) and the tool name. |
| `invalid pattern` | Instructs the model to use standard regex syntax. |
| `invalid json` / `malformed` / `parse error` | Instructs the model to ensure valid JSON with quoted keys. |
| `extra data` | Instructs the model to provide only expected inputs. |
| `context overflow` / `too long` / `overflow` | Instructs the model to provide shorter input. |
| Generic tool errors | Provides the raw error message with guidance to review the tool definition. |

The `injectToolObservation` method on `sessionAgent` (`tool_observation.go:68`) creates a synthetic `message.Tool` message with `IsError: true` and appends it to the session message store, preserving the original tool call ID so it passes the orphan filter.

The coordinator-level `injectToolObservation` (`coordinator.go:1202`) finds the last assistant message with tool calls and injects the translated observation:

```go
func (c *coordinator) injectToolObservation(ctx context.Context, sessionID string, err error)
```

This is called during the three-phase 400 Bad Request recovery flow (`coordinator.go:296-328`):

1. **First retry**: On a 400 error, retry once.
2. **Strip + Observation**: If the second attempt also fails, inject the tool observation, tag the context with `WithStripLastToolCall`, and run again.
3. **Hard stop**: If the third attempt also fails with 400, do NOT retry further.

### 4. Single-Tool Constraint (`internal/agent/templates/coder.md.tpl:21`)

A new rule in the critical rules section enforces sequential tool calls:

```
16. **SINGLE TOOL CALL**: You must issue tool calls one at a time. Do not attempt to use multiple tools in a single response block. Wait for the result of the first tool before calling the next.
```

This constraint is necessary because the vLLM XML parser (used by many local model providers) fails when the model produces multiple concurrent tool blocks in a single response. By forcing sequential tool calls, the model avoids triggering the parser bug entirely.

## How the Layers Work Together

The fix uses four interrelated layers that operate at different stages of the pipeline:

### Layer 1: Template Context Injection (Prevention)

`chat_template_kwargs` is injected into the request body **before** the API call is sent. This ensures the models reasoning output is properly wrapped in `<thinking>` tags, preventing the tool-parser from misinterpreting reasoning as tool instructions.

### Layer 2: Tool Observation Integration (Recovery)

When a tool call fails despite prevention, `translateToObservation` converts the raw error into a semantic message, and `injectToolObservation` inserts it into the conversation history. This gives the model the context it needs to self-correct on the next attempt.

### Layer 3: Single-Tool Constraint (Enforcement)

The system prompt constraint prevents the model from attempting parallel tool calls, which eliminates the vLLM XML parser bug as a failure mode entirely.

### Layer 4: Provider Policy (Abstraction)

The `ProviderPolicy` struct provides a unified entry point for thinking-related configuration, making it easy to add provider-specific overrides in the future without scattering changes across the codebase.

## Where It Could Grow (If Needed)

### Config-driven policy overrides

Users could configure `ChatTemplateKwargs` per-provider or per-model:

```json
{
  "provider_policy": {
    "openaicompat": {
      "preserve_thinking": true,
      "enable_thinking": true
    },
    "hyper": {
      "preserve_thinking": false,
      "enable_thinking": true
    }
  }
}
```

### Provider-specific template kwargs

Not all providers support `chat_template_kwargs`. A future enhancement could allow providers to declare their supported kwargs, and the coordinator could conditionally inject them.

### Similarity-based error translation

The `translateToObservation` function relies on substring matching. A more robust approach could parse structured error types from the Fantasy framework rather than matching on error message strings.

### Logging

Add `slog.Warn` in `injectToolObservation` when a tool observation is injected, including:

- Session ID
- Tool name
- First 200 chars of the error message

This would help debug which models/patterns trigger tool failures most often.

## References

- `internal/agent/coordinator.go:83-100` — `ProviderPolicy` struct and `defaultProviderPolicy`
- `internal/agent/coordinator.go:476-531` — `chat_template_kwargs` injection in `getProviderOptions`
- `internal/agent/tool_observation.go` — `translateToObservation`, `extractParamName`, `injectToolObservation`
- `internal/agent/tool_observation_test.go` — Test coverage for translation and parameter extraction
- `internal/agent/coordinator.go:1198-1244` — Coordinator-level `injectToolObservation`
- `internal/agent/coordinator.go:296-328` — Three-phase 400 Bad Request recovery flow
- `internal/agent/templates/coder.md.tpl:21` — Single-tool constraint in system prompt
- ADR: `.agents/docs/adr/0002-structured-tool-observation-for-parsing-failures.md`
- RFC: `.agents/docs/rfc/preserve-thinking.md`
