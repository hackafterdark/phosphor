---
id: mem-docs-drift
type: fact
thread: docs-pipeline
summary: "The repo root docs folder is canonical and the embedded corpus is generated"
tags:
  - docs
source: "session#seed-2026-09"
phosphor.status: active
phosphor.trust: 0.55
phosphor.asserted: false
phosphor.created: "2026-09-15T09:00:00Z"
phosphor.updated: "2026-09-18T09:00:00Z"
phosphor.last_used: "2026-09-15T09:00:00Z"
---

The generator removes and recopies, and TestDriftGuard fails the suite when an allow-listed doc is not byte identical to its source.
