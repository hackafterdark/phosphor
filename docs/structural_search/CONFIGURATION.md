# Structural Search Language Configuration

Control which languages appear in the `structural_search` tool description shown to the agent in the system prompt.

## Why?

Every language listed in the tool description costs tokens. If you only work with Go and TypeScript, listing all 17 supported languages is wasted context window. Filtering to your actual stack keeps the system prompt lean.

**Important:** This only filters the *description text* shown to the agent. The `structural_search` tool itself still works for all languages at runtime — the filter is purely cosmetic for prompt budgeting.

## Config Option

```jsonc
{
  "options": {
    "agent": {
      "structural_search_languages": ["go", "typescript"]
    }
  }
}
```

Set `structural_search_languages` under `options.agent` to a non-empty list of language IDs. An empty list (or omitting the field entirely) shows all supported languages — the default behavior.

## Config Precedence and Merging

Phosphor loads config from multiple locations and merges them with later sources overriding earlier ones:

1. **Global config files** (lowest priority): `~/.config/phosphor/phosphor.json` or `~/.local/share/phosphor/phosphor.json`
2. **Workspace config**: `<workspace>/phosphor.json` or `<workspace>/.phosphor.json`
3. **Per-workspace data dir** (highest priority): `<workspace>/.phosphor/phosphor.json`

When a key exists in a higher-priority config, it completely replaces the value from lower-priority configs. For `structural_search_languages`:

- **Global only**: All workspaces use the global language list
- **Workspace override**: That workspace uses its own list, ignoring global
- **Inherit from global**: The workspace config has no `structural_search_languages` key — the global value (if any) is used

### Setting Up Inheritance

To let a workspace inherit the global configuration, simply omit `structural_search_languages` from the workspace config entirely. The `/languages` dialog provides a dedicated "Inherit from global config" option that removes the key from the workspace config file when selected alone.

## Interactive Configuration via Slash Command

Use the `/languages` slash command from the TUI prompt:

```
/languages
```

This opens a multi-select checkbox dialog listing all 17 supported languages plus an "Inherit from global config" option at the top. Features:

- **Toggle** — Press `Enter` or `Space` on any language to check/uncheck it
- **Selection count** — Title info shows how many languages are selected, plus the current global config state if any
- **Confirm** — Press `Esc` (or the close key) to save

### Dialog Options

| Option | Effect |
|--------|--------|
| Check specific languages | Write those language IDs to workspace config |
| Check "Inherit from global config" (alone) | Delete the key from workspace config, using whatever the global config defines |
| No languages checked | Write empty array `[]` to workspace config (shows all languages) |

The dialog always writes to the **workspace** config file only. Global config is read-only and displayed in the title info for reference.

## Examples

### Go-only backend

```jsonc
// phosphor.json (workspace root)
{
  "options": {
    "agent": {
      "structural_search_languages": ["go"]
    }
  }
}
```

### Full-stack project (Go + TypeScript + Python)

```jsonc
{
  "options": {
    "agent": {
      "structural_search_languages": ["go", "typescript", "python"]
    }
  }
}
```

### Global default for all projects

```jsonc
// ~/.config/phosphor/phosphor.json
{
  "options": {
    "agent": {
      "structural_search_languages": ["go", "typescript", "javascript"]
    }
  }
}
```

### Inherit from global in a specific workspace

Omit the field entirely from the workspace config, or use the `/languages` dialog and select "Inherit from global config". The workspace config file will not contain `structural_search_languages`, so the global value (if set) takes effect.

### Disable the filter (show all languages)

Either omit the field entirely, or set it to an empty array:

```jsonc
{
  "options": {
    "agent": {
      "structural_search_languages": []
    }
  }
}
```

## Valid Language IDs

These are the canonical identifiers used by Phosphor's parser. They match the keys in `DetectLanguage()` and the `Templates` registry:

| ID | Display Name | Extensions |
|----|-------------|------------|
| `go` | Go | `*.go` |
| `cpp` | C++ | `*.cpp`, `*.cc`, `*.cxx`, `*.hpp`, `*.hxx` |
| `c` | C | `*.c`, `*.h` |
| `bash` | Bash | `*.sh` |
| `hcl` | HCL | `*.hcl`, `*.tf` |
| `json` | JSON | `*.json` |
| `html` | HTML | `*.html`, `*.htm` |
| `css` | CSS | `*.css` |
| `toml` | TOML | `*.toml` |
| `scala` | Scala | `*.scala`, `*.sbt` |
| `typescript` | TypeScript | `*.ts`, `*.tsx` |
| `javascript` | JavaScript | `*.js`, `*.jsx` |
| `python` | Python | `*.py` |
| `php` | PHP | `*.php` |
| `sql` | SQL | `*.sql` |
| `rust` | Rust | `*.rs` |
| `csharp` | C# | `*.cs` |

**Not available:** `ruby` and `java` are excluded — their tree-sitter grammars are not currently enabled (see [LANGUAGE_NOTES.md](./LANGUAGE_NOTES.md) for details).

## Token Savings

A typical unfiltered tool description lists all 17 languages with extensions and template names — roughly **25-35 lines** of markdown. Filtering to 2-3 languages reduces this to **2-3 lines**, saving approximately **150-250 tokens** per session depending on the model's tokenization.

For long-running sessions or models with tight context windows (e.g., small local models), this can be meaningful.

## See Also

- [README.md](./README.md) — Full structural search tool documentation
- [LANGUAGE_NOTES.md](./LANGUAGE_NOTES.md) — Per-language grammar capabilities and known issues
- [MODELS_AND_PROVIDERS_CONFIG.md](../MODELS_AND_PROVIDERS_CONFIG.md) — Config file structure and merge behavior
