# Observability & Logging

## Observability (OpenTelemetry)

Export traces/metrics over OTLP:

```json
{
  "observability": {
    "endpoint": "http://localhost:4317",
    "protocol": "grpc",
    "service_name": "phosphor",
    "sampling_rate": 1.0,
    "resource_attributes": { "team": "platform" },
    "sensitive_mcp_servers": ["vault"],
    "capture_input_messages": false,
    "capture_output_messages": false
  }
}
```

| Field | Type | Description |
|-------|------|-------------|
| `endpoint` | string | OTLP collector endpoint |
| `protocol` | string | `"grpc"` or `"http"` |
| `service_name` | string | Service identifier in traces |
| `sampling_rate` | float | Trace sampling rate (0.0–1.0) |
| `resource_attributes` | object | Additional resource attributes |
| `sensitive_mcp_servers` | string[] | MCP servers whose data is redacted |
| `capture_input_messages` | bool | `false` | `true` records full prompts in traces |
| `capture_output_messages` | bool | `false` | `true` records full responses in traces |

Message capture is opt-in because it puts full prompts and responses in your
trace backend.

## Logging

Logging is off unless you ask for it:

```json
{
  "logging": {
    "enabled": true,
    "level": "debug",
    "max_size_mb": 10,
    "max_age_days": 7,
    "max_backups": 3,
    "compress": true,
    "filters": [{ "field": "msg", "pattern": ".*heartbeat.*" }]
  }
}
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `false` | `true` turns on application logging |
| `level` | string | `"info"` | Log level: `"off"` (silence), `"info"` (normal), `"debug"` (verbose) |
| `max_size_mb` | int | `10` | Max log file size (MB) before rotation |
| `max_age_days` | int | `30` | Max age of log files in days |
| `max_backups` | int | `0` | Max rotated files to retain (`0` keeps all until `max_age_days` expires) |
| `compress` | bool | `false` | `true` gzips rotated logs |
| `filters` | array | `[]` | Regex filters to exclude entries |

The file lands in `<data_directory>/logs/phosphor.log`.

(`options.debug` turns on debug logging too; `options.debug_lsp` adds LSP
wire traffic.)

## Related Docs

- [observability/OPEN_TELEMETRY.md](../observability/OPEN_TELEMETRY.md) — OTel setup
- [observability/LOGGING.md](../observability/LOGGING.md) — Full logging reference
