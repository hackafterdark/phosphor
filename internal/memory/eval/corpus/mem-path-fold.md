---
id: mem-path-fold
type: environment
thread: windows-paths
summary: "Vault path checks fold separators, trailing slashes and drive letter case"
tags:
  - paths
  - windows
source: "session#seed-2026-09"
phosphor.status: active
phosphor.trust: 0.55
phosphor.asserted: false
phosphor.created: "2026-09-15T09:00:00Z"
phosphor.updated: "2026-09-16T12:00:00Z"
phosphor.last_used: "2026-09-15T09:00:00Z"
---

IsVaultPath compares normalised forward-slash forms so a backslash path cannot dodge the write fence on windows.
