# ADR-0004: OpenTelemetry GenAI Semantic Convention Instrumentation

## Status

Accepted

## Context

Phosphor originally had **no distributed tracing or structured observability**. An RFC was authored to plan OTel integration ([`.agents/docs/rfc/opentelemetry-instrumentation.md`](../../rfc/opentelemetry-instrumentation.md)), which outlined a phased implementation covering tracing, metrics, configuration, and a no-op-by-default design.

In a previous session, Phosphor's OTel foundation was implemented per the RFC:

- **Phase 1**: OTel SDK initialization, tracer provider, OTLP exporter (`internal/otel/otel.go`, config wiring in `internal/app/app.go`)
- **Phase 2**: Agent turn spans — `agent.turn`, `agent.turn.llm.request`, `agent.turn.prepare` (`internal/agent/agent.go`)
- **Phase 3**: Tool call spans — `tool.call` via `hookedTool` wrapper (`internal/agent/hooked_tool.go`)
- **Phase 4**: Hook spans — `hook.run` (`internal/hooks/runner.go`), MCP spans — `mcp.tool_call` (`internal/agent/tools/mcp/tools.go`)
- **Phase 6**: Metrics — `phosphor.tool_calls.total`, `phosphor.llm_requests.total`, `phosphor.llm_tokens.total`, `phosphor.agent_turn.duration`, etc. (`internal/otel/metrics.go`)

The existing instrumentation used Phosphor-specific span names and attribute keys (`agent.turn`, `tool.call`, `llm.provider`, `llm.model`). This made it impossible to correlate Phosphor traces with traces from other GenAI tools (Cursor, Claude Code, W&B Weave, LangSmith, etc.) in a unified observability backend.

The OpenTelemetry project maintains a **GenAI Semantic Conventions** specification (https://opentelemetry.io/docs/specs/semconv/gen-ai/) that defines standardized span names, attributes, and metric names for generative AI operations. This specification covers:

- **Span operations**: `chat`, `embeddings`, `retrieval`, `execute_tool`, `invoke_agent`, `create_agent`, `invoke_workflow`, `plan`
- **Standard attributes**: `gen_ai.operation.name`, `gen_ai.provider.name`, `gen_ai.request.model`, `gen_ai.usage.input_tokens`, `gen_ai.usage.output_tokens`, etc.
- **Standard metrics**: `gen_ai.client.token.usage`, `gen_ai.client.operation.duration`, `gen_ai.client.operation.time_to_first_chunk`

## Decision

We adopted the OpenTelemetry GenAI semantic conventions for all new instrumentation while preserving the existing Phosphor-specific metrics as a parallel set. The changes span four files:

### 1. `internal/otel/otel.go` — GenAI helper functions

Added a `GenAIAttributes` struct and two helper functions:

- **`StartGenAISpan(ctx, spanName, attrs)`** — creates a span pre-populated with GenAI attributes
- **`SetGenAIAttributes(span, attrs)`** — sets GenAI attributes on an existing span

Both functions handle the full set of GenAI semantic convention attributes:

| Category | Attributes |
|---|---|
| Operation | `gen_ai.operation.name` |
| Provider | `gen_ai.provider.name` |
| Model | `gen_ai.request.model`, `gen_ai.response.model` |
| Agent | `gen_ai.agent.name`, `gen_ai.agent.id`, `gen_ai.agent.description`, `gen_ai.agent.version` |
| Workflow | `gen_ai.workflow.name` |
| Tool | `gen_ai.tool.name`, `gen_ai.tool.type`, `gen_ai.tool.call.id`, `gen_ai.tool.call.arguments`, `gen_ai.tool.call.result` |
| Usage | `gen_ai.usage.input_tokens`, `gen_ai.usage.output_tokens`, `gen_ai.usage.reasoning.output_tokens`, `gen_ai.usage.cache_creation.input_tokens`, `gen_ai.usage.cache_read.input_tokens` |
| Request params | `gen_ai.request.temperature`, `gen_ai.request.top_p`, `gen_ai.request.top_k`, `gen_ai.request.max_tokens`, `gen_ai.request.frequency_penalty`, `gen_ai.request.presence_penalty` |
| Response | `gen_ai.response.finish_reason`, `gen_ai.response.id` |
| Conversation | `gen_ai.conversation.id` |
| Error | `gen_ai.error.message`, `error.type` |

### 2. `internal/otel/metrics.go` — GenAI standard metrics

Added three new metric instruments following the GenAI spec:

| Metric | Type | Description |
|---|---|---|
| `gen_ai.client.token.usage` | Histogram | Token counts with `gen_ai.token.type` attribute (`input`/`output`) |
| `gen_ai.client.operation.duration` | Histogram | Operation latency in seconds |
| `gen_ai.client.operation.time_to_first_chunk` | Histogram | Streaming time-to-first-byte in seconds |

The existing Phosphor-specific metrics (`phosphor.tool_calls.total`, `phosphor.llm_requests.total`, etc.) are preserved alongside these new metrics, providing a migration path for existing dashboards.

### 3. `internal/agent/agent.go` — Agent spans

Updated the three agent-level spans to follow GenAI conventions:

| RFC Span Name | New Span Name | GenAI Attributes Added |
|---|---|---|
| `agent.turn` | `invoke_agent Phosphor Agent` | `gen_ai.operation.name=invoke_agent`, `gen_ai.agent.name` |
| `agent.turn.llm.request` | `chat {model}` | `gen_ai.operation.name=chat`, `gen_ai.provider.name`, `gen_ai.request.model` |
| `agent.turn.prepare` | `plan Phosphor Agent` | `gen_ai.operation.name=plan`, `gen_ai.agent.name` |

Additionally, token usage and duration metrics are now recorded at the end of each LLM request, using both the Phosphor-specific metrics and the new GenAI standard metrics.

### 4. `internal/agent/hooked_tool.go` — Tool execution spans

Updated the tool call span:

| RFC Span Name | New Span Name | GenAI Attributes Added |
|---|---|---|
| `tool.call` | `execute_tool {tool.name}` | `gen_ai.operation.name=execute_tool`, `gen_ai.tool.name`, `gen_ai.tool.type=function` |

### 5. `internal/agent/tools/mcp/tools.go` — MCP tool spans

Updated the MCP tool call span:

| RFC Span Name | New Span Name | GenAI Attributes Added |
|---|---|---|
| `mcp.tool_call` | `execute_tool {tool.name}` | `gen_ai.operation.name=execute_tool`, `gen_ai.tool.name`, `gen_ai.tool.type=function` |

MCP tools are function tools from the GenAI perspective, so they use the same `execute_tool` span type as other tool calls. The MCP server name is preserved as an additional attribute.

### 6. `internal/otel/otel_test.go` — Tests

Added tests for the new GenAI helper functions:
- `TestStartGenAISpan_NoEndpoint` — verifies span creation with full GenAI attributes
- `TestSetGenAIAttributes_NilSpan` — nil-safety
- `TestRecordGenAIMetrics_NoMetrics` — metrics no-op safety

## Consequences

**Positive:**
- **Interoperability**: Phosphor traces are now compatible with the broader GenAI observability ecosystem. Tools like W&B Weave, LangSmith, Arize Phoenix, and Langfuse all understand the `gen_ai.*` attribute namespace and can correlate Phosphor traces with traces from other GenAI applications.
- **Standardized attributes**: All spans now include `gen_ai.operation.name`, which is the primary discriminator for filtering and grouping traces in OTel-compatible backends.
- **Provider identification**: The `gen_ai.provider.name` attribute enables filtering traces by provider (e.g., `openai`, `anthropic`, `aws.bedrock`).
- **Token tracking**: Token usage is now tracked with the standard `gen_ai.usage.*` attributes, making it easy to compute cost and performance metrics.
- **Backward compatibility**: Existing Phosphor-specific metrics are preserved, so existing dashboards and alerting rules continue to work.
- **Future-proof**: The helper functions (`StartGenAISpan`, `SetGenAIAttributes`) make it easy to add new GenAI instrumentation points without duplicating attribute logic.

**Negative:**
- **Span name changes**: The span names have changed (e.g., `agent.turn` → `invoke_agent Phosphor Agent`). Existing trace queries that reference the old span names will need to be updated.
- **Larger span payloads**: Each span now includes many more attributes (up to 30+ GenAI attributes). This increases the size of each span in the trace data. However, OTel backends typically drop attributes that are not sampled or not configured for export, so this impact is limited in practice.
- **No streaming metrics yet**: The `gen_ai.client.operation.time_to_first_chunk` metric is defined but not yet populated in the agent loop. This can be added in a future iteration by tracking the time between the LLM request start and the first `OnTextDelta` callback.

**Open Questions:**
- Should we populate `gen_ai.conversation.id` with the session ID? This would enable correlating all spans from a single conversation.
- Should we populate `gen_ai.input.messages` and `gen_ai.output.messages` as opt-in attributes? These contain PII and should be gated behind a config option.
- Should we add `gen_ai.request.stream` to indicate whether streaming was used?
- The `gen_ai.client.operation.time_to_first_chunk` metric is defined but not yet recorded — this is a known gap.

## Alignment with RFC

The RFC outlined a phased implementation plan. Here is the current status:

| RFC Phase | Description | Status |
|---|---|---|
| Phase 1: OTel SDK Foundation | Tracer provider, OTLP exporter, config | **Done** (previous session) |
| Phase 2: Agent Turn Spans | `agent.turn`, `agent.turn.llm.request`, `agent.turn.prepare` | **Done** (previous session, renamed + gen_ai attrs in this session) |
| Phase 3: Tool Call Spans | `tool.call` wrapper, individual tool spans | **Partially done** — `tool.call` renamed to `execute_tool` with gen_ai attrs. Individual tool spans (`tool.bash`, `tool.edit`, etc.) not yet added. |
| Phase 4: Hooks, LSP, MCP | `hook.run`, `lsp.init`, `mcp.tool_call` | **Done** (previous session, MCP renamed to `execute_tool` with gen_ai attrs in this session) |
| Phase 5: HTTP Server & Database | HTTP request spans, DB query spans | **Not started** |
| Phase 6: Metrics | Counters and histograms | **Done** (previous session, gen_ai standard metrics added in this session) |

The RFC also envisioned:
- Per-tool spans (`tool.bash`, `tool.edit`, etc.) — not yet implemented
- LSP request spans (`lsp.request`) — not yet implemented
- HTTP server spans (`http.request`) — not yet implemented
- DB query spans (`db.query`) — not yet implemented

## Alternatives

### Option A: Replace Phosphor-specific metrics entirely

Replace `phosphor.tool_calls.total`, `phosphor.llm_requests.total`, etc. with the GenAI standard metrics and remove the Phosphor-specific ones.

**Rejected** because it would break existing dashboards and alerting rules. The dual-metric approach (both Phosphor-specific and GenAI standard) provides a safe migration path.

### Option B: Use only the OTel Go SDK's built-in semantic conventions

Use the `go.opentelemetry.io/otel/semconv/*` packages directly instead of defining our own attribute keys.

**Rejected** because the OTel Go semconv packages don't yet include GenAI-specific attribute keys. The GenAI conventions are still in development and maintained in a separate repository (`github.com/open-telemetry/semantic-conventions-genai`). Defining our own attribute keys gives us control over the naming and makes it easier to update when the official Go semconv package catches up.

### Option C: Only add the `gen_ai.operation.name` attribute

Add just the single required attribute (`gen_ai.operation.name`) to each span and leave the rest of the instrumentation unchanged.

**Rejected** because a single attribute provides very limited observability value. The full set of GenAI attributes enables rich filtering, grouping, and cost tracking in observability backends.

## References

- [OpenTelemetry GenAI Semantic Conventions](https://opentelemetry.io/docs/specs/semconv/gen-ai/)
- [GenAI Spans Documentation](https://opentelemetry.io/docs/specs/semconv/gen-ai/gen-ai-spans/)
- [GenAI Agent Spans Documentation](https://opentelemetry.io/docs/specs/semconv/gen-ai/gen-ai-agent-spans/)
- [RFC: OpenTelemetry Instrumentation for Phosphor](../../rfc/opentelemetry-instrumentation.md)
- `internal/otel/otel.go` — GenAI helper functions and attribute keys
- `internal/otel/metrics.go` — GenAI standard metrics
- `internal/agent/agent.go` — agent span instrumentation
- `internal/agent/hooked_tool.go` — tool execution span instrumentation
- `internal/agent/tools/mcp/tools.go` — MCP tool span instrumentation
- `internal/otel/otel_test.go` — tests for GenAI helpers
