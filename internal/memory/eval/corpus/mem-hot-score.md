---
id: mem-hot-score
type: fact
thread: memory-system
summary: "The hot score blends recall, helpfulness, recency and trust against staleness penalties"
tags:
  - lifecycle
  - scoring
source: "session#seed-2026-09"
phosphor.status: active
phosphor.trust: 0.55
phosphor.asserted: false
phosphor.created: "2026-09-15T09:00:00Z"
phosphor.updated: "2026-09-24T08:00:00Z"
phosphor.last_used: "2026-09-15T09:00:00Z"
---

Lifecycle SQL recomputes it on every refresh: twice the recall count, a recency curve, a helpfulness bonus, four times trust, minus expiry, staleness and supersede penalties.
