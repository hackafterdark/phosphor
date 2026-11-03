# Logging Configuration

Phosphor's logging system is **disabled by default** (safe by default). Enable it explicitly in `phosphor.json` to capture session activity, tool usage, and debugging information.

## Overview

Logging captures:
- Session activity
- Tool calls and results
- LSP server status
- Agent reasoning steps
- Errors and warnings

**Privacy note:** Logs capture metadata (session IDs, token counts, tool names, timing) but NOT actual prompt content or message bodies. This is a privacy-by-design choice to prevent sensitive user input from appearing in log files.

### What IS Logged (slog file)

- Session IDs and timestamps
- Prompt length (character count, not content)
- Tool names (not arguments)
- Temperature, top_p, repetition_penalty values
- Token usage (input/output counts)
- LSP server names and status
- Error messages (may contain stack traces)

**Tool call arguments are NOT written to slog logs.**

### ⚠️ Known Limitation: OTel Traces

Tool call arguments ARE recorded in OpenTelemetry traces (`gen_ai.tool.call.arguments`). If you pass sensitive data (API keys, URLs with credentials, tokens) as tool arguments, that data WILL appear in your OTel trace storage if you have OpenTelemetry enabled.

**Recommendations:**
- Never pass API keys or secrets as tool arguments
- Use environment variables or config files for credentials
- Review OTel trace storage security (who can access it?)
- Consider filtering sensitive patterns in your OTel collector/exporter

Log files are written in JSON format (one entry per line) to `{data_directory}/logs/phosphor.log`.

## Configuration

Add a `logging` section to your `phosphor.json`:

```jsonc
{
  "logging": {
    "enabled": true,
    "level": "info",
    "max_size_mb": 10,
    "max_age_days": 30,
    "max_backups": 5,
    "compress": false,
    "filters": [
      {"field": "msg", "pattern": ".*password.*"},
      {"field": "msg", "pattern": ".*secret.*"}
    ],
    "source_levels": {
      "github.com/charmbracelet/x/powernap/pkg/transport": "info"
    }
  }
}
```

### Fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | `bool` | `false` | Enable application logging. **Default is `false`** — logging is off unless explicitly enabled. |
| `level` | `"off" \| "info" \| "debug"` | `"info"` | slog log level. `"off"` disables logging even if `enabled: true`. |
| `max_size_mb` | `int` | `10` | Maximum log file size in MB before rotation (lumberjack). Max 1000. |
| `max_age_days` | `int` | `30` | Maximum age of log files in days before cleanup. |
| `max_backups` | `int` | `0` | Maximum number of old rotated log files to retain. `0` (lumberjack default) keeps **all** old files until `max_age_days` expires; set a small number (e.g. `5`) to bound disk usage regardless of age. |
| `compress` | `bool` | `false` | Enable gzip compression of rotated log files. |
| `filters` | `LogFilter[]` | `[]` | Regex filters to exclude matching log entries. See [Filters](#filters). |
| `source_levels` | `map[string]string` | built-in defaults | Minimum log level per third-party source package prefix. See [Source Levels](#source-levels). |

## Log File Location

When enabled, logs are written to:

```
{data_directory}/logs/phosphor.log
```

The data directory is configured via `options.data_directory` (default: `.phosphor` in project root) or `--data-dir` CLI flag.

Example paths:
- Local project: `F:/hackafterdark/phosphor/.phosphor/logs/phosphor.log`
- Custom: `C:/Users/tomma/AppData/Local/phosphor/logs/phosphor.log`

## Viewing Logs

### CLI Command

Use the `phosphor logs` command to view recent entries:

```bash
# Show last 100 lines (default)
phosphor logs

# Show last 20 lines
phosphor logs --tail 20

# Follow new entries (like `tail -f`)
phosphor logs --follow
```

### Agent Tool

The `phosphor_logs` agent tool is available **only when logging is enabled**. It reads the last N log entries efficiently by seeking backwards (no need to load the entire file).

When `observability.memory_buffer` is set to a positive size, the `phosphor_trace` agent tool is also available. It reads the in-process ring of recent completed OTel spans (name, duration, nesting, status, key GenAI attributes), so the agent can inspect its own turn structure without a round-trip to the OTLP collector. The buffer works with or without an endpoint configured and costs on the order of tens of kilobytes for typical sizes (default 100, max 500 spans).

**Usage in Phosphor TUI:**
```
show me recent logs
use phosphor_logs with 10 lines
```

The tool returns formatted log entries suitable for debugging sessions.

**Note:** The tool is hidden when logging is disabled to prevent confusion (a tool that appears available but shows nothing).

## Redaction

### Slog Logs (`phosphor.log`)

The `phosphor_logs` agent tool and log output include basic redaction:
- Sensitive field names (authorization, api-key, token, secret, password) are masked
- Values matching sensitive patterns may be partially redacted

### OpenTelemetry Traces

OTel traces do NOT have the same redaction. Tool call arguments are recorded verbatim in `gen_ai.tool.call.arguments` attributes.

**Implications:**
- URLs with embedded credentials appear in traces
- API keys passed as arguments appear in traces
- Error messages with stack traces may contain sensitive data

**Mitigations:**
- Configure your OTel collector to filter/redact sensitive attributes
- Use environment variables instead of passing secrets as arguments
- Review trace storage access controls

## Filters

Filters exclude log entries whose fields match regex patterns. This is useful for redacting sensitive data (API keys, passwords, tokens) from log output.

### Syntax

```json
"filters": [
  {"field": "msg", "pattern": ".*password.*"},
  {"field": "msg", "pattern": ".*secret.*"},
  {"field": "api_key", "pattern": ".*"}
]
```

### Fields

- `field`: JSON key in the log entry to match against (e.g., `"msg"`, `"source"`, or any custom key)
- `pattern`: Regular expression matched against the field value (case-insensitive field matching)

### Examples

**Redact password-related messages:**
```json
{"field": "msg", "pattern": ".*password.*"}
```

**Exclude all API key values:**
```json
{"field": "api_key", "pattern": ".*"}
```

**Filter specific error types:**
```json
{"field": "msg", "pattern": "^Failed to create LSP client"}
```

## Source Levels

Third-party libraries log through the same slog logger, and some are
chatty at `debug` level — for example the powernap LSP transport logs
every message exchanged with gopls-style servers, which can add
megabytes per session. `source_levels` sets a **minimum level per
source package prefix**: records whose source lives under a gated
prefix are dropped unless they reach that level, while Phosphor's own
logging (and every other source) is unaffected.

Phosphor ships defaults that cap the LSP transport chatter:

```json
{
  "logging": {
    "source_levels": {
      "github.com/charmbracelet/x/powernap/pkg/transport": "info",
      "github.com/charmbracelet/x/powernap/pkg/lsp": "info"
    }
  }
}
```

Your `source_levels` entries are merged over these defaults, so you
can also **re-enable** a gated source when debugging it:

```json
{
  "logging": {
    "level": "debug",
    "source_levels": {
      "github.com/charmbracelet/x/powernap/pkg/transport": "debug"
    }
  }
}
```

Valid levels are `debug`, `info`, `warn`, and `error`.

## Log Rotation

Phosphor uses [lumberjack](https://github.com/natefinch/lumberjack) for log rotation:

- **MaxSize**: Maximum size of a single log file in MB before rotation
- **MaxAge**: Maximum number of days to retain old log files
- **MaxBackups**: Maximum number of old log files to retain (`0` keeps all old files until `max_age_days` prunes them)
- **Compress**: Enable gzip compression of rotated files

When the active log file exceeds `max_size_mb`, it's rotated and a new file is created. Old files are cleaned up after `max_age_days`.

## Security Considerations

### Safe by Default

Logging is **off by default**. The agent cannot read logs or send sensitive information to models unless you explicitly enable logging.

### Control What's Logged

Use `filters` to exclude sensitive data:
- API keys and tokens
- Passwords and credentials
- Personal information
- Internal error messages

### Agent Tool Access

The `phosphor_logs` tool is only available when logging is enabled. This prevents the agent from accessing logs that might contain sensitive information when logging is intentionally disabled.

### Log File Permissions

Log files are written to the data directory with standard file permissions. Ensure the data directory is not accessible to unauthorized users.

## Debug Level

Set `level: "debug"` for verbose logging useful during development:

```json
{
  "logging": {
    "enabled": true,
    "level": "debug"
  }
}
```

Debug level includes:
- All info-level entries
- Tool call arguments and results
- Provider API requests/responses (if applicable)
- Internal agent state changes

**Warning:** Debug logs may contain sensitive information. Use filters or disable logging in production.

## Troubleshooting

### No Logs Appearing

1. Verify `logging.enabled` is `true` in phosphor.json
2. Check log file exists: `{data_directory}/logs/phosphor.log`
3. Restart Phosphor after config changes (logging config is read at startup)

### Log File Empty

1. Logging may be disabled (check `logging.enabled`)
2. No activity occurred since last restart
3. Filters may be excluding all entries

### Large Log Files

1. Reduce `max_size_mb` (default: 10 MB)
2. Enable compression: `"compress": true`
3. Reduce `max_age_days` (default: 30 days)
4. Use filters to exclude verbose entries

## See Also

- [phosphor.json schema](https://raw.githubusercontent.com/hackafterdark/phosphor/main/schema.json) — Full config reference
- [Observability](./OBSERVABILITY.md) — OpenTelemetry tracing configuration
- [MODELS_AND_PROVIDERS_CONFIG.md](../MODELS_AND_PROVIDERS_CONFIG.md) — Model and provider settings
