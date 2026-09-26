---
id: mem-snapshot-frozen
type: fact
thread: memory-system
summary: "Session snapshots freeze the injected window so the provider prefix cache keeps hitting"
tags:
  - prefix-cache
  - snapshot
source: "session#seed-2026-09"
phosphor.status: active
phosphor.trust: 0.55
phosphor.asserted: false
phosphor.created: "2026-09-15T09:00:00Z"
phosphor.updated: "2026-09-24T08:00:00Z"
phosphor.last_used: "2026-09-15T09:00:00Z"
---

Mid-session writes reach the disk and the index but not the frozen block; the next session adopts them, and hysteresis keeps two topics from ping-ponging the window.
