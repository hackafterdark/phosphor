Search the deferred tool registry for tools whose schemas are not currently advertised to you. MCP server tools are deferred by default so the prompt stays small regardless of how many servers are connected; their schemas are loaded on demand after a successful lookup.

Query modes:

- Fuzzy lookup: pass plain keywords (e.g. `query: "query sentry issues"`). Candidates are ranked with BM25 over each tool's name and description.
- Exact lookup: prefix the query with `select:` and list tool names verbatim, comma-separated (e.g. `query: "select:mcp_sentry_get_issue,mcp_sentry_search_events"`). Exact lookups never fall back to fuzzy ranking.

Promoted tools stay available for the rest of the session: once `tool_search` returns a tool, its full schema is advertised on the next step and you can call it directly. If a discovered tool later changes server-side (schema drift or the server dropped it), calls to it are rejected with a re-discovery instruction — run `tool_search` again to load the current schema.

The lookup is entirely client-side: MCP servers never need to implement or advertise `tool_search` themselves. Servers that expose no tools at all are unaffected.
