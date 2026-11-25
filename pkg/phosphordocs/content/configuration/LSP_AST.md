# LSP & AST

## LSP (Language Server Protocol)

Phosphor auto-starts language servers it knows about (based on root markers
like `go.mod` or `package.json`) unless `options.auto_lsp` is `false`.
Override or extend the table of contents:

```json
{
  "options": { "auto_lsp": true },
  "lsp": {
    "gopls": {
      "command": "gopls",
      "filetypes": ["go", "mod"],
      "root_markers": ["go.mod"],
      "init_options": {},
      "timeout": 30
    },
    "rust-analyzer": { "disabled": true }
  }
}
```

### Fields

| Field | Type | Description |
|-------|------|-------------|
| `command` | string | LSP server executable |
| `filetypes` | string[] | File extensions this server handles |
| `root_markers` | string[] | Files that identify the project root |
| `init_options` | object | Passed to the server on initialization |
| `timeout` | int | Timeout in seconds |
| `disabled` | bool | `true` disables this server |

## AST (tree-sitter structural search)

The `structural_search` tool uses tree-sitter grammars to query code by
syntax. You can restrict which languages the tool advertises in its system-
prompt description via `options.agent.structural_search_languages`:

```json
{
  "options": {
    "agent": {
      "structural_search_languages": ["go", "typescript"]
    }
  }
}
```

- An empty list (or omitting the field) shows all supported languages — the
  default behavior.
- This only filters the description text shown to the agent; the tool itself
  still works for every language at runtime. The filter exists purely to
  trim token usage for the languages you actually use.

For the full list of valid language IDs, the `/languages` dialog, and merge
behavior, see
[structural_search/CONFIGURATION.md](../structural_search/CONFIGURATION.md).

## See Also

- [MCP](MCP.md) — Model Context Protocol servers
