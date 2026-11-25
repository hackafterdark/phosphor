# Services

Optional always-on platform services: an OpenAI-compatible HTTP API that
fronts the agent (which also hosts the Mermaid diagram viewer), the memory
vault viewer, and the cron scheduler for unattended runs.

```json
{
  "services": {
    "openai-api": {
      "enabled": true,
      "host": "127.0.0.1",
      "port": 8643,
      "auth": { "type": "bearer", "key": "$PHOSPHOR_API_TOKEN" }
    },
    "memory-graph": { "enabled": true, "host": "127.0.0.1", "port": 8644 },
    "cron": { "enabled": true, "jobs_directory": ".phosphor/jobs" }
  }
}
```

## `openai-api`

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `false` | Enable the API service |
| `host` | string | `"127.0.0.1"` | Bind address |
| `port` | int | `8643` | Listen port |
| `auth.type` | string | — | Authentication type; `"bearer"` is the only supported value |
| `auth.key` | string | — | Bearer token (supports `$VAR`); required when `auth.type` is `"bearer"` |

## `memory-graph`

The memory vault viewer started with `phosphor memorygraph`. It is a
standalone foreground server on its own port — it does not depend on the
OpenAI API service.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `false` | Enable the viewer |
| `host` | string | `"127.0.0.1"` | Bind address (loopback only) |
| `port` | int | `8644` | Listen port |

The viewer serves raw memory entries over plain HTTP, so it only accepts
loopback hosts, and the command refuses to start when the entry sets
`"enabled": false` (or omits it).

## `cron`

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `false` | Enable the cron scheduler |
| `jobs_directory` | string | `".phosphor/jobs"` | Directory containing job files |

## Mermaid diagram viewer

The Mermaid viewer is **not a separately-configurable service**. It is an HTTP
endpoint (`/service/mermaid/render`) hosted by the HTTP API server, so it is
available only while the OpenAI API service is running:

```json
{
  "services": {
    "openai-api": { "enabled": true, "host": "127.0.0.1", "port": 8643 }
  }
}
```

The TUI emits a `View Diagram` link for every ` ```mermaid ` block and points
it at `http://127.0.0.1:<port>/service/mermaid/render?id=<id>` on the API
port (default `8643`). The link is only reachable when `openai-api` is enabled
and listening; with the API disabled there is no server to handle the request.
Note that this route shares the API's bearer auth: if `openai-api` is
configured with `auth.type: "bearer"`, the browser cannot supply the required
`Authorization` header and the link returns `401`. The link is only usable
directly when no bearer key is configured. There is no `services.mermaid` (or
equivalent) config entry — control it through `services.openai-api`.

## Related Docs

- [platform/openai-api.md](../platform/openai-api.md) — OpenAI API reference
- [platform/SCHEDULED_JOBS.md](../platform/SCHEDULED_JOBS.md) — Scheduled jobs
