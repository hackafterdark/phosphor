# Codebase Indexing

Codebase indexing walks the workspace, splits source files into overlapping chunks, generates embedding vectors via a configured embedding model, and stores them in a SQLite database backed by the sqlite-vec extension. The `semantic_search` agent tool performs approximate nearest-neighbor (ANN) lookups against this store.

## Configuration

All settings live in `phosphor.json` under the `codebase_index` key.

| Setting | Type | Default | Description |
|---|---|---|---|
| `enabled` | bool | `false` | Master toggle for indexing and the semantic_search tool. |
| `max_chunk_size` | int | 512 | Maximum characters per chunk. Larger values capture more context per embedding. |
| `chunk_overlap` | int | 128 | Overlap in characters between consecutive chunks to preserve semantic context across boundaries. |
| `embedding_dims` | int | 384 | Expected embedding vector dimensionality. Must match the embedding model output. |
| `excluded_paths` | []string | `[]` | List of glob patterns for paths to exclude from indexing. |
| `auto_update` | bool | `false` | When enabled, the indexer monitors the workspace for changes and re-indexes automatically. |

Example configuration:

```json
{
  "codebase_index": {
    "enabled": true,
    "max_chunk_size": 512,
    "chunk_overlap": 128,
    "embedding_dims": 384,
    "auto_update": false,
    "excluded_paths": ["vendor/**", "node_modules/**"]
  }
}
```

## Exclusion Files

Patterns can be excluded from indexing through three mechanisms:

### `.phosphorindexignore`

A workspace-level file with glob patterns, similar to `.gitignore`. Only affects the indexer — the agent can still read and work with matched files. Place in the workspace root:

```
grammars/
build/
dist/
```

### `.gitignore` and `.phosphorignore`

Patterns from both files are loaded and merged. Files matching these patterns are skipped during indexing but remain accessible to the agent via `.phosphorignore`.

### Config `excluded_paths`

Glob patterns defined in `phosphor.json` under `codebase_index.excluded_paths`. Useful for project-specific exclusions that shouldn't affect `.gitignore`.

## How It Works

### Chunking

Each text file is split into overlapping chunks. A chunk of `max_chunk_size` characters advances by `max_chunk_size - chunk_overlap` positions, ensuring continuity across boundaries.

### Embedding

Chunks are sent in batches to the configured embedding model (see `models["embedding"]` in `phosphor.json`). The model returns `embedding_dims`-dimensional float32 vectors.

### Storage

Vectors are stored in `vec_chunks` (a vec0 virtual table) and metadata in `chunk_meta`. The database uses WAL mode, allowing `semantic_search` queries to run concurrently with active indexing.

### Resume & Change Detection

Each file is hashed with SHA-256 (`content_hash`). On restart, the indexer compares current file hashes against stored hashes — unchanged files are skipped. This makes re-indexing fast after an interruption.

### Semantic Search

The `semantic_search` tool takes a natural language query, embeds it, and performs an ANN lookup returning the top-k nearest neighbors with file path, offset, content, and distance.

## Performance Considerations

- **Embedding model choice**: The quality and speed of indexing depends heavily on the embedding model. A fast model like `nomic-embed-text` or `bge-small` is recommended for large codebases.
- **Batch size**: Chunks are embedded in batches of 32. Larger batches improve throughput but may exceed model token limits.
- **Retry logic**: 400 errors (input too long) trigger automatic content shrinking (90% per retry, max 3 retries). 404 triggers a single retry.

## Database

The index lives at `.phosphor/codebase_index.db` inside the workspace root. It uses the `modernc.org/sqlite` driver with the `sqlite-vec` extension, and WAL mode for concurrent read/write.