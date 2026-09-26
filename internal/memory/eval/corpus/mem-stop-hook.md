---
id: mem-stop-hook
type: fact
thread: hooks-engine
summary: "The post-turn seam runs after the transcript persists and never re-enters the model"
tags:
  - hooks
  - postturn
source: "session#seed-2026-09"
phosphor.status: active
phosphor.trust: 0.55
phosphor.asserted: false
phosphor.created: "2026-09-15T09:00:00Z"
phosphor.updated: "2026-09-22T09:00:00Z"
phosphor.last_used: "2026-09-15T09:00:00Z"
---

postturn.finishTurn fires the Stop and SessionEnd hook events, collects additional context and queues one system line for the next request.
