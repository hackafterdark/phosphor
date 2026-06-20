# TUI Theme System

Phosphor features a robust, user-configurable theme system for its terminal interface (TUI). You can customize every aspect of the application's color scheme using simple YAML or JSON files.

---

## Configuration

To activate a theme, specify it in your `phosphor.json` configuration file under the `options.tui.theme` key. You can apply it globally or at a workspace level.

```json
{
  "options": {
    "tui": {
      "theme": "synthwave"
    }
  }
}
```

---

## Directory Lookup Hierarchy

When resolving theme names (e.g. `"synthwave"`), Phosphor uses a tiered lookup strategy to find the configuration files. It scans the following candidate directories in order:

1.  **Direct Path**: If the setting contains path separators or begins with `/`, `./`, or `../`, it resolves directly (relative to the workspace root if not absolute).
2.  **Workspace Local (Highest Priority)**:
    *   `<workspace-root>/.phosphor/themes/synthwave.yaml` (or `.yml`, `.json`)
    *   `<workspace-root>/themes/synthwave.yaml` (or `.yml`, `.json`)
3.  **Global User Config (Lowest Priority)**:
    *   `$PHOSPHOR_GLOBAL_CONFIG/themes/synthwave.yaml` (if environment variable set)
    *   `~/.config/phosphor/themes/synthwave.yaml` (or `.yml`, `.json`)

This allows you to check workspace-specific themes directly into git repositories to share them across development teams.

---

## Palette Keys Reference

Here is a reference of the available color keys you can define in your theme files and which elements they control in the Phosphor TUI:

### Brand & Brand Contrast
| Key | Description | TUI Elements Controlled |
| :--- | :--- | :--- |
| `primary` | Primary brand accent color | Top-level title background, "PHOSPHOR" logo gradient start, active dialog header gradient start. |
| `secondary` | Secondary brand accent color | Blinking cursor color, logo gradient end, active selection bars, focus border lines. |
| `accent` | Brand highlighting accent | Focused input prompt character (`>`), active list bullets. |
| `keyword` | Keyword highlight color | Code block syntax highlighting for programming language keywords. |
| `onPrimary` | Text on primary backgrounds | Color of text when it sits on top of a `primary`-colored background. |

### Foreground & Background Defaults
| Key | Description | TUI Elements Controlled |
| :--- | :--- | :--- |
| `bgBase` | Main canvas background | The default background color for the entire TUI application. |
| `fgBase` | Main text foreground | Default text color for chat responses, commands, and headers. |
| `separator` | Dividers and borders | Horizontal/vertical pane division lines and markdown rules. |

### Subtle Typography / Contrast Text
| Key | Description | TUI Elements Controlled |
| :--- | :--- | :--- |
| `fgSubtle` | Mildly faded text | Inline code text color, blockquote line blocks. |
| `fgMoreSubtle` | Faded text | Helper descriptions, timestamps, metadata labels. |
| `fgMostSubtle` | Highly faded text | Placeholder text in inputs, line numbers in text areas, inactive options. |

### Paneling & Bubble Backgrounds
| Key | Description | TUI Elements Controlled |
| :--- | :--- | :--- |
| `bgMostVisible` | High contrast panels | Dialog boxes, popup input fields, code snippet boxes. |
| `bgLessVisible` | Medium contrast panels | Chat bubbles from the user. |
| `bgLeastVisible` | Low contrast panels | Sidebar backdrop, chat bubbles from the agent. |

### Status Indicators
| Key | Description | TUI Elements Controlled |
| :--- | :--- | :--- |
| `success` | Positive status indicator | Action success confirmation text, completed tasks checklist. |
| `successMoreSubtle` | Subtle success highlight | Inactive checkmarks, task lists. |
| `successMostSubtle` | Soft success foreground | Multi-line input continuation prompt characters (`::: `). |
| `error` | Failure / critical status | Error alert text, failed command diagnostics, crash indicators. |
| `warning` | Warning state | Unsaved state dialog headers, high-risk confirmation warnings. |
| `warningSubtle` | Subtle warning highlights | YOLO-mode input border highlights, warning bullet icons. |
| `info` | Informative state | Markdown headings, informational banners, info alerts. |
| `infoMoreSubtle` | Faded informational text | System logs, environment variables list. |
| `infoMostSubtle` | Soft informational text | Tool output details. |
| `destructive` | Destructive action highlights | Delete button highlights, git diff deletion lines. |
| `denied` | Permission denied status | Tool call refusal banners, security alerts. |
| `busy` | Processing status | Active prompt generator spinners, loading states. |

---

## Example Theme (`synthwave.yaml`)

```yaml
# Synthwave Aesthetic Theme
name: Synthwave
primary: "#ff007f"        # Neon Pink
secondary: "#00f0ff"      # Neon Cyan
accent: "#bd93f9"         # Pastel Purple
keyword: "#f1fa8c"        # Light Yellow

# Backgrounds
bgBase: "#181824"         # Deep Slate Navy
bgMostVisible: "#2a2a3c"
bgLessVisible: "#202030"
bgLeastVisible: "#1c1c28"

# Foregrounds
fgBase: "#f8f8f2"         # Off-white
fgSubtle: "#d8d8d2"
fgMoreSubtle: "#9898a2"
fgMostSubtle: "#585862"

# Contrast pairings
onPrimary: "#181824"
separator: "#202030"

# Status colors
success: "#50fa7b"
successMostSubtle: "#629657"
error: "#ff5555"
warning: "#ffb86c"
info: "#8be9fd"
```
