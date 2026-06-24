---
name: slash-commands
description: Developer guide for adding and implementing local slash commands in the Phosphor TUI.
---

# Adding Slash Commands to Phosphor

Slash commands are typed in the prompt starting with a `/` (e.g., `/menu`, `/stats`, `/goal`). They are executed locally in the TUI layer and are strictly prevented from leaking into the LLM conversation history.

---

## Architecture Overview

Phosphor uses a split architecture for slash commands:
1. **Registry (`internal/commands/slash_commands.go`):** A data-only package containing the list of available slash commands and their descriptions. This package is imported by the completions engine.
2. **Handlers (`internal/ui/model/ui.go`):** The TUI model maintains a dynamic map (`m.slashHandlers`) linking command names to handler methods that return a `tea.Cmd`.

### Hybrid / Agent-Facing Commands (e.g., `/learn`)

While most slash commands are purely local (UI-only) and never reach the LLM, some commands are **hybrid**.

For instance, `/learn <source>` is typed in the TUI, but instead of being blocked, the handler **translates it** into a highly structured system-guided instruction prompt and calls `m.sendMessage(prompt)` to submit it to the LLM agent. 

Because the generated prompt is in plain English and does not start with a `/`, it naturally bypasses the backend slash-prevention failsafes and leverages the agent's existing file/web tools to execute the command.

---

## Step-by-Step Guide to Adding a Slash Command

To add a new slash command `/foo`:

### 1. Register the Command
Open `internal/commands/slash_commands.go` and add a new entry to the `SlashCommands` slice:
```go
{
    Name:        "foo",
    Description: "Open the foo menu or trigger action",
}
```

### 2. Implement the Handler Method
Open `internal/ui/model/ui.go` and implement the handler method on the `UI` struct. The method must take `args []string` and return `tea.Cmd`:
```go
// handleFooSlashCommand handles the "/foo" slash command.
func (m *UI) handleFooSlashCommand(args []string) tea.Cmd {
    // Implement command logic here.
    // For example, to open a dialog:
    // m.dialog.OpenDialog(dialog.NewFoo(m.com))
    return func() tea.Msg { return nil }
}
```

### 3. Wire the Handler
In `internal/ui/model/ui.go`, locate the `registerSlashCommands` method and map your command name to the handler:
```go
func (m *UI) registerSlashCommands() {
    m.slashHandlers = map[string]slashCommandHandler{
        "goal":  m.handleGoalSlashCommand,
        "menu":  m.handleMenuSlashCommand,
        "stats": m.handleStatsSlashCommand,
        "quit":  m.handleQuitSlashCommand,
        "foo":   m.handleFooSlashCommand, // <-- Register your new handler here
    }
}
```

### 4. Write a Unit Test
Open `internal/ui/model/ui_test.go` and add a unit test to verify that the command executes properly, sanitizes the TUI input states, and triggers the expected side effect (e.g., opening a dialog):
```go
func TestUI_HandleSlashCommand_FooOpensDialog(t *testing.T) {
    t.Parallel()

    tw := &testWorkspace{}
    st := uistyles.CharmtonePantera()
    ui := &UI{
        com: &common.Common{
            Workspace: tw,
            Styles:    &st,
		},
    }
    ui.registerSlashCommands()

    ui.dialog = dialog.NewOverlay()
    ui.slashMode = true
    ui.completionsOpen = true
    ui.completions = completions.New(lipgloss.NewStyle(), lipgloss.NewStyle(), lipgloss.NewStyle())

    // Call handleSlashCommand with /foo.
    cmd := ui.handleSlashCommand("/foo")
    require.NotNil(t, cmd)

    // Verify that the dialog is open and states are cleaned up.
    require.True(t, ui.dialog.ContainsDialog(dialog.FooID))
    require.False(t, ui.slashMode)
    require.False(t, ui.completionsOpen)
}
```

---

## Key Best Practices

1. **State Sanitization:** The main dispatcher automatically sets `m.slashMode = false` and calls `m.closeCompletions()` before executing any handler. You do not need to do this manually in your handler.
2. **Session Requirements:** If a command requires an active session, check `m.hasSession()` first and return `util.ReportWarn` if one is not active.
3. **No LLM Leakage:** Never send slash commands to the LLM agent or add them to the message history.
