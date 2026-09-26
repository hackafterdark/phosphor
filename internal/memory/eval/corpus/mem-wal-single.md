---
id: mem-wal-single
type: fact
thread: sqlite-index
summary: "The index opens with WAL, a thirty second busy timeout and one writer connection"
tags:
  - sqlite
  - wal
source: "session#seed-2026-09"
phosphor.status: active
phosphor.trust: 0.55
phosphor.asserted: false
phosphor.created: "2026-09-15T09:00:00Z"
phosphor.updated: "2026-09-23T10:00:00Z"
phosphor.last_used: "2026-09-15T09:00:00Z"
---

A single writer connection dodges the WAL desync seen before; watcher, agent and a parallel session serialise on the file lock instead of corrupting it.
