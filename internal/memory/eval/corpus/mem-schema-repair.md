---
id: mem-schema-repair
type: decision
thread: tool-call-reliability
summary: "Near-miss tool call arguments are repaired against the schema before decode"
source: "session#seed-2026-09"
phosphor.status: active
phosphor.trust: 0.85
phosphor.asserted: true
phosphor.created: "2026-09-15T09:00:00Z"
phosphor.updated: "2026-09-20T11:00:00Z"
phosphor.last_used: "2026-09-15T09:00:00Z"
---

Stringified arrays coerce, unknown keys are dropped, case drift is canonicalised and truncated JSON is sanitised instead of poisoning the resend history.
