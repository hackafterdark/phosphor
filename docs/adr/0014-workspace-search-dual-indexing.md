# 14. Workspace Search: Dual Indexing (FTS5 + Optional Vector Embeddings)

- **Status:** Accepted
- **Date:** 2026-07-26
- **Authors:** Phosphor Team
- **Superseded By:** —

## Context

Phosphor provides agent tools for searching code and documents. Prior to this decision, code search relied on LLM-based semantic search (API calls) or grep/glob against raw files — slow, token-expensive, and limited to exact keyword matching. Document support was non-existent; only code files could be searched.

As Phosphor evolved beyond a pure coding agent, the need arose to index and search **all workspace content**: code symbols, PDFs, Word documents, spreadsheets, RTF, HTML, and XML. A single search mechanism needed to cover both structured code identifiers and unstructured document text, while optionally supporting semantic (conceptual) search for natural language queries.

## Decision

We introduced a unified **Workspace Search** system backed by SQLite FTS5 with two complementary indexing strategies:

1. **FTS5 Full-Text Search (primary)** — indexes code symbols extracted via Tree-sitter (`symbols_fts`) and converted document text (`docs_fts`). Instant (<1ms), zero API cost, no model needed.

2. **Optional Vector Embeddings (secondary)** — indexes document content into vector storage for semantic, concept-level matching. Gated behind `workspace_search.vector_embeddings.enabled` in config. Only activates when FTS5 returns no results for natural language queries.

Both strategies are **configurable and optional**. FTS5 is the default fast path; vector embeddings are an optional enhancement for queries where keyword matching is insufficient.

## Rationale

- **FTS5 for structured content**: Code symbols (functions, structs, interfaces, variables) are well-suited for exact or near-exact keyword matching. FTS5 provides instant lookups without network calls.
- **Document conversion pipeline**: PDF, DOCX, XLSX, RTF, HTML, and XML files are converted to plain text and indexed into `docs_fts`, enabling keyword search across non-code content.
- **Vector embeddings for conceptual queries**: Natural language queries ("how to configure X") benefit from semantic similarity search. This is reserved for fallback when FTS5 alone cannot answer.
- **Dual strategy coverage**: FTS5 handles ~90% of queries (symbol lookups, keyword matches). Vector search covers the remaining conceptual queries. Together they provide comprehensive coverage.
- **Future-proofing**: Supporting multiple document formats positions Phosphor beyond code editing — enabling document Q&A, knowledge-base search, and cross-format information retrieval.

## Implementation

### FTS5 Schema

```sql
CREATE VIRTUAL TABLE symbols_fts USING fts5(
    path, name, qualified_name, signature, documentation
);
CREATE VIRTUAL TABLE docs_fts USING fts5(
    path, content
);
```

### Document Converters

| Format | Go Package | Target Table |
|---|---|---|
| PDF | `github.com/coregx/gxpdf` | `docs_fts` |
| DOCX | `github.com/unidoc/unioffice` | `docs_fts` |
| XLSX | `github.com/xuri/excelize/v2` | `docs_fts` |
| RTF | `github.com/attilabuti/striprtf` | `docs_fts` |
| HTML/XML | `golang.org/x/net/html` + `encoding/xml` | `docs_fts` |

### Agent Tool

The `workspace_search` tool queries both FTS5 tables. Optional semantic fallback triggers only when FTS5 returns empty results for natural language queries.

### Configuration

```json
{
  "workspace_search": {
    "enabled": true,
    "max_file_size": 1048576,
    "vector_embeddings": {
      "enabled": false
    }
  }
}
```

## Consequences

- **Speed**: FTS5 search is sub-millisecond with zero API overhead.
- **Coverage**: All workspace content (code + documents) is searchable through a single tool.
- **Flexibility**: Both indexing strategies are independently toggleable via config.
- **Cost savings**: Eliminates the need for LLM-based semantic search for the vast majority of queries.
- **Positioning**: Document format support extends Phosphor's scope from a code agent to a general-purpose workspace assistant capable of answering questions across mixed content types.