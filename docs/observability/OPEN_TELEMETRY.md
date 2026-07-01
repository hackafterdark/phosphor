# OpenTelemetry Instrumentation

Phosphor uses OpenTelemetry for distributed tracing and structured metrics, enabling correlation of agent turns, LLM API calls, tool executions, and hooks within unified observability backends.

Phosphor instruments three categories of operations:

1. **Agent turns** — Full conversation cycles (LLM calls + tool executions)
2. **LLM API calls** — Individual model requests (chat completions)
3. **Tool executions** — Bash, edit, view, grep, MCP, and other tools
4. **Infrastructure** — Hooks, LSP, MCP server communication

All GenAI spans follow the [OpenTelemetry GenAI Semantic Conventions](https://opentelemetry.io/docs/specs/semconv/gen-ai/), making Phosphor traces interoperable with W&B Weave, LangSmith, Arize Phoenix, Langfuse, and other GenAI observability tools.

## Configuration

### phosphor.json

Add an `observability` block to enable OTel:

```json
{
  "observability": {
    "endpoint": "http://localhost:4317",
    "protocol": "grpc",
    "sampling_rate": 1.0,
    "service_name": "phosphor",
    "resource_attributes": {
      "workspace.id": "my-workspace"
    }
  }
}
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `endpoint` | string | _(none — disabled)_ | OTLP endpoint (e.g., `localhost:4317` for gRPC, `http://localhost:4318/v1/traces` for HTTP) |
| `protocol` | string | `grpc` | Transport: `grpc` or `http/protobuf` |
| `sampling_rate` | float64 | `1.0` | Trace sampling rate (0.0 = never, 1.0 = always). Uses parent-based + trace ID ratio sampling |
| `service_name` | string | _(empty)_ | Service name for the OTel resource. Falls back to env var `OTEL_SERVICE_NAME` |
| `resource_attributes` | map[string]string | — | Additional key-value pairs attached to every span and metric |

### Environment Variables

Standard OTel environment variables are supported via `resource.WithFromEnv()`:

| Variable | Purpose |
|----------|---------|
| `OTEL_SERVICE_NAME` | Override the service name |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | Override the collector endpoint |
| `OTEL_EXPORTER_OTLP_PROTOCOL` | Override transport protocol (`grpc` or `http/protobuf`) |
| `OTEL_TRACES_SAMPLER` | Sampler type (`always_on`, `always_off`, `parentbased_always_on`, `parentbased_traceid_ratio`) |
| `OTEL_RESOURCE_ATTRIBUTES` | Additional resource attributes (comma-separated key=value pairs) |

### No-op by Default

When `endpoint` is empty, Phosphor uses a no-op tracer and meter. No network calls are made, no SDK overhead occurs, and all instrumentation points are safe to call.

## Architecture

```
internal/otel/
├── otel.go          # SDK init, tracer provider, GenAI helpers, span nesting
├── metrics.go       # Metric instruments (Phosphor-specific + GenAI standard)
└── otel_test.go     # Tests for no-op safety and GenAI helpers

internal/app/app.go  # Wires Init() and InitMetrics() into app lifecycle
```

### Initialization Flow

1. **`otel.Init()`** — Creates the tracer provider with OTLP exporter (gRPC or HTTP), installs it as the global provider, and sets up W3C trace context + baggage propagators
2. **`otel.InitMetrics()`** — Registers all metric instruments (counters and histograms). No-op if no endpoint is configured
3. Both return shutdown functions that are deferred at app exit

### Span Nesting

Tool call spans are automatically nested under the agent turn span via `otel.StartSpan()`. The agent turn span is stored in context (`otel.AgentTurnSpan`) and retrieved by `getAgentTurnSpan()` before each tool span creation. This ensures proper parent-child relationships without requiring explicit parent references at every call site.

### Batch Export

Spans are batched asynchronously for minimal performance impact:

- Batch timeout: 2 seconds
- Max batch size: 512 spans
- Max queue size: 2048 spans
- Export timeout: 10 seconds

## Span Hierarchy

```
invoke_agent Phosphor (agent turn)          ← top-level, wraps entire turn
├── prompt_with_attachments                ← prompt preparation with attachments
├── chat gpt-4o                            ← LLM API call (child of agent turn)
│   └── execute_tool view                  ← tool execution (nested under LLM span)
│       └── hooks.run                      ← hook execution (if configured)
├── chat claude-sonnet                     ← second LLM call
│   └── execute_tool bash                  ← tool execution
│   └── execute_tool edit                  ← tool execution
└── execute_tool mcp                       ← MCP tool (direct child of agent turn)
```

### Span Names

| Span Name Pattern | Description | Span Kind |
|-------------------|-------------|-----------|
| `invoke_agent Phosphor` | Full agent turn | INTERNAL |
| `chat {model}` | Single LLM API call | CLIENT |
| `execute_tool {tool_name}` | Tool execution (native + MCP) | INTERNAL |
| `prompt_with_attachments` | Prompt building with attachments | INTERNAL |
| `attachment_prepare` | Attachment processing | INTERNAL |
| `hooks.pre_tool_use` | Pre-tool hook evaluation | INTERNAL |
| `hooks.run` | Individual hook command execution | INTERNAL |

### Span Kinds

- **CLIENT** — External API calls (LLM providers)
- **INTERNAL** — Local operations (agent turns, tool execution, hooks)

## GenAI Semantic Conventions

All GenAI spans include standardized attributes per the [OpenTelemetry GenAI SemConv](https://opentelemetry.io/docs/specs/semconv/gen-ai/).

### Operation Names

| Operation | Used On |
|-----------|---------|
| `invoke_agent` | Agent turn spans |
| `chat` | LLM API call spans |
| `execute_tool` | Tool execution spans (native + MCP) |
| `plan` | _(defined but not yet used)_ |

### Common Attributes

| Attribute | Description | Example |
|-----------|-------------|---------|
| `gen_ai.operation.name` | Standardized operation type | `"chat"`, `"execute_tool"`, `"invoke_agent"` |
| `gen_ai.provider.name` | LLM provider identifier | `"openai"`, `"anthropic"`, `"aws.bedrock"` |
| `gen_ai.request.model` | Model requested | `"gpt-4o"`, `"claude-sonnet-4-20250514"` |
| `gen_ai.response.model` | Model that responded | _(set post-call)_ |
| `gen_ai.agent.name` | Agent identifier | `"Phosphor"` |
| `gen_ai.conversation.id` | Session/conversation ID | `"session-abc123"` |

### Request Attributes

| Attribute | Description |
|-----------|-------------|
| `gen_ai.request.temperature` | Sampling temperature |
| `gen_ai.request.top_p` | Nucleus sampling parameter |
| `gen_ai.request.top_k` | Top-k sampling parameter |
| `gen_ai.request.max_tokens` | Maximum output tokens |
| `gen_ai.request.seed` | Deterministic sampling seed |
| `gen_ai.request.stop_sequences` | Stop sequences |
| `gen_ai.request.reasoning.level` | Reasoning effort level |
| `gen_ai.request.repetition_penalty` | Repetition penalty factor |

### Usage Attributes

| Attribute | Description |
|-----------|-------------|
| `gen_ai.usage.input_tokens` | Input tokens consumed |
| `gen_ai.usage.output_tokens` | Output tokens generated |
| `gen_ai.usage.reasoning.output_tokens` | Tokens used for reasoning |
| `gen_ai.usage.cache_creation.input_tokens` | Cache write tokens |
| `gen_ai.usage.cache_read.input_tokens` | Cache read tokens |

### Tool Attributes

| Attribute | Description |
|-----------|-------------|
| `gen_ai.tool.name` | Tool identifier |
| `gen_ai.tool.type` | Tool type (e.g., `"function"`) |
| `gen_ai.tool.call.id` | Unique call identifier |
| `gen_ai.tool.call.arguments` | JSON input passed to the tool |
| `gen_ai.tool.call.result` | Sanitized/redacted tool output |

### Response Attributes

| Attribute | Description |
|-----------|-------------|
| `gen_ai.response.finish_reason` | Why the model stopped | `"stop"`, `"length"`, `"tool_calls"`, `"content_filter"` |
| `gen_ai.response.id` | Model response identifier |

### Error Attributes

| Attribute | Description |
|-----------|-------------|
| `gen_ai.error.message` | Error message |
| `error.type` | Error type classification |

## Metrics

Phosphor emits two metric families: Phosphor-specific (for backward compatibility) and GenAI standard (following the OTel GenAI spec).

### Phosphor-Specific Metrics

| Metric | Type | Description |
|--------|------|-------------|
| `phosphor.tool_calls.total` | Counter | Total tool calls by name |
| `phosphor.llm_requests.total` | Counter | Total LLM API requests |
| `phosphor.llm_tokens.total` | Counter | Token usage (input/output) |
| `phosphor.tool_calls.duration` | Histogram | Tool call latency in ms |
| `phosphor.llm_requests.duration` | Histogram | LLM request latency in ms |
| `phosphor.agent_turn.duration` | Histogram | Full agent turn latency in ms |
| `phosphor.errors.total` | Counter | Errors by type |
| `phosphor.hooks.total` | Counter | Hook executions |
| `phosphor.lsp_requests.total` | Counter | LSP protocol requests |
| `phosphor.mcp_requests.total` | Counter | MCP server requests |

### GenAI Standard Metrics

| Metric | Type | Description |
|--------|------|-------------|
| `gen_ai.client.token.usage` | Histogram | Token counts with `gen_ai.token.type` attribute (`input`/`output`) |
| `gen_ai.client.operation.duration` | Histogram | Operation latency in seconds |
| `gen_ai.client.operation.time_to_first_chunk` | Histogram | Streaming time-to-first-byte in seconds _(not yet populated)_ |

## Sensitive Data Handling

MCP tool results are automatically sanitized before being recorded on spans. The `SensitiveMCPServers` config field lists MCP server names whose results should be redacted:

```json
{
  "observability": {
    "sensitive_mcp_servers": ["vault", "secret-manager"]
  }
}
```

Results from listed servers are replaced with `[REDACTED]` on the `gen_ai.tool.call.result` attribute. This prevents credentials, secrets, or other sensitive data from appearing in traces.

## Supported Backends

Phosphor's OTel integration works with any backend that accepts OTLP (gRPC or HTTP):

- **Jaeger** — Full trace UI with service dependency maps
- **Tempo** (Grafana) — Log and trace correlation
- **Datadog** — APM with GenAI-specific dashboards
- **AWS CloudWatch** — Native OTLP ingestion
- **W&B Weave** — GenAI-specific trace correlation
- **LangSmith** — LLM application tracing
- **Arize Phoenix** — ML observability
- **Langfuse** — Open-source LLM observability
- **SigNoz** — Open-source APM

## Example: Sending to a Local Collector

### Docker setup

```bash
docker run -d --name otel-collector \
  -p 4317:4317 \
  -p 4318:4318 \
  -v ./otel-config.yaml:/etc/otelcol-contrib/config.yaml \
  otel/opentelemetry-collector:latest
```

### phosphor.json

```json
{
  "observability": {
    "endpoint": "localhost:4317",
    "protocol": "grpc",
    "service_name": "phosphor-dev"
  }
}
```

## Example: Sending to Jaeger

```json
{
  "observability": {
    "endpoint": "http://jaeger:14250/api/traces",
    "protocol": "http/protobuf",
    "service_name": "phosphor"
  }
}
```

## Implementation Details

### Helper Functions

The `internal/otel` package provides reusable helpers for consistent instrumentation:

| Function | Purpose |
|----------|---------|
| `StartSpan(ctx, name)` | Create a span nested under the agent turn span if present; wraps in min-duration guard |
| `StartInvokeAgentSpan(ctx, agentName, conversationID)` | Create an `invoke_agent` span with GenAI attributes |
| `StartLLMSpan(ctx, provider, model, attrs)` | Create a `chat` span with full GenAI request/response attributes |
| `StartPromptWithAttachmentsSpan(...)` | Create a prompt preparation span |
| `SetGenAIAttributes(span, attrs)` | Set GenAI attributes on an existing span |
| `RecordError(span, err)` | Record error and set span status to Error |
| `SetErrorStatus(span, msg)` | Set span status to Error without recording the error |
| `DurationAttribute(d)` | Convert a duration to a `duration_ms` attribute |

### GenAIAttributes Struct

The `GenAIAttributes` struct in `internal/otel/otel.go` holds all optional GenAI semantic convention attributes. Fields are pointers for strings/numbers — nil fields are skipped during attribute construction, keeping spans lean.

### Zero-Duration Guard

Fast spans (sub-microsecond) are wrapped in a `minDurationSpan` that sleeps briefly to ensure at least 1µs duration. This prevents "Negative duration detected" warnings from OTel collectors.
