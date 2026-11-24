# MCP (Model Context Protocol)

Attach MCP servers via stdio processes or HTTP/SSE endpoints:

```json
{
  "mcp": {
    "context7": {
      "command": "npx",
      "args": ["-y", "@upstash/context7-mcp"],
      "type": "stdio",
      "env": {}
    },
    "remote-tools": {
      "type": "http",
      "url": "http://localhost:3000/mcp",
      "headers": { "authorization": "Bearer $MY_TOKEN" },
      "timeout": 30,
      "enabled_tools": ["search-docs"],
      "disabled_tools": []
    }
  }
}
```

## Fields

| Field | Type | Description |
|-------|------|-------------|
| `type` | string | `"stdio"` (launch a local process) or `"http"` (connect to an endpoint) |
| `command` | string | Executable for `"stdio"` servers |
| `args` | string[] | Arguments for `"stdio"` servers |
| `env` | object | Environment variables for `"stdio"` servers |
| `url` | string | Endpoint URL for `"http"` servers |
| `headers` | object | HTTP headers (values support `$VAR`/`$(cmd)` expansion) |
| `timeout` | int | Timeout in seconds (unset = server default) |
| `disabled` | bool | `true` disables this server |
| `enabled_tools` | string[] | Whitelist of tools to expose (empty = all tools except disabled ones) |
| `disabled_tools` | string[] | Blacklist of tools to hide |

Header values support `$VAR`/`$(cmd)` expansion; a header that expands to an
empty string is omitted rather than sent blank.

## See Also

- [LSP & AST](LSP_AST.md) — Language Server Protocol and tree-sitter configuration
