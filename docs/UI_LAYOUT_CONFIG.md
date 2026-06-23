# UI Layout Configuration

Phosphor's startup landing screen and sidebar are fully configurable via `phosphor.json`. You control which components appear, their order, and visibility.

## How It Works

Components are defined as an array. The array position determines display order. Components are visible by default — use `"hidden": true` to opt out. If you omit the `components` array entirely, Phosphor uses its built-in defaults. An empty `components` array hides everything.

## Landing Screen Configuration

Landing screen settings live under `options.tui.landing` in `phosphor.json`.

### Example

```json
{
  "options": {
    "tui": {
      "landing": {
        "max_columns": 2,
        "gap": 4,
        "vertical_gap": 1,
        "components": [
          { "id": "recent_sessions", "max_items": 5 },
          { "id": "quick_actions" },
          { "id": "active_llm" },
          { "id": "capabilities", "hidden": true }
        ]
      }
    }
  }
}
```

This shows recent sessions, quick actions, active LLM, and hides the capabilities component.

### Layout Settings

#### `max_columns`

Maximum number of columns to arrange components in. The layout is responsive — if the terminal window is too narrow, components stack into fewer columns.

#### `gap`

The number of blank character cells between columns.

#### `vertical_gap`

The number of blank lines between components stacked in the same column.

#### `components`

An array of component definitions. Each component has:

| Field | Type | Default | Description |
|---|---|---|---|
| `id` | `string` | — | Unique identifier (see available IDs below) |
| `hidden` | `bool` | `false` | Set to `true` to hide the component |
| `max_items` | `int` | `5` | Max items to show (applies to `recent_sessions`) |

Components are displayed in the order they appear in the array.

### Available Landing Components

| ID | Title | Description |
|---|---|---|
| `recent_sessions` | Recent Sessions | Lists the most recent sessions with titles and timestamps |
| `quick_actions` | Quick Actions | Shows keyboard shortcuts (New Session, Sessions List, Select Model, Commands, Quit) |
| `active_project` | Active Project | Displays the current working directory |
| `active_llm` | Active LLM | Shows the current model, provider, and token/cost info |
| `capabilities` | Capabilities | Lists active LSPs, MCPs, and Skills |

### Responsive Layout

The layout adapts to terminal width:

- When the window is wide enough, components are distributed across `max_columns`
- When the window narrows, columns collapse and components stack vertically
- Array order determines display sequence regardless of column count

### Default Landing Configuration

When `options.tui.landing` is omitted, Phosphor uses these defaults:

- `max_columns`: 2
- `gap`: 4
- `vertical_gap`: 1
- Components (in order): `recent_sessions`, `quick_actions`, `active_project`, `active_llm`, `capabilities`

## Sidebar Configuration

The right sidebar during active sessions is configurable via `options.tui.sidebar`.

### Example

```json
{
  "options": {
    "tui": {
      "sidebar": {
        "vertical_gap": 1,
        "components": [
          { "id": "logo" },
          { "id": "session_title" },
          { "id": "active_llm" },
          { "id": "files", "max_items": 5 },
          { "id": "lsps" },
          { "id": "mcps" },
          { "id": "skills", "hidden": true }
        ]
      }
    }
  }
}
```

### Sidebar Components

| ID | Description |
|---|---|
| `logo` | App logo (rendered by theme) |
| `session_title` | Current session title |
| `working_dir` | Working directory path |
| `active_llm` | Active model, provider, and token/cost info |
| `goal` | Current goal objective and status |
| `files` | Changed files (additions/deletions) |
| `lsps` | Active LSP servers |
| `mcps` | Active MCP connections |
| `skills` | Active skills |

### List Components

The `files`, `lsps`, `mcps`, and `skills` components are list-type components. Use `max_items` to control how many items are shown:

```json
{ "id": "files", "max_items": 5 }
```

If `max_items` is omitted, the default is 10. The list shows up to `max_items` items and adds an "…and N more" indicator if there are more items than the limit.

The sidebar renders in a single column. Components are displayed in array order with `vertical_gap` blank lines between them.

## Workspace-Level Override

Define a workspace-level config at `.phosphor/phosphor.json`:

```json
{
  "options": {
    "tui": {
      "landing": {
        "max_columns": 1,
        "components": [
          { "id": "active_llm" },
          { "id": "recent_sessions", "max_items": 3 }
        ]
      },
      "sidebar": {
        "components": [
          { "id": "logo" },
          { "id": "session_title" },
          { "id": "active_llm" },
          { "id": "files", "max_items": 5 }
        ]
      }
    }
  }
}
```

This shows only LLM and recent sessions on the landing screen, and only logo, session title, active LLM, and files in the sidebar.
