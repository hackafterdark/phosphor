# Agent Behavior

The `options.agent` section controls agent loop behavior, guardrails, and
reasoning.

```json
{
  "options": {
    "agent": {
      "active_profile": "default",
      "enable_reflection": true,
      "max_turns": 60,
      "max_tokens_continuations": 2,
      "max_consecutive_tool_failures": 2,
      "max_consecutive_tool_failures_headless": 8,
      "structural_search_languages": ["go", "typescript"],
      "microcompaction": {
        "enabled": false,
        "threshold": 0.5,
        "retention_turns": 8
      }
    }
  }
}
```

## Fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `active_profile` | string | `"default"` | Named agent profile to use (e.g. `"default"`, `"fiduciary"`, or any custom profile) |
| `enable_reflection` | bool | `true` | `true` runs a self-check pass after tool results |
| `max_turns` | int | `60` | Hard cap on tool-use turns per prompt (guardrail against runaway loops) |
| `max_tokens_continuations` | int | `2` | Times the agent auto-resumes a truncated turn (`0` = default of 2, negative = unlimited) |
| `max_consecutive_tool_failures` | int | `2` | Consecutive same-tool failures before halting interactive runs (`0` = default of 2, negative = never halt) |
| `max_consecutive_tool_failures_headless` | int | `8` | Same budget for headless runs / cron (`0` = default of 8, negative = never halt) |
| `structural_search_languages` | string[] | all | Limit which languages are advertised in the tool description (does not restrict what the tool can search) |
| `microcompaction.enabled` | bool | `false` | `true` enables deterministic elision of stale tool outputs |
| `microcompaction.threshold` | float | `0.5` | Fraction of context window (0–1) before microcompaction triggers |
| `microcompaction.retention_turns` | int | `8` | Recent turns whose tool outputs are always kept |

## Notes

- `max_tokens_continuations` — Each continuation is a full extra model call.
  `0` selects the default of two continuations and a negative value removes
  the limit; there is no supported way to disable continuations entirely.
  Raise this if long single-response tasks (e.g. generating one large file)
  keep truncating, or lower it if you rely on a low `max_tokens` for cost
  control and would rather see the truncation than pay for a retry.

- `max_consecutive_tool_failures` — Interactive sessions can keep the tight
  default because a human can step in and redirect the agent.

- `max_consecutive_tool_failures_headless` — Iterative debugging (build, test,
  edit, retry) legitimately fails several times in a row, so the headless
  default is deliberately wider than the interactive one.

- `microcompaction` — Applied before summarization. Deterministically elides
  stale tool outputs from older turns. See
  [COMPACTION.md](../COMPACTION.md#microcompaction-deterministic-tool-output-elision)
  for details.

## Auto-Summarization

Control when the conversation gets summarized:

```json
{
  "options": {
    "disable_auto_summarize": false,
    "summarize_threshold": 0.8,
    "summarize_model": "small"
  }
}
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `disable_auto_summarize` | bool | `false` | `true` disables automatic summarization |
| `summarize_threshold` | float | `0.8` | Fraction of context window (0–1) that triggers summarization |
| `summarize_model` | string | `"main"` | Which model slot performs summarization: `"main"`, `"large"`, or `"small"` |

See [COMPACTION.md](../COMPACTION.md) for the full compaction pipeline.
