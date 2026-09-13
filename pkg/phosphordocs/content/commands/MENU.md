# The `/menu` Slash Command

The `/menu` slash command opens the command menu dialog, which provides quick access to commonly used dialogs and actions.

---

## Usage

```
/menu
```

---

## Behavior

- Opens the commands dialog, which includes buttons for sessions, models, cron jobs, reasoning, notifications, and other TUI features.
- If the dialog is already open, it brings it to the front.
- Does not require an active session, but session-dependent options in the menu will be disabled when no session is active.