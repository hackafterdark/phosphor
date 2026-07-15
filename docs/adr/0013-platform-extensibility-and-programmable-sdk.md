# 13. Platform Extensibility and Programmable SDK

- **Status:** Accepted
- **Date:** 2026-07-14
- **Authors:** Phosphor Team
- **Superseded By:** —

## Context
Phosphor was originally developed with a highly integrated Terminal User Interface (TUI) and in-process agent execution logic inside the `internal/` package. While this was effective for building a standalone TUI utility, it restricted third-party applications (such as IDE integrations, HTTP API services, and automated workflow scripts) from reusing the agent, coordinator, tools, session management, or provider configurations.

To position the application as an extensible, transport-agnostic agent platform, we needed:
1. A way to connect Phosphor to multiple clients and IDEs (e.g. via the Agent Client Protocol or an OpenAI-compatible endpoint).
2. A way for developers to embed the Phosphor engine directly inside their own custom Go programs.

## Decision
We decided to restructure the codebase to separate the core agent runtime from the frontends and transport protocols:

1. **Service Integration Layer**: We defined `Service`, `AgentService`, and `Registry` interfaces inside `internal/platform` to manage the lifecycles of diverse transport platforms. We wrapped and registered:
   - **ACP**: An Agent Client Protocol (stdio JSON-RPC) adapter for IDEs like Zed.
   - **OpenAI API**: An HTTP compatibility layer exposing `/v1/chat/completions`.
   - **Cron**: A background scheduling service for recurring agent jobs.
   - **TUI**: The interactive Bubble Tea user interface.
2. **Core Extraction to `pkg/`**: We moved all TUI-independent modules from `internal/` to `pkg/` (including `agent`, `config`, `db`, `session`, `skills`, `permission`, `message`, `hooks`, `shell`, `lsp`, and `otel`).
3. **Public Client SDK**: We created the programmatic wrapper in `pkg/client` exposing `NewSession`, `SendMessage`, and `Subscribe` interfaces.

## Rationale / Why This Approach
- **Decoupled Architecture**: Standardizing on the `Service` registry allows the agent to run headless, in server mode, or in interactive TUI mode using a uniform lifecycle.
- **Dependency Isolation**: Moving the core agent package to `pkg/` lets developers import the Phosphor agent into their Go projects without pulling in TUI dependencies (like Bubble Tea) or forcing them to adopt Phosphor's HTTP router (Echo) or database migration structures.
- **Facade Pattern**: Exposing `pkg/client.SessionHandle` keeps the SDK API simple, hiding the complex configuration, database pooling, and skill discovery details from the user.

## Consequences
- Clean separation between frontend components (in `internal/`) and reusable library code (in `pkg/`).
- External developers can import `github.com/hackafterdark/phosphor/pkg/client` to execute agent turns and subscribe to tool execution hooks.
- Extensibility to support future transport adapters (such as chat bots, custom dashboards) without having to modify the core agent logic.
