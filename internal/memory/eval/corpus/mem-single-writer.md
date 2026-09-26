---
id: mem-single-writer
type: constraint
thread: memory-system
summary: "Only the memory tool writes the vault"
source: "session#seed-2026-09"
phosphor.status: active
phosphor.trust: 0.9
phosphor.asserted: true
phosphor.created: "2026-09-15T09:00:00Z"
phosphor.updated: "2026-09-24T08:00:00Z"
phosphor.last_used: "2026-09-15T09:00:00Z"
---

edit, write, append and multiedit stay refused under .phosphor/memory by the guard.
