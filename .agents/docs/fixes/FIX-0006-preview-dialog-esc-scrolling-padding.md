# FIX-0006: Attachment Preview Dialog Lacks ESC Key, Scrolling, and Proper Padding

## Status

**Applied** — `internal/ui/dialog/preview.go` rewritten to use `viewport.Model` for scrollable content, `help.Model` for proper key bindings display, and `Dialog.ContentPanel` style for consistent padding.

## Problem

The attachment preview dialog (opened via `ctrl+alt+v` when pasting content exceeding 10 lines or 1000 columns) had three critical usability issues:

1. **ESC key did not close the dialog** — `HandleMsg` was a no-op (`_ = msg; return nil`), so pressing `esc` or `alt+esc` had no effect. The session was left in a stuck/unusable state with the dialog blocking all input.

2. **No scrolling support** — Content was truncated to the dialog's fixed dimensions with an ellipsis (`…`). Users could not navigate to see content that exceeded the visible area, unlike other preview dialogs (diff views, permission dialogs) that use scrollable viewports.

3. **Missing padding and inconsistent styling** — Content ran directly into the dialog edges. The old implementation used `RenderContext` with raw text, while other dialogs use `Dialog.ContentPanel` (padding `1,2`) and `help.Model` with `ShortHelp()`/`FullHelp()` for the bottom help bar.

## Why It Occurred

The preview dialog was the only dialog in `internal/ui/dialog/` that did not implement the full dialog contract:

- `HandleMsg` returned `nil` without processing any messages, so the `Overlay.Update` loop could never return `ActionClose{}` for this dialog.
- The `Draw` method rendered content as a plain string via `RenderContext.AddPart()`, which does not support scrolling.
- The help bar was set manually via `rc.Help = t.Dialog.HelpView.Render("esc: close")` instead of implementing the `help.KeyMap` interface that other dialogs use.

Other dialogs (permissions, reasoning, notifications, quit) all follow the same pattern:
- `HandleMsg` processes `tea.KeyPressMsg` and returns `ActionClose{}` on ESC
- Content is rendered through a `viewport.Model` for scrollable regions
- `ShortHelp()` / `FullHelp()` methods provide the help bar bindings
- Content uses `Dialog.ContentPanel` style for consistent padding

## When Users Encounter It

Users hit this when:

- Pasting long text (more than 10 lines or 1000 columns) into the input
- The paste triggers the attachment preview dialog automatically
- The user presses `esc` expecting to dismiss the dialog, but nothing happens
- The user cannot see all the pasted content because it is truncated
- The user cannot scroll to read the full content

## Fix

### `HandleMsg` — ESC key support (`internal/ui/dialog/preview.go`)

`HandleMsg` now processes `tea.KeyPressMsg` and returns `ActionClose{}` when `esc` or `alt+esc` is pressed, matching the `CloseKey` binding used by all other dialogs:

```go
case tea.KeyPressMsg:
    switch {
    case key.Matches(msg, p.keyMap.Close):
        return ActionClose{}
    case key.Matches(msg, p.keyMap.ScrollDown):
        p.viewport, _ = p.viewport.Update(msg)
        p.viewportDirty = true
    // ...
    }
```

### Scrollable content with viewport (`internal/ui/dialog/preview.go`)

Added a `viewport.Model` field and configured it with scroll keybindings matching `permissions.go`:

| Key | Action |
|---|---|
| `shift+↑` / `K` | Scroll up |
| `shift+↓` / `J` | Scroll down |
| `shift+←` / `H` | Scroll left |
| `shift+→` / `L` | Scroll right |
| `shift+←↓↑→` | Generic scroll (shown in help) |

The viewport is resized dynamically based on available space, and content is re-rendered into it when dimensions change. A scrollbar is shown when content exceeds the viewport height.

### Proper padding and styling (`internal/ui/dialog/preview.go`)

- Content is now rendered through `Dialog.ContentPanel.Width(width).Render(content)`, which applies padding `1,2` (matching `permissions.go`'s `renderContentPanel`).
- The dialog frame uses `Dialog.View.Width(width).Padding(0, 1)` for consistent border padding.
- The title bar uses `common.DialogTitle` with gradient colors, matching other dialogs.

### Help bar via `help.KeyMap` interface (`internal/ui/dialog/preview.go`)

Implemented `ShortHelp()` and `FullHelp()` methods so the dialog integrates with the `help.Model` system:

```go
func (p *Preview) ShortHelp() []key.Binding {
    bindings := []key.Binding{p.keyMap.Close}
    if p.canScroll() {
        bindings = append(bindings, p.keyMap.Scroll)
    }
    return bindings
}
```

The help bar shows `esc: exit` and conditionally `shift+←↓↑→: scroll` (only when content is scrollable).

## Files Changed

- **Modified:** `internal/ui/dialog/preview.go` — Complete rewrite to match dialog patterns used by `permissions.go`, `reasoning.go`, `notifications.go`.

## Imports Added

- `charm.land/bubbles/v2/help` — Help bar model
- `charm.land/bubbles/v2/viewport` — Scrollable viewport
- `charm.land/lipgloss/v2` — Layout helpers (for `lipgloss.Height`, `lipgloss.JoinVertical/Horizontal`)

## Imports Removed

- `github.com/charmbracelet/x/ansi` — No longer needed (no manual truncation)
- `github.com/charmbracelet/ultraviolet` — No longer used directly (via `uv` alias)

## Where It Could Grow (If Needed)

### Mouse wheel scrolling

The viewport already supports mouse wheel messages through the `default` case in `HandleMsg` (`p.viewport, _ = p.viewport.Update(msg)`), but mouse wheel handling could be made explicit for clarity and to add horizontal scroll support.

### Page up / Page down

Page navigation keys are disabled on the viewport to avoid conflicts with dialog shortcuts. If the preview content is very long, enabling `PageUp`/`PageDown` bindings would improve navigation.

### Word wrap toggle

For code content, a toggle between word-wrapped and no-wrap mode would let users read long lines without horizontal scrolling. This could be added as a key binding (e.g., `w` to toggle).

### File type-aware rendering

For code files, syntax highlighting could be applied before rendering into the viewport. For text files, plain rendering is sufficient. The `message.Attachment.MimeType` field is available for this distinction.

### Copy-to-clipboard action

A key binding (e.g., `ctrl+c`) to copy the full content to the clipboard would be useful when users want to extract content from the preview without returning to their original source.
