# Configuration

Everything Phosphor reads at startup lives in one or more `phosphor.json`
files. The configuration is organized into topical reference pages under
[`docs/configuration/`](configuration/).

## Quick Reference

| Page | Covers |
|------|--------|
| [Files & Merge Order](configuration/FILES_AND_MERGING.md) | Where config lives, merge order, env vars, schema |
| [Providers & Models](configuration/PROVIDERS_AND_MODELS.md) | Provider config, model selection, sampling, reasoning |
| [Local Models](configuration/LOCAL_MODELS.md) | OpenAI-compatible servers (Ollama, vLLM, llama.cpp, LM Studio) |
| [Context & Data](configuration/CONTEXT_AND_DATA.md) | Context files, data directory, skills paths |
| [Agent Behavior](configuration/AGENT_BEHAVIOR.md) | Turn limits, compaction, reflection, summarization |
| [TUI Appearance](configuration/TUI_APPEARANCE.md) | Themes, keybindings, layout |
| [Tools & Permissions](configuration/TOOLS_AND_PERMISSIONS.md) | Tool security knobs, permission prompts |
| [MCP](configuration/MCP.md) | Model Context Protocol servers |
| [LSP & AST](configuration/LSP_AST.md) | Language Server Protocol and tree-sitter |
| [Hooks](configuration/HOOKS.md) | Lifecycle shell hooks |
| [Security](configuration/SECURITY.md) | Egress, redaction, read-only mode |
| [Observability & Logging](configuration/OBSERVABILITY.md) | OTel traces, log files |
| [Services](configuration/SERVICES.md) | OpenAI API, cron, memory-graph |
| [Workspace Search](configuration/WORKSPACE_SEARCH.md) | FTS5 and vector indexing |
| [Memory](configuration/MEMORY.md) | Durable memory vault |

## Quick Start

1. **Pick a backend.** Cloud providers work out of the box: built-in provider
   definitions ship with Phosphor and activate automatically when their API key
   is available — either as an environment variable (`OPENAI_API_KEY`,
   `ANTHROPIC_API_KEY`, …) or as an `api_key` in the `providers` section.
   Subscription-style providers authenticate via the CLI:

   ```bash
   phosphor login copilot    # GitHub Copilot
   phosphor login xai        # xAI (Grok subscription)
   ```

   For a local model, see [Local Models](configuration/LOCAL_MODELS.md).

2. **Run it and select a model.** Launch `phosphor` in your project and press
   `Ctrl+L` (or `/` → "Switch Model") to choose the large/small model pair.
   No config file is required to start: cloud providers activate from their
   env vars, and the TUI can add custom/local providers for you.

3. **Optional: global config.** Phosphor reads
   `~/.config/phosphor/phosphor.json` if it exists, but never creates it and
   runs fine without it. Copying the example there is only a convenience for
   editing settings by hand:

   ```bash
   # Linux/macOS
   mkdir -p ~/.config/phosphor
   cp examples/phosphor.example.json ~/.config/phosphor/phosphor.json

   # Windows (PowerShell)
   New-Item -ItemType Directory -Force $HOME\.config\phosphor
   Copy-Item examples\phosphor.example.json $HOME\.config\phosphor\phosphor.json
   ```

   > **Your TUI setup is not affected.** Providers and model selections you
   > make in the TUI are written to a **separate** file — the global data
   > config at `~/.local/share/phosphor/phosphor.json` — which always
   > out-ranks the global config on any conflicting key. Copying the example
   > into `~/.config/phosphor/phosphor.json` therefore cannot overwrite or
   > clobber anything the TUI configured. See
   > [Files & Merge Order](configuration/FILES_AND_MERGING.md).

Non-interactive runs use `phosphor run "your prompt"` — handy for scripts and CI.

## Useful Commands

| Command | Purpose |
|---------|---------|
| `phosphor dirs` | Print resolved config/data/project paths |
| `phosphor models` | List every model Phosphor can currently reach |
| `phosphor login [platform]` | OAuth sign-in (copilot, xai) |
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

## See Also

- [MODELS_AND_PROVIDERS_CONFIG.md](configuration/PROVIDERS_AND_MODELS.md) — Full provider/model reference
- [examples/phosphor.example.json](../examples/phosphor.example.json) — Copy-paste example
