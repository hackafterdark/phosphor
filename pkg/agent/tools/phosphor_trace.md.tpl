# Overview

Read Phosphor's in-process OpenTelemetry span buffer: a compact per-turn trace
tree (span name, duration, nesting, status, key GenAI attributes) captured by
the local ring buffer configured via `observability.memory_buffer` (default {{ .DefaultLines }} spans returned, max {{ .MaxLines }}).

# Usage

- Returns recent completed spans for the current turn (or any session when
  `session_id` is provided)
- Use to diagnose agent-loop issues: which step stalled, provider/model used,
  tool call durations, finish reasons, where nesting broke
- Lines are indented to show parent/child nesting; each shows
  `name (duration ms) [status] [key attrs]`
- Key attributes surfaced per span: provider/model and `gen_ai.step.index`
  with `gen_ai.response.finish_reason` and token usage on chat spans,
  tool name on `execute_tool` spans

# Tips

- Default returns the last {{ .DefaultLines }} spans; use the `lines` parameter
  for more (max {{ .MaxLines }})
- Requires `observability.memory_buffer` > 0 in phosphor.json; the tool reports
  that the buffer is off when the ring was never installed
- Look for `status=Error` lines and long durations first when diagnosing
  problems
