# Getting Started & Configuration

Everything Phosphor reads at startup lives in a single JSON file: `phosphor.json`.
This document covers where that file lives on each operating system, how Phosphor
merges global and project-level copies, and gives a copy-paste snippet for each
configurable section. If you just want to point Phosphor at a local model served
by llama.cpp, vLLM, Ollama, or LM Studio, jump straight to
[Running a local model](#running-a-local-model-openai-compatible-servers).

A complete, ready-to-edit example lives at
[`examples/phosphor.example.json`](../examples/phosphor.example.json) in the
repository.

## Where configuration lives

Phosphor loads several `phosphor.json` files and merges them. The files, in the
order they are loaded (later files win on conflicts):

| # | File | Who writes it | Location |
|---|------|---------------|----------|
| 1 | Global config | You | `$XDG_CONFIG_HOME/phosphor/phosphor.json` (fallback `~/.config/phosphor/phosphor.json`) |
| 2 | Global data config | Phosphor | Linux/macOS: `$XDG_DATA_HOME/phosphor/phosphor.json` (fallback `~/.local/share/phosphor/phosphor.json`) · Windows: `%LOCALAPPDATA%\phosphor\phosphor.json` |
| 3 | Project configs | You | Any `phosphor.json` or `.phosphor.json` from the current directory up to the git working-tree root |

Windows uses the same XDG logic for the global config, so the default is
`%USERPROFILE%\.config\phosphor\phosphor.json` unless `XDG_CONFIG_HOME` is set.

The **global data config** is where Phosphor persists things it learns at runtime
(recently used models, per-provider settings saved from the TUI). The **project
config** is safe to commit to a repository to share settings with your team.

The workspace also gets a **data directory** (default `.phosphor/` at the project
root, configurable via `options.data_directory` or `--data-dir`). It holds the
SQLite session database, logs, and the workspace-scoped `.phosphor/phosphor.json`
that the TUI writes when you pick a model from the model dialog (`Ctrl+L`).
Workspace-scoped settings take precedence over global ones.

To print the exact paths Phosphor is using on your machine:

```bash
phosphor dirs
```

Environment variables that relocate these paths:

| Variable | Effect |
|----------|--------|
| `XDG_CONFIG_HOME` | Overrides the base for the global config dir |
| `XDG_DATA_HOME` | Overrides the base for the global data config (Linux/macOS) |
| `PHOSPHOR_GLOBAL_CONFIG` | Set to a directory; Phosphor uses `<dir>/phosphor.json` as the global config |
| `PHOSPHOR_GLOBAL_DATA` | Set to a directory; overrides the global data config location |
| `PHOSPHOR_CACHE_DIR` | Overrides the cache directory |
| `PHOSPHOR_SKILLS_DIR` | Overrides the global skills directory |

Phosphor also honors a `PHOSPHOR_` prefix overlay: at startup, any variable named
`PHOSPHOR_FOO=bar` is exposed to provider resolution as `FOO=bar`. This lets you
scope keys per shell without clobbering a global `OPENAI_API_KEY`, for example.

## Schema and validation

Add the `$schema` key to get editor autocompletion and validation for every
section described below:

```json
{
  "$schema": "https://raw.githubusercontent.com/hackafterdark/phosphor/main/schema.json"
}
```

The schema is generated from the live config structs; regenerate it locally with
`phosphor schema` if you are on a development build.

## Quick start

1. **Pick a backend.** Cloud providers work out of the box: built-in provider
   definitions ship with Phosphor and activate automatically when their API key
   is available — either as an environment variable (`OPENAI_API_KEY`,
   `ANTHROPIC_API_KEY`, …) or as an `api_key` in the `providers` section.
   Subscription-style providers authenticate via the CLI:

   ```bash
   phosphor login            # Charm Hyper
   phosphor login copilot    # GitHub Copilot
   ```

   For a local model, see [Running a local model](#running-a-local-model-openai-compatible-servers).

2. **Create your global config** (one-time):

   ```bash
   # Linux/macOS
   mkdir -p ~/.config/phosphor
   cp examples/phosphor.example.json ~/.config/phosphor/phosphor.json

   # Windows (PowerShell)
   New-Item -ItemType Directory -Force $HOME\.config\phosphor
   Copy-Item examples\phosphor.example.json $HOME\.config\phosphor\phosphor.json
   ```

3. **Run it and select a model.** Launch `phosphor` in your project and press
   `Ctrl+L` (or `/` → "Switch Model") to choose the large/small model pair. The
   selection is saved to the workspace data directory.

Pointing Phosphor at a local server is also available as a guided flow: open
the command palette (`Ctrl+P`) and pick **Add Custom Provider** — the wizard
collects the name, base URL, optional API key, and model ids, then writes the
provider to your global config for you. Pick **Manage Custom Providers** to
list your providers, edit any field (down to per-model sampling options), or
delete one.

Non-interactive runs use `phosphor run "your prompt"` — handy for scripts and CI.

## Running a local model (OpenAI-compatible servers)

Any server that speaks the OpenAI chat-completions API works as an
`"type": "openai-compat"` provider. Define the provider with a `base_url`
pointing at your server, enable model discovery, and select one of its models:

```json
{
  "providers": {
    "local": {
      "name": "Local Server",
      "base_url": "http://localhost:11434/v1",
      "type": "openai-compat",
      "api_key": "$LOCAL_API_KEY",
      "discover_models": true
    }
  },
  "models": {
    "large": { "model": "qwen3-32b", "provider": "local" },
    "small": { "model": "qwen3-32b", "provider": "local" }
  }
}
```

Typical endpoints per server (start your server however you normally do; this
only covers how to point Phosphor at it):

| Server | Usual `base_url` | Notes |
|--------|------------------|-------|
| [Ollama](https://ollama.com) | `http://localhost:11434/v1` | `discover_models` lists installed models |
| [llama.cpp](https://github.com/ggml-org/llama.cpp) (`llama-server`) | `http://localhost:8080/v1` | Set `--port` to match |
| [vLLM](https://docs.vllm.ai) | `http://localhost:8000/v1` | See tool-call note below |
| [LM Studio](https://lmstudio.ai) | `http://localhost:1234/v1` | Start the local server from the app |

Notes that smooth the local-model path:

- **API key.** Local servers usually don't check one. You can omit `api_key`,
  set a placeholder like `"not-needed"`, or reference an env var with `$VAR`
  (values are shell-expanded at config load, `$(some-command)` works too for
  key-retrieval commands).
- **Model lists.** With `"discover_models": true` Phosphor pulls the model list
  from `/v1/models`. List models explicitly instead (or in addition) when you
  want to attach metadata — the fields Phosphor uses from each entry are
  `context_window`, `default_max_tokens`, `can_reason`, and `supports_attachments`
  (plus sampling knobs under `options`; see the
  [model entry field table](MODELS_AND_PROVIDERS_CONFIG.md#model-entry-fields-models)
  for the full list):

  ```json
  {
    "providers": {
      "local": {
        "base_url": "http://localhost:8000/v1",
        "type": "openai-compat",
        "discover_models": false,
        "models": [
          {
            "id": "qwen3-32b",
            "name": "Qwen3 32B",
            "context_window": 32768,
            "default_max_tokens": 8192,
            "can_reason": true,
            "supports_attachments": false
          }
        ]
      }
    }
  }
  ```

- **Tool calling.** Local stacks are the one place where tool calls sometimes
  can't use the native function-calling path. If your server's parser expects
  `<tool_call>…</tool_call>` text instead of native tool calls (common with some
  vLLM/llama.cpp setups and chat templates), set `"tool_call_format": "xml"` on
  the provider.
- **Thinking toggles.** For models whose chat template has an
  "enable_thinking" kwarg, set ""enable_thinking": "off"" (or ""on"") on the
  "models.large" / "models.small" selection to pin it; omitting it leaves the
  server default.
- **Server-specific fields.** Anything non-standard can ride along verbatim in
  request bodies via `extra_body` (openai-compat providers only):

  ```json
  {
    "providers": {
      "local": {
        "base_url": "http://localhost:8080/v1",
        "type": "openai-compat",
        "extra_body": { "repeat_penalty": 1.1 }
      }
    }
  }
  ```

- **Disable the cloud defaults** if you only ever run local, so nothing can
  accidentally phone home: `"options": { "disable_default_providers": true }`
  (with this on, every provider must be fully specified in your config).

Use `phosphor models` to verify what Phosphor can see per provider. For the full
field reference — every provider field, every sampling parameter, per-provider
support matrices — see
[MODELS_AND_PROVIDERS_CONFIG.md](MODELS_AND_PROVIDERS_CONFIG.md).

## The config sections

A `phosphor.json` file can contain any subset of these top-level sections. Only
include what you need; everything else has safe defaults.

| Section | What it configures | Deep dive |
|---------|--------------------|-----------|
| `providers` | API endpoints, keys, model lists | [Models & Providers](MODELS_AND_PROVIDERS_CONFIG.md) |
| `models` | Which model is `large`/`small` and how it samples | [Models & Providers](MODELS_AND_PROVIDERS_CONFIG.md) |
| `mcp` | Model Context Protocol servers | — |
| `lsp` | Language Server Protocol servers | — |
| `options` | App behavior: context files, data dir, tools off, TUI, agent | [Themes](THEMES.md), [Keybindings](KEYBINDINGS.md), [Layout](UI_LAYOUT_CONFIG.md), [Compaction](COMPACTION.md) |
| `permissions` | Tools that skip permission prompts | — |
| `tools` | Per-tool security posture (bash, web, ls, grep) | [Security config](security/CONFIGURATION.md) |
| `hooks` | Shell commands fired on agent lifecycle events | [Hooks](hooks/README.md) |
| `observability` | OpenTelemetry traces/metrics export | [OTel](observability/OPEN_TELEMETRY.md) |
| `logging` | Log file, level, rotation, filters | [Logging](observability/LOGGING.md) |
| `services` | Extra platform services (OpenAI-compatible API, cron) | [OpenAI API](platform/openai-api.md), [Scheduled jobs](platform/SCHEDULED_JOBS.md) |
| `security` | Egress, redaction, read-only mode, tool blacklist | [Security config](security/CONFIGURATION.md) |
| `workspace_search` | FTS5 full-text + vector code indexing | [Workspace search](tools/WORKSPACE_SEARCH.md) |
| `memory` | The durable memory vault | [Memory](memory/OVERVIEW.md) |

### `providers` and `models`

Covered in depth above and in [MODELS_AND_PROVIDERS_CONFIG.md](MODELS_AND_PROVIDERS_CONFIG.md).
The minimum viable cloud setup:

```json
{
  "providers": {
    "anthropic": {
      "api_key": "$ANTHROPIC_API_KEY",
      "models": [{ "id": "claude-sonnet-4-20250514", "name": "Claude Sonnet 4" }]
    }
  },
  "models": {
    "large": { "model": "claude-sonnet-4-20250514", "provider": "anthropic" },
    "small": { "model": "claude-haiku-4-5-20251001", "provider": "anthropic" }
  }
}
```

`large` is the primary coding/reasoning model; `small` handles title generation
and lightweight sub-tasks. Model entries also accept sampling overrides
(`temperature`, `top_p`, `top_k`, `max_tokens`, `seed`, `min_p`,
`repetition_penalty`, `stop`, …) plus `reasoning_effort` (OpenAI-style),
`think` (Anthropic), and `max_thinking_tokens`.

### `mcp`

Attach MCP servers; stdio processes or HTTP/SSE endpoints:

```json
{
  "mcp": {
    "context7": {
      "command": "npx",
      "args": ["-y", "@upstash/context7-mcp"],
      "type": "stdio",
      "env": {}
    },
    "remote-tools": {
      "type": "http",
      "url": "http://localhost:3000/mcp",
      "headers": { "authorization": "Bearer $MY_TOKEN" },
      "timeout": 30,
      "enabled_tools": ["search-docs"],
      "disabled_tools": []
    }
  }
}
```

`disabled`, `enabled_tools`, and `disabled_tools` let you scope what each server
exposes. Header values support `$VAR`/`$(cmd)` expansion; a header that expands
to an empty string is omitted rather than sent blank.

### `lsp`

Phosphor auto-starts language servers it knows about (based on root markers like
`go.mod` or `package.json`) unless `options.auto_lsp` is `false`. Override or
extend the table of contents:

```json
{
  "options": { "auto_lsp": true },
  "lsp": {
    "gopls": {
      "command": "gopls",
      "filetypes": ["go", "mod"],
      "root_markers": ["go.mod"],
      "init_options": {},
      "timeout": 30
    },
    "rust-analyzer": { "disabled": true }
  }
}
```

### `options`

The catch-all for app behavior. Highlights:

```json
{
  "options": {
    "context_paths": ["AGENTS.md", "PHOSPHOR.md", ".cursorrules"],
    "global_context_paths": ["~/.config/phosphor/PHOSPHOR.md"],
    "skills_paths": ["~/.config/phosphor/skills", "./skills"],
    "data_directory": ".phosphor",
    "disabled_tools": ["sourcegraph"],
    "disabled_skills": ["phosphor-config"],
    "disable_auto_summarize": false,
    "summarize_threshold": 0.8,
    "summarize_model": "small",
    "notification_style": "auto",
    "initialize_as": "AGENTS.md",
    "disable_default_providers": false,
    "agent": {
      "active_profile": "default",
      "enable_reflection": true,
      "max_turns": 60,
      "structural_search_languages": ["go", "typescript"]
    }
  }
}
```

- `context_paths` — files injected as project context on every prompt. Phosphor
  always picks up `AGENTS.md`, `PHOSPHOR.md`, `CLAUDE.md`, `GEMINI.md` (and
  `.local` variants) from the working directory; add anything else here.
- `data_directory` — where the SQLite DB, logs, and workspace config go.
  Relative paths resolve against the working directory.
- `summarize_threshold` — fraction of the context window that triggers
  auto-compaction ([details](COMPACTION.md)).
- `agent.max_turns` — hard cap on tool-use turns per prompt, a guardrail against
  runaway loops.

`options.tui` handles look-and-feel:

```json
{
  "options": {
    "tui": {
      "theme": "pantera",
      "compact_mode": false,
      "diff_mode": "unified",
      "transparent": false,
      "history_limit": 100,
      "history_batch_size": 50,
      "keybindings": { "models": "ctrl+m,ctrl+l" }
    }
  }
}
```

See [THEMES.md](THEMES.md), [KEYBINDINGS.md](KEYBINDINGS.md), and
[UI_LAYOUT_CONFIG.md](UI_LAYOUT_CONFIG.md) for themes, custom key bindings, and
landing/sidebar layout.

### `permissions`

Tools listed here run without a permission prompt (per-tool asks still apply to
everything else):

```json
{
  "permissions": {
    "allowed_tools": ["view", "grep", "glob", "ls", "structural_search"]
  }
}
```

### `tools`

Per-tool security knobs. The bash tool confines the agent's shell: which env
vars survive, which extra commands are banned, whether inline interpreter
execution (`python -c …`) is allowed, which extra roots are trusted, and an
opt-in network egress policy. The web tools can allowlist private IP ranges:

```json
{
  "tools": {
    "bash": {
      "allowed_env": ["PATH", "HOME", "GOPATH", "PATH_TO_TOOL"],
      "banned_commands": ["shutdown"],
      "allow_inline_execution": false,
      "trusted_extra_roots": ["D:/shared-build-cache"],
      "network": {
        "enabled": true,
        "allowed_commands": ["git", "ping"],
        "host_allowlist": ["github.com"]
      }
    },
    "web_fetch": {
      "ip_allow_list": ["192.168.0.0/24", "127.0.0.1"],
      "allow_raw_ips": false
    },
    "ls": { "max_depth": 10, "max_items": 500 },
    "grep": { "timeout": 10000000000 }
  }
}
```

(`grep.timeout` is a Go duration in nanoseconds — `10000000000` = 10s.)

Every control and its security implications:
[security/CONFIGURATION.md](security/CONFIGURATION.md),
[ENVIRONMENT_HARDENING.md](security/ENVIRONMENT_HARDENING.md),
[NETWORK_EGRESS_HARDENING.md](security/NETWORK_EGRESS_HARDENING.md).

### `hooks`

Shell scripts that fire on lifecycle events and can block, rewrite, or annotate
tool calls:

```json
{
  "hooks": {
    "PreToolUse": [
      {
        "name": "block-env-edits",
        "matcher": "^edit$",
        "command": "./.phosphor/hooks/gate.sh",
        "timeout": 30
      }
    ]
  }
}
```

The command receives a JSON payload on stdin and returns a decision on stdout.
Full protocol, events, and examples: [hooks/README.md](hooks/README.md).

### `observability`

Export traces/metrics over OTLP:

```json
{
  "observability": {
    "endpoint": "http://localhost:4317",
    "protocol": "grpc",
    "service_name": "phosphor",
    "sampling_rate": 1.0,
    "resource_attributes": { "team": "platform" },
    "sensitive_mcp_servers": ["vault"],
    "capture_input_messages": false,
    "capture_output_messages": false
  }
}
```

Message capture is opt-in because it puts full prompts and responses in your
trace backend. See [observability/OPEN_TELEMETRY.md](observability/OPEN_TELEMETRY.md).

### `logging`

Logging is off unless you ask for it:

```json
{
  "logging": {
    "enabled": true,
    "level": "debug",
    "max_size_mb": 10,
    "max_age_days": 7,
    "max_backups": 3,
    "compress": true,
    "filters": [{ "field": "msg", "pattern": ".*heartbeat.*" }]
  }
}
```

The file lands in `<data_directory>/logs/phosphor.log`. Details and filtering:
[observability/LOGGING.md](observability/LOGGING.md). (`options.debug` turns on
debug logging too; `options.debug_lsp` adds LSP wire traffic.)

### `services`

Optional always-on platform services: an OpenAI-compatible HTTP API that fronts
the agent, and the cron scheduler for unattended runs:

```json
{
  "services": {
    "openai-api": {
      "enabled": true,
      "host": "127.0.0.1",
      "port": 8643,
      "auth": { "type": "bearer", "key": "$PHOSPHOR_API_TOKEN" }
    },
    "cron": { "enabled": true, "jobs_directory": ".phosphor/jobs" }
  }
}
```

Docs: [platform/openai-api.md](platform/openai-api.md),
[platform/SCHEDULED_JOBS.md](platform/SCHEDULED_JOBS.md).

### `security`

The defense-in-depth dials: egress posture, secret/PII redaction on the wire and
at read time, forced read-only mode, and a tool blacklist:

```json
{
  "security": {
    "read_only": false,
    "tool_blacklist": ["bash"],
    "allowed_egress": { "http": true },
    "redact_outgoing_secrets": true,
    "redact_outgoing_pii": false,
    "redact_sensitive_files": true,
    "sensitive_file_patterns": ["secrets/**/*.json"],
    "tokenize_secrets": false
  }
}
```

Defaults are already the secure positions (redaction on, tokenization off, PII
masking off because it false-positives on code). Don't loosen anything here
without reading [security/CONFIGURATION.md](security/CONFIGURATION.md) first.

### `workspace_search`

Zero-API full-text (FTS5) indexing plus optional vector embeddings:

```json
{
  "workspace_search": {
    "fulltext": {
      "enabled": true,
      "auto_index": true,
      "debounce_ms": 2000,
      "exclude_patterns": ["node_modules/**", "dist/**"],
      "max_file_size": 1048576,
      "index_documents": true
    },
    "vector_embeddings": {
      "enabled": false,
      "auto_index": false,
      "max_chunk_size": 512,
      "chunk_overlap": 128,
      "embedding_dims": 384
    }
  }
}
```

Full-text indexing is cheap and stays fresh automatically once enabled; vector
indexing spends embedding-model calls, so it's off by default
([CODEBASE_INDEXING.md](CODEBASE_INDEXING.md),
[tools/WORKSPACE_SEARCH.md](tools/WORKSPACE_SEARCH.md)).

### `memory`

The durable memory vault (decisions/constraints that survive sessions):

```json
{
  "memory": {
    "enabled": true,
    "ask": "balanced",
    "adaptive": true,
    "proactive_recall": "hint",
    "cross_session": "related",
    "max_inject_bytes": 8192
  }
}
```

`ask` is the write-approval dial (`ask` | `balanced` | `auto`). Everything else,
including category menus and injection caps: [memory/OVERVIEW.md](memory/OVERVIEW.md).

## Useful commands

| Command | Purpose |
|---------|---------|
| `phosphor dirs` | Print resolved config/data/project paths |
| `phosphor models` | List every model Phosphor can currently reach |
| `phosphor login [platform]` | OAuth sign-in (hyper, copilot) |
| `phosphor logout [platform]` | Clear stored OAuth credentials |
| `phosphor run "prompt"` | One-shot non-interactive agent run |
| `phosphor schema` | Emit the JSON schema for `phosphor.json` |
| `phosphor server` | Run the client/server backend process |

## Troubleshooting

- **"invalid JSON in config file …"** — the loader rejects malformed JSON
  outright; fix the syntax rather than chasing a silent default.
- **A provider is missing from the model dialog** — it was skipped because its
  API key didn't resolve. Check the env var (`phosphor dirs` then your shell),
  or set `api_key` explicitly.
- **Local server returns 401** — some servers still require a non-empty
  `Authorization` header; set `"api_key": "not-needed"`.
- **Tool calls render as text** — your server's parser doesn't match the native
  function-calling format; set `"tool_call_format": "xml"` on the provider.
- **Changes not picked up** — configs merge global → data → project (nearest
  last); a workspace `.phosphor/phosphor.json` written by the model dialog
  overrides your global file. Check `phosphor dirs` for the merge order.
