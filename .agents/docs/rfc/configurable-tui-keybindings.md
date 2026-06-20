# RFC: Configurable TUI Key Bindings

## Status

Draft

## Problem

Phosphor's TUI key bindings are hardcoded in `internal/ui/model/keys.go`. Users whose systems intercept certain key combinations (e.g., `ctrl+alt+v` for clipboard/paste on some Linux desktop environments, `ctrl+v` on Windows terminals, `ctrl+alt+<key>` on macOS) cannot use those bindings because the OS or terminal swallows the event before it reaches Phosphor.

The paste preview feature (`ctrl+alt+v`) is a concrete blocker for one user. Other commonly conflicting bindings include `ctrl+v` (paste image), `ctrl+space` (toggle pills), and `ctrl+alt+<letter>` combos on certain platforms.

There is no mechanism to override key bindings through `phosphor.json`.

## Goals

1. Allow every TUI key binding to be configured via `phosphor.json` under `options.tui.keybindings`.
2. Preserve all existing defaults as fallbacks — if a binding is not specified in config, the current hardcoded default is used.
3. Support the same key format that `charm.land/bubbles/v2/key` already uses (`"ctrl+alt+v"`, `"ctrl+v"`, `"enter"`, etc.).
4. Support multiple keys per binding (e.g., `"ctrl+n,ctrl+j"`), matching the existing `key.WithKeys` semantics.
5. Keep the implementation minimal and non-breaking — no changes to existing config for existing users.
6. Provide clear error messages if an invalid key string is provided.

## Non-Goals

- Removable/disabled bindings (a binding can only be changed, not unset — use `keybindings` map with an empty string later if needed).
- Per-component keymaps with different scopes (all bindings live in one flat map).
- Runtime hot-reload of key bindings (config reload would require a full UI restart).
- Custom key bindings for non-TUI components (LSP, agent tools, etc.).

## Proposed Design

### Config Schema

Add a `KeyBindings` field to `TUIOptions` in `internal/config/config.go`:

```go
type TUIOptions struct {
    CompactMode bool   `json:"compact_mode,omitempty" jsonschema:"description=Enable compact mode for the TUI interface,default=false"`
    DiffMode    string `json:"diff_mode,omitempty" jsonschema:"description=Diff mode for the TUI interface,enum=unified,enum=split"`
    Completions Completions `json:"completions,omitzero" jsonschema:"description=Completions UI options"`
    Transparent *bool       `json:"transparent,omitempty" jsonschema:"description=Enable transparent background for the TUI interface,default=false"`
    KeyBindings map[string]string `json:"keybindings,omitempty" jsonschema:"description=Custom TUI key bindings. Keys are binding names, values are key combinations (e.g., \"ctrl+alt+v\"). Unspecified bindings use defaults."`
}
```

### Binding Names

A flat map of binding names to key strings. The names mirror the internal field names for discoverability:


| Binding Name             | Default               | Category   |
| ------------------------ | --------------------- | ---------- |
| `quit`                   | `ctrl+c`              | Global     |
| `help`                   | `ctrl+g`              | Global     |
| `commands`               | `ctrl+p`              | Global     |
| `models`                 | `ctrl+m,ctrl+l`       | Global     |
| `suspend`                | `ctrl+z`              | Global     |
| `sessions`               | `ctrl+s`              | Global     |
| `tab`                    | `tab`                 | Global     |
| `toggle_yolo`            | `ctrl+y`              | Global     |
| `send_message`           | `enter`               | Editor     |
| `open_editor`            | `ctrl+o`              | Editor     |
| `newline`                | `shift+enter,ctrl+j`  | Editor     |
| `add_image`              | `ctrl+f`              | Editor     |
| `paste_image`            | `ctrl+v`              | Editor     |
| `mention_file`           | `@`                   | Editor     |
| `add_file`               | `/`                   | Editor     |
| `attachment_delete_mode` | `ctrl+r`              | Editor     |
| `attachment_escape`      | `esc,alt+esc`         | Editor     |
| `delete_all_attachments` | `r`                   | Editor     |
| `preview_attachment`     | `ctrl+alt+v`          | Editor     |
| `history_prev`           | `up`                  | Editor     |
| `history_next`           | `down`                | Editor     |
| `clear_prompt`           | `ctrl+x`              | Editor     |
| `new_session`            | `ctrl+n`              | Chat       |
| `add_attachment`         | `ctrl+f`              | Chat       |
| `cancel`                 | `esc,alt+esc`         | Chat       |
| `details`                | `ctrl+d`              | Chat       |
| `toggle_pills`           | `ctrl+t,ctrl+space`   | Chat       |
| `pill_left`              | `left`                | Chat       |
| `pill_right`             | `right`               | Chat       |
| `scroll_down`            | `down,ctrl+j,j`       | Chat       |
| `scroll_up`              | `up,ctrl+k,k`         | Chat       |
| `scroll`                 | `up,down`             | Chat       |
| `scroll_one_item_up`     | `shift+up,K`          | Chat       |
| `scroll_one_item_down`   | `shift+down,J`        | Chat       |
| `scroll_one_item`        | `shift+up,shift+down` | Chat       |
| `half_page_down`         | `d`                   | Chat       |
| `page_down`              | `pgdown, ,f`          | Chat       |
| `page_up`                | `pgup,b`              | Chat       |
| `half_page_up`           | `u`                   | Chat       |
| `home`                   | `g,home`              | Chat       |
| `end`                    | `G,end`               | Chat       |
| `copy`                   | `c,y,C,Y`             | Chat       |
| `clear_highlight`        | `esc,alt+esc`         | Chat       |
| `expand`                 | `space`               | Chat       |
| `initialize_yes`         | `y,Y`                 | Initialize |
| `initialize_no`          | `n,N,esc,alt+esc`     | Initialize |
| `initialize_switch`      | `left,right,tab`      | Initialize |
| `initialize_enter`       | `enter`               | Initialize |


### Implementation

#### 1. Key binding parser

Add a helper function that converts a config map into `key.Binding` values. This lives in `internal/ui/model/keys.go`:

```go
// parseKeyBinding parses a comma-separated key string (e.g., "ctrl+alt+v")
// and returns a *key.Binding, or nil if the string is empty.
func parseKeyBinding(keys string) *key.Binding {
    if keys == "" {
        return nil
    }
    b := key.NewBinding(key.WithKeys(strings.Split(keys, ",")...))
    return &b
}
```

#### 2. Config-aware keymap builder

Add a function that builds a `KeyMap` from config, falling back to defaults:

```go
// KeyMapFromConfig builds a KeyMap using config overrides where provided,
// falling back to DefaultKeyMap() for any binding not specified.
func KeyMapFromConfig(keybindings map[string]string) KeyMap {
    km := DefaultKeyMap()

    // Apply overrides
    if v, ok := keybindings["quit"]; ok {
        if b := parseKeyBinding(v); b != nil {
            km.Quit = *b
        }
    }
    // ... repeat for all bindings
    return km
}
```

#### 3. Wire into UI construction

In `internal/ui/model/ui.go`, replace the hardcoded call:

```go
// Before:
keyMap := DefaultKeyMap()

// After:
keyMap := DefaultKeyMap()
if com.Config().Options != nil && com.Config().Options.TUI != nil && com.Config().Options.TUI.KeyBindings != nil {
    keyMap = KeyMapFromConfig(com.Config().Options.TUI.KeyBindings)
}
```

#### 4. Completions keymap

The completions component also has a hardcoded keymap (`internal/ui/completions/keys.go`). Add the same `KeyBindingsFromConfig` pattern there if the user wants to customize completions keys. This can be a follow-up.

### Key Format

Key strings follow the format produced by the underlying `ultraviolet` library's `Key.Keystroke()` method. Modifiers are always in a fixed order: `ctrl+alt+shift+meta+hyper+super+key`.

Supported modifiers and their platform mappings:


| String  | macOS       | Linux       | Windows |
| ------- | ----------- | ----------- | ------- |
| `ctrl`  | Control (⌃) | Control     | Control |
| `alt`   | Option (⌥)  | Alt / AltGr | Alt     |
| `super` | Command (⌘) | Super / Win | Win     |
| `shift` | Shift (⇧)   | Shift       | Shift   |
| `meta`  | Meta        | Meta        | Meta    |
| `hyper` | Hyper       | Hyper       | Hyper   |


Supported special keys include: `enter`, `tab`, `esc`, `backspace`, `space`, `up`, `down`, `left`, `right`, `pgup`, `pgdown`, `home`, `end`, `f1`–`f63`, `insert`, `delete`, and any printable character (`a`, `1`, `@`, etc.).

Multiple keys per binding are comma-separated: `"ctrl+n,ctrl+j"`.

### Platform Considerations

Phosphor currently has **no platform-specific keybinding defaults** — all platforms use the same bindings. The config system is designed to allow platform-specific defaults in the future without breaking changes:

```go
// Future: DefaultKeyMap() could vary by runtime.GOOS
func DefaultKeyMap() KeyMap {
    if runtime.GOOS == "darwin" {
        // macOS defaults (e.g., cmd for super)
    }
    return km
}
```

For now, users on any platform can use `super` to refer to the platform's command/super key (⌘ on macOS, Win on Windows, Super on Linux).

### Example Config

```json
{
  "options": {
    "tui": {
      "keybindings": {
        "preview_attachment": "ctrl+shift+v",
        "paste_image": "ctrl+shift+v",
        "quit": "ctrl+q"
      }
    }
  }
}
```

## Validation

- **Empty string**: Treated as "use default" — no binding is created for that key, so the default is preserved.
- **Invalid key string**: Silently falls back to the default. No validation is performed at config load time to avoid maintaining a duplicate of ultraviolet's ~150-entry key name map. Invalid keys simply don't match anything at runtime.
- **Duplicate keys**: If two bindings map to the same key combo, bubbletea handles it (last registration wins). We don't deduplicate — that's bubbletea's job.
- **Key conflicts between user and system**: Out of scope. The user is responsible for choosing keys that work on their system.

## Testing

1. **Unit test**: `KeyMapFromConfig` with a partial map — verify defaults are preserved for unspecified bindings.
2. **Unit test**: `KeyMapFromConfig` with an empty map — verify it equals `DefaultKeyMap()`.
3. **Unit test**: `parseKeyBinding` with empty string, single key, multi-key, invalid key.
4. **Integration test**: Start Phosphor with a config that changes `preview_attachment` to a non-conflicting key. Verify the new key triggers the preview dialog.
5. **Config loading test**: Verify that `keybindings` in `phosphor.json` are correctly parsed into the `TUIOptions` struct and merged with workspace config.

## Migration

Zero — this is a purely additive change. Existing `phosphor.json` files are unaffected. The `keybindings` field is `omitempty` and `map[string]string` defaults to `nil`.

## Files to Change


| File                              | Change                                                     |
| --------------------------------- | ---------------------------------------------------------- |
| `internal/config/config.go`       | Add `KeyBindings` field to `TUIOptions`                    |
| `internal/ui/model/keys.go`       | Add `parseKeyBinding()` and `KeyMapFromConfig()` functions |
| `internal/ui/model/ui.go`         | Wire `KeyMapFromConfig` into `New()`                       |
| `internal/ui/completions/keys.go` | (Optional) Add config-aware builder for completions keys   |
| `internal/ui/model/keys_test.go`  | New test file for keymap parsing                           |


## Open Questions

1. **Should we support disabling bindings?** Currently no — a binding must have at least one key. We could add support for `"binding_name": ""` meaning "disable this binding", but that's a nice-to-have for later.
2. **Should we validate key strings at config load time?** No. The `ultraviolet` library's `keyMatchString` parser uses an unexported ~150-entry map of special key names. Duplicating it would drift from upstream every time new keys are added. Invalid key strings (e.g., `"ctrl+xyz"`) silently produce no-op bindings — no crash, no error. The user experience is just a non-functional key. Instead:
  - Empty strings are treated as "use default" (already handled by `parseKeyBinding`).
  - Invalid key strings silently fall back to the default binding.
  - Document this behavior in the schema description.
  - If a user reports "my keybinding doesn't work," they can check the key string format against the supported list.
  - Revisit only if `ultraviolet` exports a validation function in the future.
3. **Should the completions keymap also be configurable?** No. The completions component has its own hardcoded keymap. Leave this alone for now, do not allow it to be configurable.
4. **Should we expose a "list all bindings" command?** DONE - already implemented. Users need to know what binding names exist. We could add a `phosphor keybindings` CLI command or show them in the help dialog. This is a UX polish item.
5. **What about the `key.WithHelp` text?** Currently each binding has a hardcoded help string (e.g., `"ctrl+alt+v"` in the help column). If we change the key, the help text should update automatically. The `key.Binding` struct stores the keys, so we can derive the help display from the actual keys. This may require a small change in how help is rendered — currently the help text is set at binding creation time. We should verify that the help column shows the *actual* keys, not a static string.
6. **Should we ship platform-specific default keymaps?** macOS users traditionally expect `cmd` (super) for common actions like copy/paste/quit. Linux users may prefer `super` or `alt`. Windows users may expect `ctrl` or `win`. We could ship different `DefaultKeyMap()` implementations per platform, while still allowing full override via config. This is a larger UX change and could be done in a follow-up PR.

## Future Potential

- **Import/export**: Allow users to copy keybindings from another editor (VS Code, Vim, etc.).
- **Keybinding conflict detection**: Warn if two bindings use the same key combo.
- **Runtime reload**: Apply keybinding changes without restarting the TUI.
- **Theme-like presets**: Ship a few pre-configured keybinding sets (Emacs, Vim, VS Code).

