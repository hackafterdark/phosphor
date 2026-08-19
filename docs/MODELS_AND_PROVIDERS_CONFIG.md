# Models and Providers Configuration

This document covers how to configure LLM providers and models in `phosphor.json`.

## Overview

Phosphor configuration has two main sections for LLM setup:

- **`providers`** — Defines API endpoints, authentication, and provider-specific settings
- **`models`** — Selects which model to use for "large" (primary) and "small" (fallback/title generation) tasks

```json
{
  "$schema": "https://raw.githubusercontent.com/hackafterdark/phosphor/main/schema.json",
  "providers": {
    "openai": {
      "api_key": "$OPENAI_API_KEY",
      "models": [{"id": "gpt-4o", "name": "GPT-4o"}]
    }
  },
  "models": {
    "large": {
      "model": "gpt-4o",
      "provider": "openai",
      "temperature": 0.7
    },
    "small": {
      "model": "gpt-4o-mini",
      "provider": "openai"
    }
  }
}
```
## Configuration Scopes

You can configure models either by editing the `phosphor.json` file (global or workspace) or via the TUI.

When you use the model selection dialog (opened with `/` → "Switch Model" or `Ctrl+L`), the selected model is saved to the **workspace‑local** configuration file (`<project>/.phosphor/phosphor.json`) under the `models.large` or `models.small` key.

The global configuration (`%APPDATA%\phosphor\phosphor.json` or `$XDG_CONFIG_HOME/phosphor/phosphor.json`) can still be edited manually and provides defaults for workspaces that do not have a local override.

Workspace‑scoped settings take precedence over global settings.

---

## Providers Configuration

The `providers` section defines each LLM provider you want to use. Each provider is keyed by a unique ID (e.g., `"openai"`, `"anthropic"`, `"gb10"`).

### Provider Fields

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `id` | string | No | Unique identifier for the provider (used in `models.*.provider`) |
| `name` | string | No | Human-readable display name |
| `base_url` | string | No | API endpoint URL (defaults vary by provider type) |
| `type` | string | No | Provider API format: `openai`, `openai-compat`, `anthropic`, `gemini`, `azure`, `vertexai` (default: `openai`) |
| `api_key` | string | No | API key (supports `$VAR` environment variable expansion) |
| `disable` | bool | No | Set to `true` to disable the provider (default: `false`) |
| `system_prompt_prefix` | string | No | Custom prefix injected into system prompts |
| `tool_call_format` | string | No | How tool calls are elicited: `native` (default) or `xml` (for self-hosted parsers like vLLM) |
| `extra_headers` | object | No | Additional HTTP headers (values support `$VAR` expansion) |
| `extra_body` | object | No | Fields passed verbatim to OpenAI-compatible request bodies |
| `provider_options` | object | No | Provider-specific options (merged with model-level options) |
| `flat_rate` | bool | No | Skip cost tracking for subscription/flat-rate billing |
| `discover_models` | bool | No | Auto-discover models from `/v1/models` endpoint (default: `true`) |
| `models` | array | No | Explicit list of available models (if empty and `discover_models` is true, Phosphor auto-discovers) |

### Provider Types

| Type | Description | Examples |
|------|-------------|----------|
| `openai` | OpenAI native API (uses Responses API by default) | `openai`, `azure` |
| `anthropic` | Anthropic Messages API | `anthropic`, `bedrock` |
| `openai-compat` | OpenAI-compatible APIs (vLLM, llama.cpp, Ollama, etc.) | `vllm`, `ollama`, `litellm` |
| `gemini` | Google Gemini API | `google` |
| `azure` | Azure OpenAI Service | `azure` |
| `vertexai` | Google Vertex AI | `vertexai` |

### Examples

**OpenAI:**
```json
{
  "providers": {
    "openai": {
      "api_key": "$OPENAI_API_KEY",
      "models": [{"id": "gpt-4o", "name": "GPT-4o"}]
    }
  }
}
```

**Anthropic:**
```json
{
  "providers": {
    "anthropic": {
      "api_key": "$ANTHROPIC_API_KEY",
      "type": "anthropic",
      "models": [{"id": "claude-sonnet-4-20250514", "name": "Claude Sonnet 4"}]
    }
  }
}
```

**vLLM (OpenAI-compatible):**
```json
{
  "providers": {
    "gb10": {
      "base_url": "http://localhost:8000/v1",
      "type": "openai-compat",
      "tool_call_format": "xml",
      "models": [
        {"id": "ornith1.0-35b-nvfp4-mtp", "name": "Ornith 1.0 35B"}
      ]
    }
  }
}
```

**Ollama:**
```json
{
  "providers": {
    "ollama": {
      "base_url": "http://localhost:11434/v1",
      "type": "openai-compat",
      "discover_models": true
    }
  }
}
```

---

## Models Configuration

The `models` section selects which model to use for different tasks. Phosphor uses two model slots:

- **`large`** — Primary model for coding, reasoning, and complex tasks
- **`small`** — Fallback model for title generation, quick responses, and sub-agents

### Model Fields

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `model` | string | Yes | Model ID as used by the provider API |
| `provider` | string | Yes | Provider ID matching a key in the `providers` section |
| `reasoning_effort` | string | No | Reasoning effort level: `low`, `medium`, `high` (OpenAI models) |
| `think` | bool | No | Enable thinking mode for Anthropic models |
| `enable_thinking` | string | No | `on`/`off` override of the `enable_thinking` chat template kwarg for openai-compatible models (llama.cpp, vLLM, ...); omitted leaves the provider default (on) |
| `max_tokens` | int64 | No | Maximum tokens for model responses (default: from provider catalog) |
| `temperature` | float64 | No | Sampling temperature (0-1, default: 1.0) |
| `top_p` | float64 | No | Nucleus sampling parameter (0-1, default: 1.0) |
| `top_k` | int64 | No | Top-k sampling parameter (-1 for all tokens) |
| `frequency_penalty` | float64 | No | Penalty for token frequency (-2.0 to 2.0) |
| `presence_penalty` | float64 | No | Penalty for token presence (-2.0 to 2.0) |
| `seed` | int64 | No | Random seed for deterministic sampling |
| `min_p` | float64 | No | Minimum probability relative to most likely token (0-1) |
| `repetition_penalty` | float64 | No | Penalty for repetition (>1 discourages repetition) |
| `stop` | array[string] | No | Sequences that halt generation |
| `top_logprobs` | int64 | No | Number of top log probabilities per token (0-20, OpenAI) |
| `max_thinking_tokens` | int64 | No | Max tokens for thinking/reasoning output |
| `provider_options` | object | No | Additional provider-specific options |

### Sampling Parameters

#### Standard Parameters (All Providers)

These parameters are supported by most providers via fantasy's native field support:

| Parameter | Type | Range | Default | Description |
|-----------|------|-------|---------|-------------|
| `temperature` | float64 | 0-1 | 1.0 | Controls randomness. Lower = more deterministic, higher = more random |
| `top_p` | float64 | 0-1 | 1.0 | Nucleus sampling: only consider tokens with cumulative probability ≤ top_p |
| `top_k` | int64 | -1 to ∞ | -1 | Only consider the top K tokens. -1 means all tokens |
| `frequency_penalty` | float64 | -2.0 to 2.0 | 0.0 | Penalizes tokens based on frequency in generated text |
| `presence_penalty` | float64 | -2.0 to 2.0 | 0.0 | Penalizes tokens based on whether they appear in text so far |
| `max_tokens` | int64 | 1 to 200,000 | Provider default | Maximum tokens in the response |

#### Extended Parameters (Provider-Specific)

These parameters are passed through `ProviderOptions` and mapped per-provider:

| Parameter | Type | Description | OpenAI | Anthropic | vLLM/Compat | Google | OpenRouter | Vercel |
|-----------|------|-------------|--------|-----------|-------------|--------|------------|--------|
| `seed` | int64 | Random seed for deterministic sampling | ✓ | — | ✓ | — | — | — |
| `min_p` | float64 | Minimum probability relative to most likely token (0-1) | — | — | ✓ | — | — | — |
| `repetition_penalty` | float64 | Penalty for tokens appearing in prompt+output (>1 discourages repetition) | — | — | ✓ | — | — | — |
| `stop` | array[string] | Sequences that stop generation | ✓ | — | ✓ | — | — | — |
| `top_logprobs` | int64 | Log probabilities of top N tokens per position (0-20) | ✓ | — | — | — | — | ✓ |
| `max_thinking_tokens` | int64 | Max tokens for thinking/reasoning | — | `thinking.budget_tokens` | `extra_body.thinking.max_tokens` | `thinking_config.thinking_budget` | `reasoning.max_tokens` | `reasoning.max_tokens` |

**Notes:**
- ✓ = Supported natively or via direct field mapping
- — = Not supported by this provider
- Parameters not natively supported by fantasy are passed through `extra_body` for openai-compat providers, which typically accept arbitrary fields.

#### Reasoning/Thinking Parameters

Different providers use different field names for reasoning control:

| Provider | Field Path | Values |
|----------|------------|--------|
| OpenAI | `reasoning_effort` | `none`, `minimal`, `low`, `medium`, `high`, `xhigh` |
| Anthropic | `effort` | `low`, `medium`, `high`, `xhigh`, `max` |
| Anthropic | `thinking.budget_tokens` | int64 (token count) |
| Google (Gemini 2+) | `thinking_config.thinking_budget` | int64 |
| Google (older) | `thinking_config.thinking_level` | `LOW`, `MEDIUM`, `HIGH`, `MINIMAL` |
| OpenRouter | `reasoning.enabled` + `reasoning.effort` | bool + effort level |
| Vercel | `reasoning.max_tokens` + `reasoning.effort` | int64 + effort level |
| vLLM/Compat | `extra_body.thinking.max_tokens` | int64 |
| vLLM/Compat | `reasoning_effort` | `none`, `minimal`, `low`, `medium`, `high`, `xhigh`, `max` (passed through for models without catalog-declared reasoning levels, also injected into `chat_template_kwargs` for Jinja-based engines like llama.cpp/vLLM; engines that don't support the field ignore it) |
| vLLM/Compat | `enable_thinking` | `on`/`off` (sets `chat_template_kwargs.enable_thinking`, overriding the provider default of `true`; Jinja templates that don't reference the kwarg ignore it) |

---

## Model Discovery

When you configure a provider without explicit `models`, Phosphor auto-discovers available models by calling the provider's `/v1/models` endpoint (for OpenAI-compatible providers) or using embedded catalog data.

### Controlling Discovery

```json
{
  "providers": {
    "my-provider": {
      "discover_models": true,  // Default: true
      "models": []              // Empty = auto-discover
    }
  }
}
```

To override discovered models:
```json
{
  "providers": {
    "my-provider": {
      "discover_models": true,
      "models": [
        {"id": "my-custom-model", "name": "My Custom Model"}
      ]
    }
  }
}
```

Discovered models are merged with your explicit list (your models take precedence).

---

## Provider Options Merging

Options are merged from three sources in order of priority (lowest to highest):

1. **Catwalk catalog defaults** — Embedded model metadata from the provider catalog
2. **Provider-level `provider_options`** — Set in the `providers.*.provider_options` section
3. **Model-level `provider_options`** — Set in the `models.*.provider_options` section

Merging uses JSON merge semantics: nested objects are merged recursively, arrays are replaced.

### Example

```json
{
  "providers": {
    "openai": {
      "provider_options": {
        "service_tier": "auto",
        "metadata": {"project": "default"}
      }
    }
  },
  "models": {
    "large": {
      "model": "gpt-4o",
      "provider": "openai",
      "provider_options": {
        "metadata": {"project": "my-project"}
      }
    }
  }
}
```

Result for the large model:
```json
{
  "service_tier": "auto",
  "metadata": {"project": "my-project"}
}
```

---

## Extra Body (OpenAI-Compatible Providers)

The `extra_body` field in provider config passes arbitrary fields to OpenAI-compatible API requests. This is useful for vLLM, llama.cpp, and other self-hosted providers that support non-standard parameters.

**Important:** String values in `extra_body` are **not** shell-expanded — they're passed verbatim as JSON.

### Example: vLLM with Custom Sampling

```json
{
  "providers": {
    "gb10": {
      "base_url": "http://localhost:8000/v1",
      "type": "openai-compat",
      "extra_body": {
        "ignore_eos": true,
        "n": 1
      }
    }
  },
  "models": {
    "large": {
      "model": "ornith1.0-35b-nvfp4-mtp",
      "provider": "gb10",
      "min_p": 0.05,
      "repetition_penalty": 1.1
    }
  }
}
```

The `min_p` and `repetition_penalty` from the model config are injected into `extra_body` automatically.

---

## Tool Call Format

Controls how tool calls are elicited from models:

| Format | Description | Use Case |
|--------|-------------|----------|
| `native` (default) | Uses provider's native function-calling API | Most providers |
| `xml` | Injects system prompt instruction for `<tool_call>` text | vLLM, llama.cpp with custom parsers |

```json
{
  "providers": {
    "gb10": {
      "tool_call_format": "xml"
    }
  }
}
```

---

## Complete Example

```json
{
  "$schema": "https://raw.githubusercontent.com/hackafterdark/phosphor/main/schema.json",
  "providers": {
    "openai": {
      "api_key": "$OPENAI_API_KEY",
      "models": [
        {"id": "gpt-4o", "name": "GPT-4o"},
        {"id": "gpt-4o-mini", "name": "GPT-4o Mini"}
      ]
    },
    "anthropic": {
      "api_key": "$ANTHROPIC_API_KEY",
      "type": "anthropic",
      "models": [
        {"id": "claude-sonnet-4-20250514", "name": "Claude Sonnet 4"}
      ]
    },
    "gb10": {
      "base_url": "http://localhost:8000/v1",
      "type": "openai-compat",
      "tool_call_format": "xml",
      "models": [
        {"id": "ornith1.0-35b-nvfp4-mtp", "name": "Ornith 1.0 35B"}
      ]
    }
  },
  "models": {
    "large": {
      "model": "gpt-4o",
      "provider": "openai",
      "reasoning_effort": "medium",
      "temperature": 0.7,
      "top_p": 0.9,
      "max_tokens": 8192,
      "seed": 42
    },
    "small": {
      "model": "gpt-4o-mini",
      "provider": "openai",
      "temperature": 0.5
    }
  }
}
```

---

## Environment Variables

API keys and other sensitive values support `$VAR` expansion:

```json
{
  "providers": {
    "openai": {
      "api_key": "$OPENAI_API_KEY"
    }
  }
}
```

Shell commands are also supported:

```json
{
  "providers": {
    "openai": {
      "api_key": "$(cat ~/.secrets/openai-key)"
    }
  }
}
```

---

## Troubleshooting

### Model Not Found

If you configure a model that doesn't exist in the provider's catalog:
1. Check that `discover_models` is enabled (default: `true`)
2. Verify the model ID matches the provider's API
3. Explicitly list models in the provider config

### Sampling Parameters Not Applied

- **Native parameters** (`temperature`, `top_p`, etc.) work with all providers
- **Extended parameters** (`seed`, `min_p`, etc.) depend on fantasy's SDK wrapper
- **vLLM/compat providers** should work for most extended params via `extra_body`
- Test against your actual provider to confirm parameter support

### Thinking/Reasoning Not Working

- Ensure the model supports reasoning (check provider documentation)
- Use the correct field name for your provider (see Reasoning/Thinking Parameters table)
- Some providers require explicit `think: true` or `reasoning_effort` setting

---

## See Also

- [phosphor.json schema](https://raw.githubusercontent.com/hackafterdark/phosphor/main/schema.json)
- [VLLM Sampling Parameters](https://docs.vllm.ai/en/v0.6.5/dev/sampling_params.html)
- [OpenAI Chat API Reference](https://platform.openai.com/docs/api-reference/chat/create)
- [Anthropic Messages API](https://docs.anthropic.com/en/api/messages)

---

## Logging Configuration

Application logging is **disabled by default** (safe by default). Enable it explicitly in `phosphor.json`:

```jsonc
{
  "logging": {
    "enabled": true,
    "level": "info",
    "max_size_mb": 10,
    "max_age_days": 30,
    "max_backups": 0,
    "compress": false,
    "filters": [
      {"field": "msg", "pattern": ".*password.*"},
      {"field": "msg", "pattern": ".*secret.*"}
    ]
  }
}
```

### Fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | `bool` | `false` | Enable application logging. **Default is `false`** — logging is off unless explicitly enabled. |
| `level` | `"off" \| "info" \| "debug"` | `"info"` | slog log level. `"off"` disables logging even if `enabled: true`. |
| `max_size_mb` | `int` | `10` | Maximum log file size in MB before rotation (lumberjack). Max 1000. |
| `max_age_days` | `int` | `30` | Maximum age of log files in days before cleanup. |
| `max_backups` | `int` | `0` | Maximum number of old rotated log files to retain. |
| `compress` | `bool` | `false` | Enable gzip compression of rotated log files. |
| `filters` | `LogFilter[]` | `[]` | Regex filters to exclude matching log entries. See below. |

### Filter Rules

Filters exclude log entries whose fields match the regex pattern. Each filter has:

- `field`: JSON key in the log entry (e.g., `"msg"`, `"source"`, or any custom key)
- `pattern`: Regular expression matched against the field value (case-insensitive field matching)

Example — redact sensitive messages:

```json
"filters": [
  {"field": "msg", "pattern": ".*password.*"},
  {"field": "msg", "pattern": ".*token.*"},
  {"field": "api_key", "pattern": ".*"}
]
```

### Log File Location

When enabled, logs are written to `{data_directory}/logs/phosphor.log` in JSON format (one JSON object per line). The CLI `phosphor logs --tail N` command reads the last N lines.

### Security Considerations

- Logging is **off by default** — the agent cannot read logs unless you enable it
- Use `filters` to exclude sensitive data (API keys, passwords, tokens) from log output
- The `phosphor_logs` agent tool can read log files — only enable when needed for debugging
- Log rotation prevents disk space issues (default: 10 MB per file, 30 days retention)
