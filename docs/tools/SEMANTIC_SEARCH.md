# Semantic Search

## Description

`semantic_search` performs approximate nearest-neighbor (ANN) lookups against the codebase embedding store. It accepts a natural language query, embeds it using the configured embedding model, and returns the top-k most semantically similar code chunks.

## Usage

### Parameters

| Parameter | Type | Required | Description |
|---|---|---|---|
| `query` | string | Yes | Natural language query to search for (e.g., "error handling in the login function") |
| `count` | int | No | Number of results to return (default: 5, max: 20) |

### Example

```
Tool: semantic_search
Parameters:
  query: "how is the database connection pool configured"
  count: 3
```

### Output

Returns up to `count` results, each containing:

| Field | Type | Description |
|---|---|---|
| `id` | string | Unique chunk identifier (SHA-256 hash) |
| `file_path` | string | Path to the source file |
| `offset` | int | Byte offset of the chunk within the file |
| `content` | string | The actual code/text content of the chunk |
| `distance` | float64 | Vector distance (lower = more similar) |

## Requirements

- `codebase_index.enabled` or `codebase_index.auto_update` must be true in `phosphor.json`
- An embedding model must be configured in `models["embedding"]`
- The codebase must be indexed (or currently being indexed — WAL mode allows concurrent queries)

## How It Works

1. The query is embedded into a vector using the same model that created the index.
2. The vector is used to search the `vec_chunks` vec0 virtual table.
3. Results are returned sorted by distance (most similar first).
4. Because the database uses WAL mode, searches can run while indexing is in progress.

## Tips

- Use natural language, not code identifiers. The embedding model maps language to vectors.
- Smaller `count` returns faster results.
- Results improve as more of the codebase gets indexed. A partially indexed codebase will only return matches from indexed files.