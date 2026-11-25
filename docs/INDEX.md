# Documentation Index

A complete map of everything under `docs/`. In-app, the same corpus is
searchable through the `phosphor_docs` tool or by viewing any
`phosphor://docs/<path>` file.

## Getting Started

| Doc | Covers |
| --- | --- |
| [CONFIGURATION.md](CONFIGURATION.md) | Configuration index — links to all topical config pages. Start here. |
| [VIBE_CODING.md](VIBE_CODING.md) | High-trust single-workspace setup: prompt-free toolchain/git/gh loop with the sandbox containing the blast radius. |
| [CLI.md](CLI.md) | Global flags, subcommands, and per-command options for the `phosphor` CLI. |
| [CORE_PHILOSOPHY.md](CORE_PHILOSOPHY.md) | Identity and core pillars of the project. |
| [KEYBINDINGS.md](KEYBINDINGS.md) | Default TUI key bindings and how to configure custom ones. |

## Configuration

| Doc | Covers |
| --- | --- |
| [configuration/FILES_AND_MERGING.md](configuration/FILES_AND_MERGING.md) | Config file locations, merge order, env vars, schema. |
| [configuration/PROVIDERS_AND_MODELS.md](configuration/PROVIDERS_AND_MODELS.md) | Provider config, model selection, sampling, reasoning. |
| [configuration/LOCAL_MODELS.md](configuration/LOCAL_MODELS.md) | OpenAI-compatible servers (Ollama, vLLM, llama.cpp, LM Studio). |
| [configuration/CONTEXT_AND_DATA.md](configuration/CONTEXT_AND_DATA.md) | Context files, data directory, skills paths. |
| [configuration/AGENT_BEHAVIOR.md](configuration/AGENT_BEHAVIOR.md) | Turn limits, compaction, reflection, summarization. |
| [configuration/TUI_APPEARANCE.md](configuration/TUI_APPEARANCE.md) | Themes, keybindings, layout. |
| [configuration/TOOLS_AND_PERMISSIONS.md](configuration/TOOLS_AND_PERMISSIONS.md) | Tool security knobs, permission prompts. |
| [configuration/MCP.md](configuration/MCP.md) | MCP server configuration. |
| [configuration/LSP_AST.md](configuration/LSP_AST.md) | LSP and AST (tree-sitter) configuration. |
| [configuration/HOOKS.md](configuration/HOOKS.md) | Lifecycle shell hooks. |
| [configuration/SECURITY.md](configuration/SECURITY.md) | Egress, redaction, read-only mode. |
| [configuration/OBSERVABILITY.md](configuration/OBSERVABILITY.md) | OTel traces, log files. |
| [configuration/SERVICES.md](configuration/SERVICES.md) | OpenAI API, cron, memory-graph services. |
| [configuration/WORKSPACE_SEARCH.md](configuration/WORKSPACE_SEARCH.md) | FTS5 and vector indexing. |
| [configuration/MEMORY.md](configuration/MEMORY.md) | Durable memory vault. |
| [THEMES.md](THEMES.md) | Customizing the color scheme and branding via theme files. |
| [LOGO_CUSTOMIZATION.md](LOGO_CUSTOMIZATION.md) | Logo text and FIGlet font settings. |
| [UI_LAYOUT_CONFIG.md](UI_LAYOUT_CONFIG.md) | Landing screen and sidebar component layout. |
| [observability/LOGGING.md](observability/LOGGING.md) | Enabling and configuring the log file. |
| [observability/OPEN_TELEMETRY.md](observability/OPEN_TELEMETRY.md) | OTel tracing and metrics across agent activity. |

## Tools

| Doc | Covers |
| --- | --- |
| [tools/OVERVIEW.md](tools/OVERVIEW.md) | Catalog of all built-in agent tools. |
| [tools/edit_tools.md](tools/edit_tools.md) | `edit` and `multiedit`: matching, safety, and diagnostics. |
| [tools/SCAN_SECRETS.md](tools/SCAN_SECRETS.md) | Scanning files and git history for leaked credentials. |
| [tools/SEMANTIC_SEARCH.md](tools/SEMANTIC_SEARCH.md) | Embedding-based natural-language code search. |
| [tools/TOOL_SEARCH.md](tools/TOOL_SEARCH.md) | Deferred MCP tool schemas and on-demand discovery. |
| [tools/WORKSPACE_SEARCH.md](tools/WORKSPACE_SEARCH.md) | Zero-API full-text search over symbols and docs. |
| [structural_search/README.md](structural_search/README.md) | AST-based structural search with tree-sitter. |
| [structural_search/CONFIGURATION.md](structural_search/CONFIGURATION.md) | Filtering which languages the tool advertises. |
| [structural_search/LANGUAGE_NOTES.md](structural_search/LANGUAGE_NOTES.md) | Per-language support notes and caveats. |

## Context and Memory

| Doc | Covers |
| --- | --- |
| [COMPACTION.md](COMPACTION.md) | Auto-summarization of session history near capacity. |
| [HISTORY_LIMIT.md](HISTORY_LIMIT.md) | Chat history pagination and rendering performance. |
| [CONTEXT_CHAIN.md](CONTEXT_CHAIN.md) | Context values propagated through the agent system. |
| [SYSTEM_PROMPT.md](SYSTEM_PROMPT.md) | How the modular system prompt is composed. |
| [CODEBASE_INDEXING.md](CODEBASE_INDEXING.md) | Workspace indexing for semantic and full-text search. |

## Memory

| Doc | Covers |
| --- | --- |
| [memory/OVERVIEW.md](memory/OVERVIEW.md) | How the memory system works: vaults, the entry model, write/read paths, Tier A/B injection, config, `/memory`. |
| [memory/INTEGRITY.md](memory/INTEGRITY.md) | The tamper seal: the HMAC key, what is signed vs. deliberately exempt, the threat model, key operations. |
| [memory/POISONING.md](memory/POISONING.md) | The layered defenses against a poisoned memory entry reaching the system prompt. |
| [memory/MEMORYGRAPH.md](memory/MEMORYGRAPH.md) | The memory graph viewer: live server, report export, host/port config, and node/edge semantics. |

## Extending the Agent

| Doc | Covers |
| --- | --- |
| [SKILLS.md](SKILLS.md) | The skill system for teaching the agent workflows. |
| [hooks/README.md](hooks/README.md) | Shell-script hooks fired on agent-lifecycle events. |
| [commands/GOAL.md](commands/GOAL.md) | `/goal`: keeping the agent on one verifiable objective. |
| [commands/LANGUAGES.md](commands/LANGUAGES.md) | `/languages` dialog for structural search. |
| [commands/LEARN.md](commands/LEARN.md) | `/learn`: turning reference material into skills. |
| [commands/MENU.md](commands/MENU.md) | `/menu`: quick access to common dialogs. |
| [commands/NAME.md](commands/NAME.md) | `/name`: view or rename the session. |
| [commands/PIN_SESSION.md](commands/PIN_SESSION.md) | Pinning sessions against deletion and pruning. |
| [commands/QUIT.md](commands/QUIT.md) | `/quit`. |
| [commands/STATS.md](commands/STATS.md) | `/stats`: token and cost usage. |

## Security

| Doc | Covers |
| --- | --- |
| [security/HARDENING.md](security/HARDENING.md) | Security philosophy, the control stack, and a recommended hardening baseline. Start here. |
| [security/CONFIGURATION.md](security/CONFIGURATION.md) | Every security control and how to configure it. |
| [security/WORKSPACE_HARDENING.md](security/WORKSPACE_HARDENING.md) | Filesystem confinement and the workspace threat model. |
| [security/ENVIRONMENT_HARDENING.md](security/ENVIRONMENT_HARDENING.md) | Environment variable filtering in shell commands. |
| [security/NETWORK_EGRESS_HARDENING.md](security/NETWORK_EGRESS_HARDENING.md) | The network egress firewall for fetch/search. |
| [security/SECRETS_PROTECTION.md](security/SECRETS_PROTECTION.md) | Defense-in-depth against secrets reaching the model. |
| [security/SECURITY_VALIDATION_TEST_SUITE.md](security/SECURITY_VALIDATION_TEST_SUITE.md) | Maintainer-facing validation walkthrough for the guard stack. |
| [adr/0016-os-level-sandboxing.md](adr/0016-os-level-sandboxing.md) | Kernel sandboxing of bash children (Landlock / Seatbelt / restricted token). |
| [adr/0017-toolchain-cache-trust-and-scratch-state.md](adr/0017-toolchain-cache-trust-and-scratch-state.md) | Toolchain cache trust model: leaf grants, denied home-dir classes, scratch-GOPATH redirection. |

## Platform and Integration

| Doc | Covers |
| --- | --- |
| [platform/ACP.md](platform/ACP.md) | Running as an Agent Client Protocol server for editors. |
| [platform/openai-api.md](platform/openai-api.md) | The OpenAI-compatible HTTP API. |
| [platform/SCHEDULED_JOBS.md](platform/SCHEDULED_JOBS.md) | Unattended cron-scheduled agent runs. |
| [SDK.md](SDK.md) | Embedding the agent in Go programs via `pkg/client`. |
| [MERMAID.md](MERMAID.md) | The diagram rendering service. |

## Architecture

| Doc | Covers |
| --- | --- |
| [architecture/OVERVIEW.md](architecture/OVERVIEW.md) | Module layout and the transport/backend split. |
| [architecture/SESSIONS.md](architecture/SESSIONS.md) | The two-tier session model backed by SQLite. |
| [architecture/PROVIDER_ONBOARDING.md](architecture/PROVIDER_ONBOARDING.md) | The first-run provider onboarding flow and the custom (local) provider path. |

### Architecture Decision Records

Design records for how and why features were built, in
[`adr/`](adr/ADRs.md). Highlights:

- [ADR-0006](adr/0006-tree-sitter-structural-search.md) — tree-sitter structural search.
- [ADR-0014](adr/0014-workspace-search-dual-indexing.md) — FTS5 plus optional vector indexing.
- [ADR-0015](adr/0015-secret-protection-architecture.md) — the secret protection architecture.
- [ADR-0013](adr/0013-platform-extensibility-and-programmable-sdk.md) — platform extensibility and the SDK.
