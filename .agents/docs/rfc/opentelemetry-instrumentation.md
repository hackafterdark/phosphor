# RFC: OpenTelemetry Instrumentation for Phosphor

## Status

Draft

## Problem

Phosphor currently has no distributed tracing or structured observability. When debugging performance issues, understanding agent behavior, or diagnosing failures across a user's workflow, developers and users are limited to:

- `slog` output (unstructured, no timing correlations)
- The `phosphor logs` command (session-scoped, no cross-component view)
- The optional pprof endpoint (`PHOSPHOR_PROFILE=1`, CPU/memory only)

This makes it impossible to answer questions like:

- How long did the full agent turn take, broken down by sub-phase?
- Which tool call was the slowest in a 20-turn session?
- How long did the LSP initialization take vs. the first AI response?
- Did an MCP server timeout, and how long did it block the turn?
- What is the end-to-end latency from user prompt to final response?

OpenTelemetry (OTel) provides a vendor-neutral, industry-standard way to emit traces, metrics, and logs that can be consumed by any OTel-compatible backend (Jaeger, Tempo, Datadog, Honeycomb, etc.).

## Proposed Solution

Add OpenTelemetry SDK integration to Phosphor with:

1. **Tracing**: Spans for every major operation (agent turns, tool calls, LSP operations, MCP operations, shell commands, HTTP requests, DB queries).
2. **Metrics**: Counters and histograms for tool call counts, token usage, error rates, and latencies.
3. **Configuration**: A new `observability` section in `phosphor.json` allowing users to configure the OTel collector endpoint and sampling.
4. **No-op by default**: Instrumentation is disabled unless the user configures an endpoint. When enabled, it uses OTel's `OTLPExporter` to send data to the configured collector.

### Why OpenTelemetry

- Industry standard — works with Jaeger, Grafana Tempo, Datadog, Honeycomb, New Relic, etc.
- SDK provides automatic context propagation, span lifecycle management, and resource attributes.
- `otelhttp` and `otelgrpc` middleware already exist in Phosphor's indirect deps (from catwalk/fantasy), so the dependency footprint is minimal.
- Can be enabled/disabled at runtime via config or env vars.

## Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                        Phosphor CLI/TUI                            │
│                                                                 │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌───────────────┐  │
│  │  Agent   │  │   Tools  │  │   LSP    │  │     MCP       │  │
│  │  Turn    │  │  Calls   │  │ Ops      │  │   Ops         │  │
│  └────┬─────┘  └────┬─────┘  └────┬─────┘  └───────┬───────┘  │
│       │             │             │                │           │
│       └─────────────┴─────────────┴────────────────┘           │
│                         │                                       │
│                  ┌──────▼──────┐                               │
│                  │  OTel SDK   │                               │
│                  │  Tracer     │                               │
│                  └──────┬──────┘                               │
│                         │                                       │
│                  ┌──────▼──────┐                               │
│                  │  OTLP       │                               │
│                  │  Exporter   │                               │
│                  └──────┬──────┘                               │
└─────────────────────────┼───────────────────────────────────────┘
                          │ OTLP (gRPC/HTTP)
                          ▼
              ┌─────────────────────┐
              │  OTel Collector     │
              │  (user-deployed)    │
              └──────────┬──────────┘
                         │
              ┌──────────▼──────────┐
              │  Backend (Jaeger,   │
              │  Tempo, Datadog,    │
              │  Honeycomb, etc.)   │
              └─────────────────────┘
```

## Configuration

### New config section in `phosphor.json`

```json
{
  "observability": {
    "endpoint": "http://localhost:4317",
    "protocol": "grpc",
    "sampling_rate": 1.0,
    "service_name": "phosphor",
    "resource_attributes": {
      "workspace.id": "workspace-123",
      "session.id": "session-456"
    }
  }
}
```

### Config struct

```go
// In internal/config/config.go

type Observability struct {
    // Endpoint is the OTel collector endpoint (e.g. "http://localhost:4317").
    // When empty, instrumentation is disabled.
    Endpoint string `json:"endpoint,omitempty" jsonschema:"description=OTel collector endpoint (gRPC or HTTP),example=http://localhost:4317"`
    // Protocol is the transport protocol: "grpc" or "http/protobuf".
    // Defaults to "grpc" when Endpoint is set.
    Protocol string `json:"protocol,omitempty" jsonschema:"description=OTLP transport protocol,enum=grpc,enum=http/protobuf,default=grpc"`
    // SamplingRate controls the probability of a span being sampled (0.0-1.0).
    // Defaults to 1.0 (always sample) when set.
    SamplingRate float64 `json:"sampling_rate,omitempty" jsonschema:"description=Sampling probability 0.0-1.0,default=1.0"`
    // ServiceName identifies this Phosphor instance in traces.
    // Defaults to "phosphor".
    ServiceName string `json:"service_name,omitempty" jsonschema:"description=Service name for trace identification,default=phosphor"`
    // ResourceAttributes are additional key-value pairs attached to every span.
    ResourceAttributes map[string]string `json:"resource_attributes,omitempty" jsonschema:"description=Additional resource attributes for all spans"`
}
```

### Env var overrides

Users who prefer env vars (e.g., for CI/CD or Docker) can use standard OTel env vars:

| Env Var | Purpose |
|---|---|
| `OTEL_SERVICE_NAME` | Override service name |
| `OTEL_TRACES_SAMPLER` | Sampler type (`always_on`, `always_off`, `parentbased_always_on`, `parentbased_traceid_ratio`) |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | Override collector endpoint |
| `OTEL_EXPORTER_OTLP_PROTOCOL` | Override protocol (`grpc`, `http/protobuf`) |
| `PHOSPHOR_OTEL_ENABLED` | When set to `"1"`, enables instrumentation even without an endpoint config |

Env vars take precedence over `phosphor.json` values.

## Instrumentation Scope

### Priority 1: Core Agent Loop (Must Have)

These are the highest-value spans for understanding agent behavior:

| Span | Location | Description |
|---|---|---|
| `agent.turn` | `internal/agent/agent.go` — `SessionAgent.Run()` | Full agent turn from prompt to final response. Parent of all sub-spans. |
| `agent.turn.prepare` | `agent.go` — prompt preparation, session message loading | Context window estimation, summarization checks, message history loading. |
| `agent.turn.llm.request` | `agent.go` — `agent.Stream()` call | The actual LLM API call. Child of `agent.turn`. |
| `agent.turn.llm.response` | `agent.go` — streaming callbacks | Token usage, finish reason, latency. |
| `agent.turn.summarize` | `agent.go` — `Summarize()` | Auto-summarization calls. |
| `agent.turn.title` | `agent.go` — `generateTitle()` | Title generation for new sessions. |

### Priority 2: Tool Execution (High Value)

Each tool call gets a span, enabling per-tool latency analysis:

| Span | Location | Description |
|---|---|---|
| `tool.call` | `internal/agent/hooked_tool.go` — tool execution wrapper | Each tool call. Parent of tool-specific spans. |
| `tool.bash` | `internal/agent/tools/bash.go` | Shell command execution. |
| `tool.edit` | `internal/agent/tools/edit.go` | File edit operations. |
| `tool.multiedit` | `internal/agent/tools/multiedit.go` | Multi-file edit operations. |
| `tool.view` | `internal/agent/tools/view.go` | File read operations. |
| `tool.write` | `internal/agent/tools/write.go` | File write operations. |
| `tool.append` | `internal/agent/tools/append.go` | File append operations. |
| `tool.grep` | `internal/agent/tools/grep.go` | Text search operations. |
| `tool.glob` | `internal/agent/tools/glob.go` | File pattern matching. |
| `tool.ls` | `internal/agent/tools/ls.go` | Directory listing. |
| `tool.rg` | `internal/agent/tools/rg.go` | Ripgrep search. |
| `tool.fetch` | `internal/agent/tools/fetch.go` | HTTP fetch operations. |
| `tool.download` | `internal/agent/tools/download.go` | File download operations. |
| `tool.web_fetch` | `internal/agent/tools/web_fetch.go` | AI-powered web fetch. |
| `tool.web_search` | `internal/agent/tools/web_search.go` | Web search operations. |
| `tool.sourcegraph` | `internal/agent/tools/sourcegraph.go` | Sourcegraph code search. |
| `tool.diagnostics` | `internal/agent/tools/diagnostics.go` | LSP diagnostics. |
| `tool.references` | `internal/agent/tools/references.go` | LSP symbol references. |
| `tool.lsp_restart` | `internal/agent/tools/lsp_restart.go` | LSP server restart. |
| `tool.job_output` | `internal/agent/tools/job_output.go` | Background job output. |
| `tool.job_kill` | `internal/agent/tools/job_kill.go` | Background job kill. |
| `tool.phosphor_info` | `internal/agent/tools/phosphor_info.go` | Phosphor runtime info. |
| `tool.phosphor_logs` | `internal/agent/tools/phosphor_logs.go` | Phosphor log retrieval. |
| `tool.goal` | `internal/agent/tools/goal.go` | Goal management. |
| `tool.todos` | `internal/agent/tools/todos.go` | Todo list management. |
| `tool.mcp.*` | `internal/agent/tools/mcp-tools.go` | MCP tool calls. |
| `tool.read_mcp_resource` | `internal/agent/tools/read_mcp_resource.go` | MCP resource read. |
| `tool.list_mcp_resources` | `internal/agent/tools/list_mcp_resources.go` | MCP resource listing. |
| `tool.agentic_fetch` | `internal/agent/agentic_fetch_tool.go` | Agentic fetch tool. |

### Priority 3: Hooks (Medium Value)

| Span | Location | Description |
|---|---|---|
| `hook.run` | `internal/hooks/runner.go` — `Run()` | All hooks for a tool call. |
| `hook.run.one` | `internal/hooks/runner.go` — `runOne()` | Individual hook execution. |

### Priority 4: LSP Operations (Medium Value)

| Span | Location | Description |
|---|---|---|
| `lsp.init` | `internal/lsp/manager.go` — LSP initialization | LSP server startup and initialization. |
| `lsp.request` | `internal/lsp/client.go` — LSP request wrapper | Individual LSP protocol requests (diagnostics, references, etc.). |
| `lsp.notification` | `internal/lsp/client.go` — LSP notification handling | LSP notifications (publishDiagnostics, telemetry, etc.). |

### Priority 5: MCP Operations (Medium Value)

| Span | Location | Description |
|---|---|---|
| `mcp.init` | `internal/agent/tools/mcp/init.go` — MCP server initialization | MCP server startup and initialize handshake. |
| `mcp.tool_call` | `internal/agent/tools/mcp/tools.go` — MCP tool invocation | MCP server tool calls (delegated from agent tools). |
| `mcp.resource_read` | `internal/agent/tools/mcp/resources.go` — MCP resource read | MCP server resource reads. |
| `mcp.prompt_get` | `internal/agent/tools/mcp/prompts.go` — MCP prompt get | MCP server prompt retrieval. |

### Priority 6: Shell Execution (Medium Value)

| Span | Location | Description |
|---|---|---|
| `shell.exec` | `internal/shell/run.go` — `Run()` | Shell command execution (used by hooks and bash tool). |

### Priority 7: HTTP Server (Lower Value)

| Span | Location | Description |
|---|---|---|
| `http.request` | `internal/server/server.go` — request handler | Incoming HTTP requests to the Phosphor server. |

Already partially covered by `otelhttp` middleware (indirect dep via catwalk/fantasy), but Phosphor's own HTTP router could benefit from explicit spans.

### Priority 8: Database (Lower Value)

| Span | Location | Description |
|---|---|---|
| `db.query` | `internal/db/` — sqlc generated queries | Individual SQL queries. |
| `db.transaction` | `internal/db/` — transaction wrappers | Database transactions. |

### Priority 9: Config Loading (Low Value)

| Span | Location | Description |
|---|---|---|
| `config.load` | `internal/config/load.go` — config loading | Full config load cycle. |
| `config.resolve` | `internal/config/resolve.go` — config resolution | Provider/model resolution. |

## Implementation Plan

### Phase 1: OTel SDK Foundation

**Files to create/modify:**

- `internal/otel/otel.go` — OTel SDK initialization, tracer provider, span helpers
- `internal/config/config.go` — Add `Observability` struct and JSON schema
- `internal/app/app.go` — Wire OTel provider into app lifecycle

**Key code sketch:**

```go
// internal/otel/otel.go

package otel

import (
    "go.opentelemetry.io/otel"
    "go.opentelemetry.io/otel/exporters/otlp/otlptrace"
    "go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
    "go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
    "go.opentelemetry.io/otel/propagation"
    "go.opentelemetry.io/otel/sdk/resource"
    sdktrace "go.opentelemetry.io/otel/sdk/trace"
    semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

var tracer = otel.Tracer("github.com/hackafterdark/phosphor")

// Init creates and installs the global TracerProvider and Propagator.
// Returns a shutdown function that should be deferred.
func Init(cfg config.Observability) (func(context.Context) error, error) {
    if cfg.Endpoint == "" {
        // No endpoint configured — use no-op tracer.
        return func(ctx context.Context) error { return nil }, nil
    }

    res, err := resource.New(ctx,
        resource.WithFromEnv(),
        resource.WithProcess(),
        resource.WithHost(),
        resource.WithAttributes(
            semconv.ServiceName(cfg.ServiceName),
            semconv.ServiceVersion(version.Version),
        ),
    )
    if err != nil {
        return nil, fmt.Errorf("otel: create resource: %w", err)
    }

    var exporter *otlptrace.Exporter
    switch cfg.Protocol {
    case "http/protobuf":
        exporter, err = otlptracehttp.New(ctx, otlptracehttp.WithEndpoint(cfg.Endpoint))
    default: // grpc
        exporter, err = otlptracegrpc.New(ctx, otlptracegrpc.WithEndpoint(cfg.Endpoint))
    }
    if err != nil {
        return nil, fmt.Errorf("otel: create exporter: %w", err)
    }

    tp := sdktrace.NewTracerProvider(
        sdktrace.WithBatcher(exporter),
        sdktrace.WithResource(res),
        sdktrace.WithSampler(sdktrace.ParentBased(
            sdktrace.TraceIDRatioBased(cfg.SamplingRate),
        )),
    )

    otel.SetTracerProvider(tp)
    otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
        propagation.TraceContext{},
        propagation.Baggage{},
    ))

    return tp.Shutdown, nil
}

// Tracer returns the global Phosphor tracer.
func Tracer() trace.Tracer {
    return tracer
}
```

### Phase 2: Agent Turn Spans

**Files to modify:**

- `internal/agent/agent.go` — Add spans around `Run()`, LLM requests, tool calls
- `internal/agent/coordinator.go` — Add spans around `Run()` at coordinator level

**Span hierarchy:**

```
agent.turn (SessionAgent.Run)
├── agent.turn.prepare
│   ├── db.query (session message loading)
│   └── agent.turn.summarize (if triggered)
├── agent.turn.llm.request
│   ├── otelhttp (HTTP request to provider)
│   └── agent.turn.llm.response
├── tool.call (for each tool call)
│   ├── tool.bash
│   ├── tool.edit
│   └── ...
└── agent.turn.title (if first message)
```

**Key code sketch:**

```go
// In internal/agent/agent.go, inside SessionAgent.Run()

func (a *sessionAgent) Run(ctx context.Context, call SessionAgentCall) (*fantasy.AgentResult, error) {
    ctx, span := otel.Tracer("github.com/hackafterdark/phosphor").Start(ctx, "agent.turn")
    defer span.End()

    span.SetAttributes(
        attribute.String("agent.session_id", call.SessionID),
        attribute.String("agent.run_id", call.RunID),
    )
    defer func() {
        if retErr != nil {
            span.RecordError(retErr)
            span.SetStatus(codes.Error, retErr.Error())
        }
    }()

    // ... existing Run logic ...

    // LLM request span
    ctx, llmSpan := otel.Tracer("github.com/hackafterdark/phosphor").Start(ctx, "agent.turn.llm.request")
    // ... stream call ...
    llmSpan.End()

    return result, err
}
```

### Phase 3: Tool Call Spans

**Files to modify:**

- `internal/agent/hooked_tool.go` — Wrap tool execution with spans
- `internal/agent/tools/bash.go` — Add span for shell execution
- `internal/agent/tools/mcp-tools.go` — Add spans for MCP tool calls

**Key code sketch:**

```go
// In internal/agent/hooked_tool.go

func (h *hookedTool) Call(ctx context.Context, input string) (string, error) {
    ctx, span := otel.Tracer("github.com/hackafterdark/phosphor").Start(ctx, "tool.call")
    defer span.End()

    span.SetAttributes(
        attribute.String("tool.name", h.tool.Name),
        attribute.String("tool.session_id", tools.GetSessionFromContext(ctx)),
    )

    startTime := time.Now()
    defer func() {
        span.SetAttributes(attribute.Float64("tool.duration_ms", float64(time.Since(startTime).Milliseconds())))
    }()

    // ... existing hooked tool logic ...
}
```

### Phase 4: Hooks, LSP, MCP Spans

**Files to modify:**

- `internal/hooks/runner.go` — Add spans around hook execution
- `internal/lsp/client.go` — Add spans around LSP requests
- `internal/lsp/manager.go` — Add spans around LSP initialization
- `internal/agent/tools/mcp/*.go` — Add spans around MCP operations

### Phase 5: HTTP Server & Database Spans

**Files to modify:**

- `internal/server/server.go` — Add middleware for HTTP request spans
- `internal/db/` — Add spans around SQL queries (via sqlc hooks or wrapper)

### Phase 6: Metrics

**Files to create/modify:**

- `internal/otel/metrics.go` — OTel metrics setup
- `internal/agent/agent.go` — Record metrics for token usage, tool call counts
- `internal/config/config.go` — Optional metrics-specific config

**Metrics to emit:**

| Metric Type | Name | Description |
|---|---|---|
| Counter | `phosphor.tool_calls.total` | Total tool calls by name |
| Counter | `phosphor.llm_requests.total` | Total LLM API requests |
| Counter | `phosphor.llm_tokens.total` | Tokens used (in/out) |
| Histogram | `phosphor.tool_calls.duration` | Tool call latency |
| Histogram | `phosphor.llm_requests.duration` | LLM request latency |
| Histogram | `phosphor.agent_turn.duration` | Full agent turn latency |
| Counter | `phosphor.errors.total` | Errors by type |
| Counter | `phosphor.hooks.total` | Hook executions |
| Counter | `phosphor.lsp_requests.total` | LSP protocol requests |
| Counter | `phosphor.mcp_requests.total` | MCP server requests |

## Span Attributes

Every span should include these standard attributes:

| Attribute | Description |
|---|---|
| `service.name` | Set at resource level (from config) |
| `service.version` | Phosphor version |
| `agent.session_id` | Session ID for agent-related spans |
| `agent.run_id` | Run ID for correlating client requests |
| `tool.name` | Tool name for tool call spans |
| `llm.provider` | Provider name (openai, anthropic, etc.) |
| `llm.model` | Model ID |
| `hook.name` | Hook command name |
| `lsp.server` | LSP server name |
| `mcp.server` | MCP server name |

## Testing

### Unit Tests

1. **No-op test**: Verify that when no endpoint is configured, no OTel SDK is initialized and no network calls are made.
2. **Span creation test**: Verify that spans are created with correct names, attributes, and hierarchy.
3. **Context propagation test**: Verify that span context propagates through goroutines and async operations.

### Integration Tests

1. **Jaeger test**: Run Phosphor with a local Jaeger instance and verify traces appear in the Jaeger UI.
2. **Metrics test**: Verify that metrics are emitted and can be scraped by a Prometheus instance.
3. **Config test**: Verify that config changes take effect without restart (hot-reload of OTel config).

### Manual Testing

1. Run `phosphor run` with an OTel collector endpoint configured.
2. Open Jaeger UI and verify traces appear.
3. Verify span hierarchy matches the proposed structure.
4. Verify tool call latencies are captured accurately.
5. Verify error spans are recorded with correct status and error attributes.

## Performance Considerations

- **Batching**: OTel's `WithBatcher` option batches spans before sending, minimizing network overhead.
- **Sampling**: Default sampling rate of 1.0 (always sample) can be reduced for high-volume environments.
- **No-op mode**: When no endpoint is configured, the OTel SDK uses no-op implementations with minimal overhead.
- **Blocking**: Span creation is synchronous but lightweight (microseconds). Export is always async.
- **Memory**: Spans are batched and exported periodically. The batch size is configurable (default 512).

## Future Potential

### "Configurable Policy" for Instrumentation

Allow users to configure which areas are instrumented:

```json
{
  "observability": {
    "endpoint": "http://localhost:4317",
    "instrumentation": {
      "agent": true,
      "tools": true,
      "hooks": true,
      "lsp": false,
      "mcp": false,
      "shell": false,
      "http": true,
      "db": false
    }
  }
}
```

### "Phosphor Dashboard"

A built-in dashboard (TUI component) that displays:
- Current active spans
- Recent tool call latencies
- Token usage summary
- Error rates

### "Trace Export"

Allow users to export the current session's traces as a JSON file for debugging:

```bash
phosphor traces export --session <session-id> traces.json
```

### "Remote Debugging"

When a user reports a bug, they could enable trace export and share the trace data with the Charm team for debugging.

### "Performance Benchmarking"

Use OTel traces to automatically detect performance regressions in CI:
- Compare agent turn latencies across versions
- Detect tool call regressions
- Track LSP/MCP initialization times

### "Distributed Tracing for Multi-Process Phosphor"

As Phosphor evolves to support multiple processes (e.g., remote agents, distributed workspaces), OTel's context propagation enables end-to-end tracing across process boundaries.

## Charm Package Support

- **fantasy**: The fantasy library (LLM provider abstraction) already uses `otelhttp` for HTTP requests. Phosphor's own spans would complement these, providing the agent-level context that fantasy's provider-level spans lack.
- **bubbletea/v2**: No OTel integration. Phosphor would add spans around TUI event handling if desired.
- **catwalk**: Testing framework only, no runtime impact.
- **lipgloss/v2**: No OTel integration needed (pure rendering).
- **glamour/v2**: No OTel integration needed (markdown rendering).

None of the charm packages provide built-in OTel integration. Phosphor would be the first Charm CLI tool to expose OTel instrumentation to end users.

## Dependencies

New direct dependencies (OTel SDK):

```
go.opentelemetry.io/otel          v1.x
go.opentelemetry.io/otel/sdk      v1.x
go.opentelemetry.io/otel/trace    v1.x
go.opentelemetry.io/otel/exporters/otlp/otlptrace       v1.x
go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc  v1.x
go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp v1.x
go.opentelemetry.io/otel/sdk/metric    v1.x  (Phase 6)
```

Note: `go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp` is already an indirect dependency (via catwalk/fantasy), so the dependency footprint is minimal.

## Risks and Mitigations

| Risk | Mitigation |
|---|---|
| **Performance overhead** | No-op when disabled; batching; sampling; async export |
| **Network failures** | OTel SDK handles connection errors gracefully; falls back to in-memory buffer |
| **Sensitive data in traces** | Tool input/output may contain sensitive data; users can configure sampling or disable specific span types |
| **Dependency bloat** | OTel SDK is ~2MB; acceptable for a CLI tool with optional feature |
| **Config complexity** | Simple defaults (empty endpoint = disabled); env var overrides for power users |

## Conclusion

OpenTelemetry instrumentation would give Phosphor users and developers powerful observability into agent behavior, tool performance, and system health. The implementation is incremental — starting with agent turns and tool calls, then expanding to LSP, MCP, and HTTP spans. The feature is opt-in (disabled by default) and works with any OTel-compatible backend, giving users full control over their observability stack.
