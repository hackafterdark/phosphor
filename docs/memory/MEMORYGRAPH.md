# Memory Graph Viewer

`phosphor memorygraph` visualizes the memory vault as an interactive 2D
constellation. Memory entries are nodes colored by type; threads and tags
become hub nodes that cluster related entries together.

## Run the live viewer

```bash
phosphor memorygraph
```

The command starts a local HTTP server and prints the viewer URL:

```
Phosphor Memory Graph Viewer
============================
Open http://127.0.0.1:8644/view.html

Press Ctrl+C to stop.
```

The viewer polls the graph data endpoint every 10 seconds, so new memory
entries appear without restarting. Press `Ctrl+C` to stop the server.

## Export a snapshot report

```bash
phosphor memorygraph --report report.html
```

This writes a single self-contained HTML file that embeds the current graph
payload and opens offline with no server or polling. The exported report is
static: it shows the graph as it existed at export time.

## Host and port

The viewer defaults to `127.0.0.1:8644`. You can override it in
`phosphor.json`:

```json
{
  "services": {
    "memory-graph": {
      "host": "127.0.0.1",
      "port": 8644
    }
  }
}
```

The viewer uses its own port so it works regardless of whether the
OpenAI-compatible API service is enabled.

## What the viewer shows

- **Nodes**: memory entries (decisions, constraints, references, etc.) plus
  thread and tag hubs.
- **Edges**: relationships such as `supersedes`, `decision`, `thread`, and
  `tag`.
- **Side panel**: click a node to view its id, type, trust, recall, dates,
  tags, body, and linked nodes.
- **Search**: press `/` to focus the search box, type to filter nodes, and
  press `Enter` to jump to the top-scoring match.

## Related docs

- [Memory Overview](OVERVIEW.md) — how the memory vault and index work.
- [CLI Reference](CLI.md) — global flags and all commands.