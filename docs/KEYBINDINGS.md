# Configurable TUI Key Bindings

Phosphor allows you to customize all TUI key bindings through `phosphor.json`. This is useful when your OS or terminal intercepts certain key combinations (e.g., `ctrl+alt+v` for clipboard on Linux, `ctrl+v` on Windows terminals, `ctrl+alt+<key>` on macOS).

## Configuration

Add keybindings under `options.tui.keybindings` in your `phosphor.json`:

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

Unspecified bindings fall back to the defaults listed below.

## Format

Key values use the same format as [`charm.land/bubbles/v2/key`](https://charm.land/bubbles/v2/key):

- Single key: `"enter"`, `"tab"`, `"esc"`, `"@"`
- Modifier combos: `"ctrl+c"`, `"ctrl+alt+v"`, `"shift+enter"`
- Multiple keys per binding (comma-separated): `"ctrl+n,ctrl+j"`, `"esc,alt+esc"`

## Default Key Bindings

### Global

| Binding Name | Default | Description |
|---|---|---|
| `quit` | `ctrl+c` | Quit Phosphor |
| `help` | `ctrl+g` | Show more help |
| `commands` | `ctrl+p` | Open commands palette |
| `models` | `ctrl+m,ctrl+l` | Switch models |
| `suspend` | `ctrl+z` | Suspend session |
| `sessions` | `ctrl+s` | List sessions |
| `tab` | `tab` | Change focus |
| `toggle_yolo` | `ctrl+y` | Toggle YOLO mode |

### Editor

| Binding Name | Default | Description |
|---|---|---|
| `send_message` | `enter` | Send message |
| `open_editor` | `ctrl+o` | Open external editor |
| `newline` | `shift+enter,ctrl+j` | Insert newline |
| `add_image` | `ctrl+f` | Add image attachment |
| `paste_image` | `ctrl+v` | Paste image from clipboard |
| `mention_file` | `@` | Mention a file |
| `add_file` | `/` | Add file attachment |
| `attachment_delete_mode` | `ctrl+r` | Delete attachment at index i |
| `attachment_escape` | `esc,alt+esc` | Cancel delete mode |
| `delete_all_attachments` | `r` | Delete all attachments |
| `preview_attachment` | `ctrl+alt+v` | Preview attachment |
| `history_prev` | `up` | Previous message in history |
| `history_next` | `down` | Next message in history |
| `clear_prompt` | `ctrl+x` | Clear prompt |

### Chat

| Binding Name | Default | Description |
|---|---|---|
| `new_session` | `ctrl+n` | New session |
| `add_attachment` | `ctrl+f` | Add attachment |
| `cancel` | `esc,alt+esc` | Cancel current action |
| `details` | `ctrl+d` | Toggle details |
| `toggle_pills` | `ctrl+t,ctrl+space` | Toggle tasks |
| `pill_left` | `left` | Switch section (left) |
| `pill_right` | `right` | Switch section (right) |
| `scroll_down` | `down,ctrl+j,j` | Move down |
| `scroll_up` | `up,ctrl+k,k` | Move up |
| `scroll` | `up,down` | Scroll |
| `scroll_one_item_up` | `shift+up,K` | Up one item |
| `scroll_one_item_down` | `shift+down,J` | Down one item |
| `scroll_one_item` | `shift+up,shift+down` | Scroll one item |
| `half_page_down` | `d` | Half page down |
| `page_down` | `pgdown, ,f` | Page down |
| `page_up` | `pgup,b` | Page up |
| `half_page_up` | `u` | Half page up |
| `home` | `g,home` | Go to top |
| `end` | `G,end` | Go to bottom |
| `copy` | `c,y,C,Y` | Copy selected text |
| `clear_highlight` | `esc,alt+esc` | Clear selection |
| `expand` | `space` | Expand/collapse |

### Initialize

| Binding Name | Default | Description |
|---|---|---|
| `initialize_yes` | `y,Y` | Confirm |
| `initialize_no` | `n,N,esc,alt+esc` | Decline |
| `initialize_switch` | `left,right,tab` | Switch option |
| `initialize_enter` | `enter` | Select option |

## Listing Keybindings

Use the built-in `phosphor bindings` command to list all available keybindings grouped by section:

```bash
phosphor bindings
```

This outputs a formatted table of all bindings with their current keys and descriptions.

## Migration

This feature is fully backward-compatible. Existing `phosphor.json` files without `keybindings` will continue to work with all defaults unchanged.

## Notes

- Invalid key strings are silently ignored — the default binding is used instead.
- All 47 binding names listed above are recognized. If you use an unrecognized name, it is ignored.
- Key bindings are read at startup. Changes to `phosphor.json` require a restart of Phosphor.
