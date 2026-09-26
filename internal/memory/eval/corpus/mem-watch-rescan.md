---
id: mem-watch-rescan
type: fact
thread: sqlite-index
summary: "The content hash watcher rescans changed files and prunes rows for vanished ones"
tags:
  - watcher
  - reindex
source: "session#seed-2026-09"
phosphor.status: active
phosphor.trust: 0.55
phosphor.asserted: false
phosphor.created: "2026-09-15T09:00:00Z"
phosphor.updated: "2026-09-23T10:00:00Z"
phosphor.last_used: "2026-09-15T09:00:00Z"
---

A ledger compares the file hash on every walk; a deleted markdown file counts as a human delete and drops its derived rows while the retire trail survives elsewhere.
