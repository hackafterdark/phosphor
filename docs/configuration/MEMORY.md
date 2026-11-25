# Memory

The durable memory vault (decisions/constraints that survive sessions):

```json
{
  "memory": {
    "enabled": true,
    "ask": "balanced",
    "adaptive": true,
    "proactive_recall": "hint",
    "cross_session": "related",
    "max_inject_bytes": 8192
  }
}
```

## Fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `false` | `true` enables the memory vault |
| `ask` | string | `"balanced"` | Write-approval posture — see below |
| `adaptive` | bool | `true` | `true` lets learned per-bucket thresholds shape around the `ask` dial |
| `proactive_recall` | string | `"hint"` | Session-start recall posture — see below |
| `cross_session` | string | `"related"` | Cross-session recall posture — see below |
| `max_inject_bytes` | int | `8192` | Max bytes injected into context (values above 32768 are clamped) |

## `ask` Values

| Value | Behavior |
|-------|----------|
| `"ask"` | Always prompt before writing |
| `"balanced"` | Auto-approve low-risk, prompt for sensitive |
| `"auto"` | Never prompt |

## `proactive_recall` Values

| Value | Behavior |
|-------|----------|
| `"off"` | No session-start recall hint |
| `"hint"` | Pay a small names-only "active threads" hint at session start |
| `"on"` | Always surface the active-threads hint |

## `cross_session` Values

| Value | Behavior |
|-------|----------|
| `"isolated"` | Recall stays within the current session |
| `"related"` | May pull cross-session distillates above the relevance floor |
| `"transcript"` | May pull raw cross-session transcript content |

## Related Docs

- [memory/OVERVIEW.md](../memory/OVERVIEW.md) — Memory system overview
- [memory/MEMORYGRAPH.md](../memory/MEMORYGRAPH.md) — Memory graph viewer
- [memory/INTEGRITY.md](../memory/INTEGRITY.md) — Integrity checks
- [memory/POISONING.md](../memory/POISONING.md) — Poisoning defenses
