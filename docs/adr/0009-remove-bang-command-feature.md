# ADR: Remove Bang Command Feature

## Status

Accepted

## Context

Phosphor previously included a "bang command" feature (from Crush, the project Phosphor was forked from) that allowed users to type `!` into the input prompt to enter a special shell command mode. When activated, the prompt would change to indicate shell mode, and pressing Enter would execute a shell command directly in the workspace directory.

The bang command had the following characteristics:
- The command output was displayed in the chat UI but was **not** stored as a message for the LLM to see.
- The agent could not trigger or invoke bang commands on its own — only users could activate them.
- Shell commands ran as a separate execution path (not via the agent's `bash` tool).

Despite the output being invisible to the model, the feature introduced several challenges that outweighed its value in an agent CLI tool.

## Decision

Remove the bang command feature entirely from the codebase.

### Why This Approach

1. **Safer for users**: Running system commands from a separate terminal session is safer than running them inside an agent CLI. A dedicated terminal provides full control over command history, output scrolling, and process management.

2. **Terminal-inside-terminal complexity**: A terminal rendered inside another terminal is inherently complex. Interactive commands (e.g., `top`, `less`, `more`, `vim`) require PTY handling and proper signal forwarding. Providing complete support for interactive commands would be difficult and would require significant ongoing maintenance.

3. **Lean application**: Removing the feature eliminates a non-trivial code surface area across the UI, server, backend, workspace, message, and proto layers. This reduces maintenance burden and potential for bugs.

4. **Clearer separation of concerns**: In an agent CLI, the primary interaction model is through LLM tool calls. Direct shell execution was a secondary, edge-case feature that didn't align with the core use case of agent-driven workflows. Understanding that some popular tools like Warp serve as both a terminal and an AI agent, that functional blend is not the goal of Phosphor.

### Alternatives Considered

- **Keeping the feature as-is**: The feature was functional for simple, non-interactive commands. However, the terminal-inside-terminal problem space is hard to get right for all cases.

- **Making the feature opt-in via config**: An opt-in toggle would have preserved the feature for power users but added ongoing maintenance cost for a feature that most users don't need.

### Consequences

- Users who would prefer a `!` shortcut should open a separate terminal session or window for direct shell command execution.
- The agent's `bash` tool remains fully functional — agents can still run shell commands through their normal tool call workflow.
- The feature has been removed across all layers: UI (bang mode, shell result display), server (shell endpoint, backend handler), workspace (shell command execution), and message/protocol layers (shell command types, serialization).
