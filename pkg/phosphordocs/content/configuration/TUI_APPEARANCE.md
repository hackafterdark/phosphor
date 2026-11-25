# TUI Appearance

The `options.tui` section controls the terminal UI's look and feel.

```json
{
  "options": {
    "tui": {
      "theme": "pantera",
      "compact_mode": false,
      "diff_mode": "unified",
      "transparent": false,
      "show_thinking": true,
      "history_limit": 100,
      "history_batch_size": 50,
      "keybindings": {
        "models": "ctrl+m,ctrl+l"
      }
    }
  }
}
```

## Fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `theme` | string | `""` | Color theme — see below |
| `compact_mode` | bool | `false` | `true` reduces visual spacing between messages |
| `diff_mode` | string | `"unified"` | Diff display style: `"unified"` (single column) or `"split"` (side-by-side) |
| `transparent` | bool | `false` | `true` renders the background transparent |
| `show_thinking` | bool | `true` | `false` hides the agent's thinking output, leaving tool calls and the final response |
| `history_limit` | int | `100` | Maximum chat history messages kept in memory |
| `history_batch_size` | int | `50` | Messages loaded per batch when scrolling back |
| `keybindings` | object | `{}` | Custom key bindings (see [KEYBINDINGS.md](../KEYBINDINGS.md)) |

### `theme` values

| Value | Result |
|-------|--------|
| `""` (empty) | Provider-based default theme |
| `"pantera"` / `"charmtone"` | Charmtone Pantera |
| `"obsidiana"` / `"hyper"` | Hyperphosphor Obsidiana |
| a path/name | A custom theme file; if it cannot be found or parsed, falls back to the provider default |

For custom theme files, see [THEMES.md](../THEMES.md).

## Related Docs

- [THEMES.md](../THEMES.md) — Available themes and custom theme creation
- [KEYBINDINGS.md](../KEYBINDINGS.md) — Remapping keys
- [UI_LAYOUT_CONFIG.md](../UI_LAYOUT_CONFIG.md) — Landing/sidebar layout
- [LOGO_CUSTOMIZATION.md](../LOGO_CUSTOMIZATION.md) — Custom logo

## Notification Style

Control how notifications work:

```json
{
  "options": {
    "notification_style": "auto"
  }
}
```

| Value | Behavior |
|-------|----------|
| ""auto"" | Detect automatically: "native" for local sessions, "osc" for SSH (with OSC 99/777 detection) |
| ""native"" | OS-native desktop notification |
| ""osc"" | OSC escape-sequence notification |
| ""bell"" | Terminal bell |
| ""disabled"" | No notifications |
