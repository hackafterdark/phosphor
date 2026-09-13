# Mermaid Rendering Service

## Overview

The Mermaid Rendering Service stores diagram syntax in a `diagrams` table in the Phosphor SQLite database. The HTTP endpoint looks up diagrams by ID, returning a fully-rendered HTML page containing the SVG diagram. This allows the agent to provide short, clean URLs that open directly in a browser.

## Endpoint

```
GET /service/mermaid/render?id=<diagram_id>
```

## How It Works

1. The agent emits a ` ```mermaid ` code block in its response.
2. The TUI (`internal/ui/chat/assistant.go`) detects the block and calls `server.MermaidURL(syntax)` to store the syntax in the DB and get a URL with the diagram ID.
3. The URL is `http://127.0.0.1:<port>/service/mermaid/render?id=<id>`.
4. The server handler looks up the syntax from the DB by ID and renders the diagram.

## Query Parameters

| Parameter | Required | Default | Description |
|---|---|---|---|
| `id` | Yes | — | Diagram ID from the `diagrams` table |
| `theme` | No | `dark` | Theme: `default`, `dark`, `forest`, `neutral`, `black`, `base` |
| `width` | No | `800` | SVG width in pixels |
| `height` | No | `600` | SVG height in pixels |

## Example Requests

All examples use a diagram ID (e.g., `42`) that was previously stored in the DB:

```
GET http://127.0.0.1:8643/service/mermaid/render?id=42
GET http://127.0.0.1:8643/service/mermaid/render?id=42&theme=light&width=1024&height=768
```

## Response

The endpoint returns `text/html` — a self-contained HTML page with:

- The embedded `mermaid.min.js` library (~3.5MB, vendored)
- The Mermaid syntax in a `<pre class="mermaid">` block
- A `mermaid.run()` call that renders the SVG
- Pan & zoom controls (zoom buttons, scroll-to-zoom, drag-to-pan)
- A "Download SVG" button for client-side export

## Agent Usage

When the agent generates a diagram, it emits a ` ```mermaid ` block. The TUI automatically generates a clickable "View" link using the diagram ID.

```
Here is a flowchart of the system:

graph TD
    A[Client] --> B[API Gateway]
    B --> C[Service A]
    B --> D[Service B]

To view this diagram, open:
http://127.0.0.1:8643/service/mermaid/render?id=42
```

## Configuration

The endpoint runs on the same port as the OpenAI-compatible API. Configure via `phosphor.json`:

```json
{
  "services": {
    "openai-api": {
      "enabled": true,
      "port": 8643,
      "host": "127.0.0.1"
    }
  }
}
```

## Constraints

| Constraint | Value |
|---|---|
| Max syntax size | 64KB (65536 bytes) |
| External dependencies | None (mermaid.min.js is vendored) |
| Network required | No — fully self-contained |
| Auth | None (local-only, bound to 127.0.0.1) |

## Database Schema

Diagrams are stored in the `diagrams` table:

```sql
CREATE TABLE IF NOT EXISTS diagrams (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id TEXT NOT NULL,
    syntax TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE CASCADE
);
```

## Implementation Files

| File | Purpose |
|---|---|
| `internal/server/mermaid.go` | Echo handler and `MermaidURL` function |
| `internal/server/mermaid_embed.go` | `go:embed` directives |
| `internal/server/mermaid/mermaid.min.js` | Vendored Mermaid library |
| `internal/server/mermaid/mermaid.html` | HTML page template |
| `internal/platform/httpapi/service.go` | Route registration |
| `internal/ui/chat/assistant.go` | `mermaidViewLink` auto-link generation |
| `pkg/agent/templates/system.md.tpl` | Agent prompt instructions |
| `pkg/db/migrations/*_create_diagrams_table.sql` | DB migration |