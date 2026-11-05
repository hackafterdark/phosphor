# Tool Search (Deferred MCP Tool Schemas)

`tool_search` keeps MCP tool schemas out of the prompt until the agent
actually needs them. With several MCP servers connected, their tool schemas
dominate the fixed prompt cost of every request; deferring them keeps the
prompt small, cheaper, and less confusing regardless of server count.

This feature is **client-side only**: MCP servers advertise their tools
normally and never need to implement or know about `tool_search`. Servers
that provide no tools, or that predate this pattern entirely, work
unchanged.

## Enabling

```json
{
  "options": {
    "agent": {
      "tool_search": {
        "enabled": true
      }
    }
  }
}
```

When enabled, every MCP tool schema is removed from the advertised tool list
and the `tool_search` tool is added instead. The built-in tools (view, edit,
bash, grep, ...) are always advertised directly — deferral applies to MCP
tools only.

## Pinning tools with `keep_loaded`

Some MCP tools are used on nearly every turn and shouldn't pay the discovery
round trip. Pin them by full tool name or by server name:

```json
{
  "options": {
    "agent": {
      "tool_search": {
        "enabled": true,
        "keep_loaded": ["mcp_sentry_get_issue", "my_other_server"]
      }
    }
  }
}
```

Entries matching a full tool name pin one tool; entries matching an MCP
server name pin every tool from that server. This mirrors the
`_meta["anthropic/alwaysLoad"]` pinning behavior other harnesses support.

## How discovery works

The agent discovers deferred tools on demand:

1. **Fuzzy lookup** — `tool_search` with plain keywords
   (`"query sentry issues"`). Candidates are ranked with BM25 over each
   tool's name and description; the name carries double weight so
   capability keywords in tool names dominate.
2. **Exact lookup** — `select:<tool_name>` (comma-separated for several).
   Accepts the full wrapped name (`mcp_sentry_get_issue`) or the raw MCP
   tool name (`get_issue`).

A successful lookup returns the full parameter schema of each match and
promotes the tools into the live tool list. Promoted tools stay advertised
for the rest of the session — the model calls them directly from the next
step onward. If the same tool is looked up again, the promotion is
idempotent.

## Schema-fingerprint guard

Every deferred tool carries a fingerprint of its advertised schema (name,
description, and parameter structure). When a promoted tool is called, the
guard re-validates the fingerprint against the live MCP tool state:

- **Schema drift** (the server changed the tool's description or
  parameters after discovery): the call is rejected with a re-discovery
  instruction — run `tool_search` again to load the current schema.
- **Tool removed** (server dropped it or permissions changed): the call is
  rejected the same way.

This prevents the model from invoking a tool with arguments that no longer
match its contract.

## Interaction with agent permissions

The deferred registry only contains tools the agent is already allowed to
use (`allowed_mcp` filters apply before deferral). Tools pinned via
`keep_loaded` skip the registry entirely and behave exactly as before.
Permission prompts, hooks, and the coercion wrapper apply to promoted tools
identically to directly advertised ones — deferred tools run through the
same wrapper chain.

## Compatibility

| Server behavior | Result |
|---|---|
| Server advertises tools normally | Deferral and discovery work unchanged |
| Server provides no tools | Nothing is deferred; no `tool_search` tool is advertised |
| Server refreshes its tool list mid-session | The live resolver re-reads current schemas; drift is caught by the guard on the next call |
| Harness with its own native tool search | Unaffected — each side defers independently |
