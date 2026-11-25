# Security Configuration

The defense-in-depth dials: egress posture, secret/PII redaction on the wire
and at read time, forced read-only mode, and a tool blacklist:

```json
{
  "security": {
    "read_only": false,
    "disable_yolo": false,
    "tool_blacklist": ["bash"],
    "allowed_egress": { "http": true },
    "redact_outgoing_secrets": true,
    "redact_outgoing_pii": false,
    "redact_sensitive_files": true,
    "sensitive_file_patterns": ["secrets/**/*.json"],
    "tokenize_secrets": false
  }
}
```

## Fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `read_only` | bool | `false` | `true` forces read-only mode (agent cannot write) |
| `disable_yolo` | bool | `false` | `true` pins YOLO mode off for every workspace |
| `tool_blacklist` | string[] | `[]` | Tools that cannot be used |
| `allowed_egress` | object | `{}` | Network egress policy (see below) |
| `redact_outgoing_secrets` | bool | `true` | `true` redacts secrets from outgoing messages |
| `redact_outgoing_pii` | bool | `false` | `true` redacts PII from outgoing messages |
| `redact_sensitive_files` | bool | `true` | `true` redacts sensitive file content on read |
| `sensitive_file_patterns` | string[] | `[]` | Additional file patterns to redact |
| `tokenize_secrets` | bool | `false` | `true` tokenizes secrets (advanced) |

### `allowed_egress`

| Key | Type | Default | Effect |
|-----|------|---------|--------|
| `http` | bool | `false` | Allow HTTP(S) egress |
| `discord` | bool | `false` | Allow Discord egress |
| `slack` | bool | `false` | Allow Slack egress |
| `telegram` | bool | `false` | Allow Telegram egress |

## Notes

Defaults are already the secure positions (redaction on, tokenization off, PII
masking off because it false-positives on code). Don't loosen anything here
without reading
[security/CONFIGURATION.md](../security/CONFIGURATION.md) first.

`disable_yolo` is operator-only: set it to `true` in the global config to pin
YOLO mode off for every workspace. `--yolo`, ctrl+y, and headless clients
cannot enable permission-request skipping while it is pinned, workspace files
cannot unset it, and unattended cron jobs refuse to run while it is active.

## Related Docs

- [security/CONFIGURATION.md](../security/CONFIGURATION.md) — Every control and its config path
- [security/HARDENING.md](../security/HARDENING.md) — Hardening guide
- [security/SECRETS_PROTECTION.md](../security/SECRETS_PROTECTION.md) — Secrets protection
- [security/WORKSPACE_HARDENING.md](../security/WORKSPACE_HARDENING.md) — Workspace hardening
- [security/ENVIRONMENT_HARDENING.md](../security/ENVIRONMENT_HARDENING.md) — Environment hardening
- [security/NETWORK_EGRESS_HARDENING.md](../security/NETWORK_EGRESS_HARDENING.md) — Network egress
