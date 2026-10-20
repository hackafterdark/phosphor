# memorygraph

Visualization of the phosphor agent memory store (`.phosphor/memory/.phosphor-index/memory.db`).
The builder reads the SQLite vault read-only and clusters entries by thread and tag.

## Usage

Live view (polling server — the page self-refreshes as memory grows):

```
python build_graph.py --serve
# open http://127.0.0.1:8642/view.html
```

Portable snapshot report (single self-contained HTML file, data embedded):

```
python build_graph.py --report report.html
```

The report opens offline with no server and no polling; ship it as-is.

Plain static mode (one-shot load, no polling):

```
python build_graph.py            # writes graph.json
python -m http.server 8642       # then open view.html
```

All modes accept an explicit `memory.db` path as the first argument; otherwise
the vault is auto-discovered by walking up from the current directory and
checking the usual per-user phosphor data locations.

## Notes

- The viewer renders with cosmos.gl 3.5.1 (GPU force graph) via CDN; only
  thread/tag hub nodes carry text labels.
- `graph.json` and `report*.html` hold raw memory entries and are git-ignored.
- The viewer ships with the phosphor binary: run `phosphor memorygraph` to
  start its own server (default `http://127.0.0.1:8644/view.html`),
  independent of the OpenAI API service. Host and port are configurable via
  the `memory-graph` service entry in phosphor.json. `phosphor memorygraph
  --report report.html` writes a self-contained snapshot report that opens
  offline. This Python builder remains for offline/standalone use.
- Future: add a JS chat app on top of the same web surface so the web GUI
  is an alternative front-end to the TUI.