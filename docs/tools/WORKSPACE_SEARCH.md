# Workspace Search (FTS5)

## Description

`workspace_search` performs full-text search over a local SQLite FTS5 index. It searches code symbols (functions, types, methods) and document text (Markdown, config files, etc.) with zero API calls and sub-millisecond latency.

## Usage

### Parameters

| Parameter | Type | Required | Description |
|---|---|---|---|
| `query` | string | Yes | Search query (keywords or identifier) |
| `table` | string | No | Which table to search: `symbols`, `docs`, or `all` (default: `all`) |
| `limit` | int | No | Max results (default: 10, max: 50) |

### Example

```
Tool: workspace_search
Parameters:
  query: "Coordinator"
  table: symbols
  limit: 5
```

### Output

Returns up to `limit` results, each containing:

| Field | Type | Description |
|---|---|---|
| `path` | string | File path |
| `name` | string | Symbol name (functions, types) |
| `qualified_name` | string | Fully qualified name (e.g., `package.Type`) |
| `signature` | string | Function signature or type definition |
| `content` | string | Document text snippet (for docs table) |

## Requirements

- `workspace_search.fulltext.enabled` or `workspace_search.fulltext.auto_index` must be true in `phosphor.json`
- The workspace must be indexed (auto-index or manual `phosphor index` CLI command)

## How It Works

1. Code files are parsed with Tree-sitter to extract symbols (functions, types, methods).
2. Non-code files (Markdown, configs, etc.) are indexed as plain text.
3. Both are stored in an FTS5 virtual table (`symbols_fts` and `docs_fts`).
4. Searches use SQLite FTS5 match queries — instant, local, zero API calls.
5. File watcher with configurable debounce keeps the index up-to-date.

## Tips

- Use `workspace_search` for fast identifier lookups and keyword searches.
- Use `semantic_search` for conceptual/natural language queries where keyword matching falls short.
- The `all` table option searches both symbols and docs in a single call.
- Index size is typically 2–5× the source text — much smaller than vector embeddings.