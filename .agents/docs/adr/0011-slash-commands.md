# ADR: Slash Commands

## Status

Accepted

## Context

The `/` keybinding previously opened the command palette when the textarea was empty, preventing users from typing slash commands. The only existing slash command (`/goal`) was effectively hidden behind this behavior. `ctrl+p` already opened the command palette, so the `/` keybinding was redundant for that purpose.

The goal was to separate two distinct systems:
- **Command Menu** — browse and select simple actions via `ctrl+p`
- **Slash Commands** — type text commands directly in the chat input via `/command`

## Decision

Implement a slash command system as a separate feature from the command menu, with its own registry, execution model, and UI integration. The system lives in `internal/commands/slash_commands.go`.

### Implementation

**1. Removed `/` keybinding for command menu**

The `/` key no longer opens the command dialog on empty textarea. `ctrl+p` remains the keyboard shortcut for the command palette (binding name: `commands` in `phosphor.json`).

**2. Slash command registry**

Created `SlashCommand` type in `internal/commands/slash_commands.go`:

- Each command has: `Name`, `Description`, and `Arguments` (optional)
- Registered commands: `/goal`, `/menu`, `/stats`, `/quit`
- `GetSlashCommands()` and `GetSlashCommandNames()` provide access to the registry

**3. Slash command mode in UI**

Added `slashMode` bool field to the `UI` struct. The system operates in two states:
- **Slash mode**: triggered when user types `/` at the start of the textarea. Enter key processes the slash command instead of sending a message.
- **Normal mode**: standard message sending behavior.

The mode transition works as follows:
- Typing `/` at position 0 (empty input) enters slash mode
- Typing more characters after `/` shows slash command completions
- If input no longer starts with `/`, exit slash mode
- Completing a slash command via the completion popup preserves the `/` prefix

**4. Slash command completions**

The completions system was extended to support slash commands, similar to `@` for file mentions:
- `SlashCommandValue` type for completion items
- `SelectionMsg[SlashCommandValue]` for selection handling
- Completion popup shows available commands when user types `/`

**5. Command execution**

`handleSlashCommand()` processes slash commands:
- `/goal` — manages session goal (show status, set, clear)
- `/menu` — opens the command menu
- `/stats` — opens the usage statistics dialog
- `/quit` — quits the application
- Unrecognized commands are blocked with a warning message, preventing them from leaking into the agent prompt

**6. Message sending guard**

`sendMessage()` checks for `slashMode` and blocks sending slash commands to the agent. The command is routed through `handleSlashCommand()` instead.

## Alternatives Considered

- **Extend the command menu**: The command menu is dialog-based (browse/select). Slash commands are more natural for users who want to type quick commands. The two systems serve different interaction patterns.
- **Use a different prefix**: `/` is the most recognizable convention for chat commands. After removing the keybinding conflict, there was no reason to change it.

## Consequences

- Users can now type slash commands naturally in the chat input
- The command palette remains accessible via `ctrl+p`
- Slash commands are discoverable through `/` autocomplete
- Unrecognized commands produce a warning instead of silently being sent to the agent
- The system is extensible — new commands can be added to the registry in `internal/commands/slash_commands.go`
- The `slashMode` state is managed in the UI layer (not the agent), keeping the two systems loosely coupled
