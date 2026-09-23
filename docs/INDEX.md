# Documentation Index

A complete map of everything under `docs/`. In-app, the same corpus is
searchable through the `phosphor_docs` tool or by viewing any
`phosphor://docs/<path>` file.

## Getting Started

| Doc | Covers |
| --- | --- |
| [CORE_PHILOSOPHY.md](CORE_PHILOSOPHY.md) | Identity and core pillars of the project. |
| [KEYBINDINGS.md](KEYBINDINGS.md) | Default TUI key bindings and how to configure custom ones. |

## Configuration

| Doc | Covers |
| --- | --- |
| [MODELS_AND_PROVIDERS_CONFIG.md](MODELS_AND_PROVIDERS_CONFIG.md) | Configuring providers and models in `phosphor.json`. |
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
| [security/CONFIGURATION.md](security/CONFIGURATION.md) | Every security control and how to configure it. |
| [security/WORKSPACE_HARDENING.md](security/WORKSPACE_HARDENING.md) | Filesystem confinement and the workspace threat model. |
| [security/ENVIRONMENT_HARDENING.md](security/ENVIRONMENT_HARDENING.md) | Environment variable filtering in shell commands. |
| [security/NETWORK_EGRESS_HARDENING.md](security/NETWORK_EGRESS_HARDENING.md) | The network egress firewall for fetch/search. |
| [security/SECRETS_PROTECTION.md](security/SECRETS_PROTECTION.md) | Defense-in-depth against secrets reaching the model. |

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

### Architecture Decision Records

Design records for how and why features were built, in
[`adr/`](adr/ADRs.md). Highlights:

- [ADR-0006](adr/0006-tree-sitter-structural-search.md) — tree-sitter structural search.
- [ADR-0014](adr/0014-workspace-search-dual-indexing.md) — FTS5 plus optional vector indexing.
- [ADR-0015](adr/0015-secret-protection-architecture.md) — the secret protection architecture.
- [ADR-0013](adr/0013-platform-extensibility-and-programmable-sdk.md) — platform extensibility and the SDK.
