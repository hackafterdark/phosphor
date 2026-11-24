# Workspace Search

Zero-API full-text (FTS5) indexing plus optional vector embeddings:

```json
{
  "workspace_search": {
    "fulltext": {
      "enabled": true,
      "auto_index": true,
      "debounce_ms": 2000,
      "exclude_patterns": ["node_modules/**", "dist/**"],
      "max_file_size": 1048576,
      "index_documents": true
    },
    "vector_embeddings": {
      "enabled": false,
      "auto_index": false,
      "max_chunk_size": 512,
      "chunk_overlap": 128,
      "embedding_dims": 384
    }
  }
}
```

## `fulltext`

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `false` | `true` enables full-text search |
| `auto_index` | bool | `true` | `true` re-indexes on file changes |
| `debounce_ms` | int | `2000` | Debounce interval (ms) before indexing |
| `exclude_patterns` | string[] | `[]` | Glob patterns to exclude |
| `max_file_size` | int | `1048576` | Max file size in bytes to index |
| `index_documents` | bool | `true` | `true` also indexes non-code documents |

## `vector_embeddings`

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `false` | `true` enables vector search |
| `auto_index` | bool | `false` | `true` re-indexes on file changes |
| `max_chunk_size` | int | `512` | Chunk size (tokens) for embedding |
| `chunk_overlap` | int | `128` | Overlap (tokens) between chunks |
| `embedding_dims` | int | `384` | Embedding vector dimensions |

## Notes

Full-text indexing is cheap and stays fresh automatically once enabled;
vector indexing spends embedding-model calls, so it's off by default.

## Related Docs

- [CODEBASE_INDEXING.md](../CODEBASE_INDEXING.md) — Indexing architecture
- [tools/WORKSPACE_SEARCH.md](../tools/WORKSPACE_SEARCH.md) — Search tool usage
