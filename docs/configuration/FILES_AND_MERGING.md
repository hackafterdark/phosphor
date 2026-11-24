# Configuration Files & Merge Order

Phosphor loads several `phosphor.json` files and deep-merges them. The files,
in the order they are loaded (later files win on conflicts):

| # | File | Who writes it | Location |
|---|------|---------------|----------|
| 1 | Global config | You | `$XDG_CONFIG_HOME/phosphor/phosphor.json` (fallback `~/.config/phosphor/phosphor.json`) |
| 2 | Global data config | Phosphor | Linux/macOS: `$XDG_DATA_HOME/phosphor/phosphor.json` (fallback `~/.local/share/phosphor/phosphor.json`) · Windows: `%LOCALAPPDATA%\phosphor\phosphor.json` |
| 3 | Project configs | You | Any `phosphor.json` or `.phosphor.json` from the current directory up to the git working-tree root |
| 4 | Workspace config | Phosphor | `<data_directory>/phosphor.json` (default `.phosphor/phosphor.json`) |

Windows uses the same XDG logic for the global config, so the default is
`%USERPROFILE%\.config\phosphor\phosphor.json` unless `XDG_CONFIG_HOME` is set.

All files are **deep-merged per JSON key**, not replaced wholesale — a project
file that sets only `models.large` leaves everything else in your global file
intact. The files with the higher numbers above override the lower ones for
any key they define.

## File Purposes

The **global config** is where you put settings that apply everywhere — API
keys, default providers, personal preferences.

The **global data config** is where Phosphor persists things it learns at
runtime (recently used models, per-provider settings saved from the TUI).

The **project config** is safe to commit to a repository to share settings
with your team.

The **workspace config** is written by the TUI when you pick a model from the
model dialog (`Ctrl+L`). Workspace-scoped settings take precedence over
global ones.

If the TUI ever writes something to the global data config that you disagree
with (e.g. a `models.small` selection), edit or delete that key there — it
overrides the same key in your global config.

## Environment Variables

| Variable | Effect |
|----------|--------|
| `XDG_CONFIG_HOME` | Overrides the base for the global config dir |
| `XDG_DATA_HOME` | Overrides the base for the global data config (Linux/macOS) |
| `PHOSPHOR_GLOBAL_CONFIG` | Set to a directory; Phosphor uses `<dir>/phosphor.json` as the global config |
| `PHOSPHOR_GLOBAL_DATA` | Set to a directory; overrides the global data config location |
| `PHOSPHOR_CACHE_DIR` | Overrides the cache directory |
| `PHOSPHOR_SKILLS_DIR` | Overrides the global skills directory |

### `PHOSPHOR_` Prefix Overlay

At startup, any variable named `PHOSPHOR_FOO=bar` is exposed to provider
resolution as `FOO=bar`. This lets you scope keys per shell without clobbering
a global `OPENAI_API_KEY`, for example:

```bash
PHOSPHOR_ANTHROPIC_API_KEY=sk-ant-... phosphor
```

## Data Directory

The workspace also gets a **data directory** (default `.phosphor/` at the
project root, configurable via `options.data_directory` or `--data-dir`). It
holds the SQLite session database, logs, and the workspace-scoped
`.phosphor/phosphor.json`.

## Inspecting Resolved Paths

To print the exact paths Phosphor is using on your machine:

```bash
phosphor dirs
```

## Schema and Validation

Add the `$schema` key to get editor autocompletion and validation for every
section:

```json
{
  "$schema": "https://raw.githubusercontent.com/hackafterdark/phosphor/main/schema.json"
}
```

The schema is generated from the live config structs; regenerate it locally
with `phosphor schema` if you are on a development build.
