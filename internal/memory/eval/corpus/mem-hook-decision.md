---
id: mem-hook-decision
type: fact
thread: hooks-engine
summary: "Hook decisions aggregate so deny wins, and a halt stops the run after the turn"
tags:
  - hooks
source: "session#seed-2026-09"
phosphor.status: active
phosphor.trust: 0.55
phosphor.asserted: false
phosphor.created: "2026-09-15T09:00:00Z"
phosphor.updated: "2026-09-22T09:00:00Z"
phosphor.last_used: "2026-09-15T09:00:00Z"
---

The runner fans events out in parallel with a timeout, dedupes identical commands and parses both the phosphor and the claude code stdout shapes.
