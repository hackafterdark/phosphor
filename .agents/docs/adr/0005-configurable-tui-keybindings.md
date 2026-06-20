# ADR: Configurable TUI Key Bindings

## Status

Accepted

## Context

Phosphor's TUI key bindings were hardcoded in `internal/ui/model/keys.go`. Users whose systems intercept certain key combinations (e.g., `ctrl+alt+v` for clipboard/paste on Linux desktop environments, `ctrl+v` on Windows terminals, `ctrl+alt+<key>` on macOS) cannot use those bindings because the OS or terminal swallows the event before it reaches Phosphor. The paste preview feature (`ctrl+alt+v`) is a concrete blocker for one user. Other commonly conflicting bindings include `ctrl+v` (paste image), `ctrl+space` (toggle pills), and `ctrl+alt+<letter>` combos on certain platforms.

## Decision

We allow every TUI key binding to be configured via `phosphor.json` under `options.tui.keybindings`. The config is a flat map of binding names to key strings, using the same format that `charm.land/bubbles/v2/key` already uses. If a binding is not specified in config, the current hardcoded default is used.

### Implementation

- Added `KeyBindings map[string]string` to `TUIOptions` in `internal/config/config.go`.
- Added `parseKeyBinding()` in `internal/ui/model/keys.go` to parse comma-separated key strings into `key.Binding` values.
- Added `KeyMapFromConfig()` in `internal/ui/model/keys.go` that starts with `DefaultKeyMap()` and applies overrides from the config map.
- Wired `KeyMapFromConfig` into `internal/ui/model/ui.go`'s `New()` function, reading from the config service.
- Added `internal/ui/model/keys_test.go` with tests covering all 47 binding names, empty/nil configs, partial overrides, and multi-key bindings.

### Why This Approach

1. **Backward compatible**: The `keybindings` field is `omitempty` and defaults to `nil`. Existing users see zero change.
2. **Defaults preserved**: `KeyMapFromConfig()` starts with `DefaultKeyMap()` and only replaces bindings that are explicitly set in config.
3. **Simple config format**: A flat `map[string]string` is the simplest possible schema — no nested objects, no arrays, no complex validation.
4. **Silent fallback**: Invalid key strings are silently ignored and the default is used. This avoids maintaining a duplicate of the upstream library's ~150-entry key name map.
5. **Multi-key support**: Comma-separated values like `"ctrl+n,ctrl+j"` match the existing `key.WithKeys` semantics.

### Alternatives Considered

- **Per-platform default keymaps**: Ship different defaults per OS (e.g., `cmd` on macOS). This was deferred as a larger UX change that would require platform-specific code in `DefaultKeyMap()`.
- **Disabling bindings**: Allow `"binding_name": ""` to remove a binding. This was deferred — a binding must have at least one key. An empty string is treated as "use default."
- **Runtime hot-reload**: Apply keybinding changes without restarting the TUI. This was deferred — it would require a full UI restart to take effect.
- **Keybinding conflict detection**: Warn if two bindings use the same key combo. This was deferred — bubbletea handles conflicts at runtime (last registration wins).
- **Keybinding presets**: Ship pre-configured sets (Emacs, Vim, VS Code). This was deferred — it would require a more complex config schema.
- **Import/export from other editors**: Allow users to copy keybindings from VS Code, Vim, etc. This was deferred — it would require a mapping layer between editor keymaps and Phosphor's format.

### Consequences

- Users can now customize any of the 47 TUI key bindings.
- The `phosphor bindings` CLI command already lists all available bindings, giving users discoverability.
- Config changes require a restart of Phosphor to take effect.
- Invalid key strings silently fall back to defaults — the user experience is a non-functional key with no error message.
- No validation is performed at config load time, so typos in binding names are silently ignored.

### Files Changed

| File | Change |
|---|---|
| `internal/config/config.go` | Added `KeyBindings` field to `TUIOptions` |
| `internal/ui/model/keys.go` | Added `parseKeyBinding()` and `KeyMapFromConfig()` |
| `internal/ui/model/ui.go` | Wired `KeyMapFromConfig` into `New()` |
| `internal/ui/model/keys_test.go` | New test file for keymap parsing |
| `KEYBINDINGS.md` | User-facing documentation |

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
