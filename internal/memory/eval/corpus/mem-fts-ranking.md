---
id: mem-fts-ranking
type: fact
thread: memory-system
summary: "BM25 ranks search hits, trust breaks ties, snippets cost one sentence"
tags:
  - fts5
  - ranking
source: "session#seed-2026-09"
phosphor.status: active
phosphor.trust: 0.55
phosphor.asserted: false
phosphor.created: "2026-09-15T09:00:00Z"
phosphor.updated: "2026-09-24T08:00:00Z"
phosphor.last_used: "2026-09-15T09:00:00Z"
---

The search statement orders by the bm25 rank ascending then trust descending, and the SQLite snippet call caps the excerpt at a dozen tokens.
