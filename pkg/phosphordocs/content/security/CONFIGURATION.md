# Phosphor Security Configuration

This guide covers every security control that can be managed via the
`phosphor.json` configuration file and the `.phosphorignore` file.

## Table of Contents

- [Ignoring Files](#ignoring-files)
- [Permission Prompts](#permission-prompts)
  - [Allowing Tools](#allowing-tools)
  - [YOLO Mode](#yolo-mode)
- [Disabling Built-In Tools](#disabling-built-in-tools)
- [Disabling Skills](#disabling-skills)
- [MCP Server Security](#mcp-server-security)
  - [Disabling MCP Servers](#disabling-mcp-servers)
  - [Disabling MCP Tools](#disabling-mcp-tools)
  - [Redacting Sensitive MCP Results](#redacting-sensitive-mcp-results)
- [Provider Security](#provider-security)
  - [Disabling Providers](#disabling-providers)
  - [Locking Down Default Providers](#locking-down-default-providers)
  - [Disabling Provider Auto-Update](#disabling-provider-auto-update)
- [Observability Security](#observability-security)
- [Telemetry opt-out](#telemetry-opt-out)
- [Tool Limits and Timeouts](#tool-limits-and-timeouts)
- [Network Egress Hardening](#network-egress-hardening)
- [Hooks as Security Gateways](#hooks-as-security-gateways)

---

## Ignoring Files

Phosphor respects `.gitignore` files by default, but you can also create a
`.phosphorignore` file to specify additional files and directories that
Phosphor should ignore. This is useful for excluding files that you want in
version control but don't want Phosphor to consider when providing context.

The `.phosphorignore` file uses the same syntax as `.gitignore` and can be
placed in the root of your project or in subdirectories.

---

## Permission Prompts

By default, Phosphor will ask you for permission before running tool calls
that modify the filesystem or execute shell commands. You can control this
behavior via configuration or the `--yolo` CLI flag.

### Allowing Tools

If you'd like to allow certain tools to be executed without prompting you for
permissions, list them in `permissions.allowed_tools`:

```json
{
  "$schema": "https://github.com/hackafterdark/phosphor/blob/main/schema.json",
  "permissions": {
    "allowed_tools": [
      "view",
      "ls",
      "glob",
      "grep",
      "edit",
      "mcp_context7_get-library-doc"
    ]
  }
}
```

Tools listed here will execute without a permission prompt. All other tools
will still require approval. Use this with care — only whitelist read-only
or low-risk tools.

### YOLO Mode

You can skip **all** permission prompts entirely by running Phosphor with the
`--yolo` flag:

```bash
phosphor --yolo
```

Or via the `ctrl+y` keyboard shortcut to toggle it mid-session. Be very, very
careful with this feature — it disables every permission gate for the session.

> **Note:** YOLO mode is a session-level override set at startup. It cannot
> be toggled back on once the session begins in server mode (first-wins
> semantics). In the TUI, it can be toggled on and off with `ctrl+y`.

---

## Disabling Built-In Tools

If you'd like to prevent Phosphor from using certain built-in tools entirely,
disable them via the `options.disabled_tools` list. Disabled tools are
completely hidden from the agent — it will not know they exist.

```json
{
  "$schema": "https://github.com/hackafterdark/phosphor/blob/main/schema.json",
  "options": {
    "disabled_tools": ["bash", "sourcegraph"]
  }
}
```

To disable tools exposed by MCP servers, see [Disabling MCP Tools](#disabling-mcp-tools).

---

## Disabling Skills

If you'd like to prevent Phosphor from using certain skills entirely, disable
them via the `options.disabled_skills` list. Disabled skills are hidden from
the agent, including builtin skills and skills discovered from disk.

```json
{
  "$schema": "https://github.com/hackafterdark/phosphor/blob/main/schema.json",
  "options": {
    "disabled_skills": ["phosphor-config"]
  }
}
```

---

## MCP Server Security

MCP (Model Context Protocol) servers are user-installed extensions that
provide additional tools to the agent. Phosphor provides several controls
for managing MCP security.

### Disabling MCP Servers

To completely disable an MCP server and prevent any of its tools from being
available:

```json
{
  "$schema": "https://github.com/hackafterdark/phosphor/blob/main/schema.json",
  "mcp": {
    "context7": {
      "type": "stdio",
      "command": "npx",
      "args": ["-y", "@upstash/context7@latest"],
      "disabled": true
    }
  }
}
```

### Disabling MCP Tools

To disable specific tools from an MCP server while keeping the rest available,
use `disabled_tools`:

```json
{
  "$schema": "https://github.com/hackafterdark/phosphor/blob/main/schema.json",
  "mcp": {
    "context7": {
      "type": "stdio",
      "command": "npx",
      "args": ["-y", "@upstash/context7@latest"],
      "disabled_tools": ["get-library-doc"]
    }
  }
}
```

### Enabling Only Specific MCP Tools (Allow List)

To restrict an MCP server to only a subset of its tools, use `enabled_tools`
as an allow list:

```json
{
  "$schema": "https://github.com/hackafterdark/phosphor/blob/main/schema.json",
  "mcp": {
    "context7": {
      "type": "stdio",
      "command": "npx",
      "args": ["-y", "@upstash/context7@latest"],
      "enabled_tools": ["get-library-doc"]
    }
  }
}
```

### Redacting Sensitive MCP Results

To prevent sensitive data (credentials, secrets, tokens) from leaking into
OpenTelemetry traces, list MCP server names in
`observability.sensitive_mcp_servers`. Results from these servers will be
redacted in distributed tracing:

```json
{
  "$schema": "https://github.com/hackafterdark/phosphor/blob/main/schema.json",
  "observability": {
    "endpoint": "otel-collector:4317",
    "sensitive_mcp_servers": ["vault", "secret-manager"]
  }
}
```

---

## Provider Security

### Disabling Providers

To prevent a configured provider from being used:

```json
{
  "$schema": "https://github.com/hackafterdark/phosphor/blob/main/schema.json",
  "providers": {
    "openai": {
      "disable": true
    }
  }
}
```

### Locking Down Default Providers

By default, Phosphor merges your config with built-in default providers. To
prevent this and require fully explicit provider configuration:

```json
{
  "$schema": "https://github.com/hackafterdark/phosphor/blob/main/schema.json",
  "options": {
    "disable_default_providers": true
  }
}
```

When enabled, providers must be fully specified in the config file with
`base_url`, `models`, and `api_key` — no merging with defaults occurs.

### Disabling Provider Auto-Update

To prevent Phosphor from automatically fetching updated provider model lists:

```json
{
  "$schema": "https://github.com/hackafterdark/phosphor/blob/main/schema.json",
  "options": {
    "disable_provider_auto_update": true
  }
}
```

---

## Observability Security

Phosphor supports OpenTelemetry for distributed tracing. The following
controls help manage what data is exported:

```json
{
  "$schema": "https://github.com/hackafterdark/phosphor/blob/main/schema.json",
  "observability": {
    "endpoint": "otel-collector:4317",
    "service_name": "phosphor",
    "protocol": "grpc",
    "sampling_rate": 0.1,
    "resource_attributes": {
      "environment": "production"
    },
    "sensitive_mcp_servers": ["vault"]
  }
}
```

- **`endpoint`**: When empty, OTel is disabled (no-op). Set this to enable
  trace export.
- **`sampling_rate`**: Controls the fraction of traces exported (0.0–1.0).
  Lower values reduce data leakage risk at the cost of observability.
- **`sensitive_mcp_servers`**: MCP server names whose tool results are
  redacted from traces to prevent credential/secrets leakage.

---

## Tool Limits and Timeouts

You can constrain the scope of file-searching tools to limit the agent's
blast radius:

```json
{
  "$schema": "https://github.com/hackafterdark/phosphor/blob/main/schema.json",
  "tools": {
    "ls": {
      "max_depth": 10,
      "max_items": 100
    },
    "grep": {
      "timeout": "10s"
    }
  }
}
```

- **`tools.ls.max_depth`**: Maximum directory traversal depth for `ls` (default
  `0` = unlimited).
- **`tools.ls.max_items`**: Maximum number of items returned by `ls` (default
  `1000`).
- **`tools.grep.timeout`**: Timeout for grep operations (default `5s`).

---

## Network Egress Hardening

Phosphor's web-fetch and web-search tools operate under an **Adaptive Trust**
network firewall. Every outgoing HTTP request is intercepted by a
`securityTransport` (a custom `http.RoundTripper`) before execution, validated
against a layered trust hierarchy, and logged as an OTel span.

### Trust Hierarchy

When the transport intercepts a request, it checks in this order:

1. **In-memory allow list** — session-local, populated by the user prompt.
2. **Workspace-level allow list** — `.phosphor/allowed_domains.json` or
   `.local/.phosphor/allowed_domains.json`.
3. **Global-level allow list** —
   `~/.phosphor/allowed_domains.json` or
   `$XDG_CONFIG_HOME/phosphor/allowed_domains.json`.
4. **Fallback to user prompt** — the TUI shows
   *"WebFetch is requesting access to `[host]`. Allow this domain?"*
   - **Allow** → host added to in-memory allow list and request resumes.
   - **Deny** → request blocked; host added to a temporary deny list for the
     current session.

### Configuration: `cfg.Tools.WebFetch`

These fields let you override the IP-block policy:

| Field | Type | Default | Description |
|---|---|---|---|
| `IPAllowList` | `[]string` | `[]` | CIDR or literal IPs allowed to be reached |
| `AllowRawIPs` | `bool` | `false` | Opt-in for raw IP access (default: FQDN required) |

```json
{
  "$schema": "https://github.com/hackafterdark/phosphor/blob/main/schema.json",
  "tools": {
    "web_fetch": {
      "ip_allow_list": [
        "192.168.0.0/24",
        "10.0.0.0/8",
        "127.0.0.1"
      ],
      "allow_raw_ips": true
    }
  }
}
```

**How it works** when `securityTransport` intercepts a raw IP request:

| Source | Behavior |
|---|---|
| 127.0.0.1 / ::1 (localhost) | Always allowed, no config needed. |
| `IPAllowList` | Literal match or CIDR match via `matchCIDR`. |
| `AllowRawIPs = true` | Allowed (if no CIDR match). |
| No match | TUI prompt: *"WebFetch is requesting access to `[host]`. Allow this domain?"* |

### Security Properties

| Property | Description |
|---|---|
| Fail-closed | No network requests succeed until the agent confirms the domain is trusted. |
| IP-block policy | Raw IP addresses are rejected — FQDNs required. |
| Opt-in overrides | `AllowRawIPs` + `IPAllowList` (CIDR) allow raw IPs for local/network development. |
| Layered trust | Workspace + global allow lists + session allow list + user prompt. |
| OTel observability | Every allow/deny/event is captured as an OTel span with structured attributes. |
| Cacheable | User approvals persist across requests (in-memory cache), but are session-local. |
| Shared transport | Both `web_fetch` and `web_search` share the same `*http.Client` → same `securityTransport`. |

### Allow-list File Formats

Both allow-list files are plain JSON arrays of domain strings. Case-insensitive
matching via `strings.EqualFold`.

**Global allow list** (`~/.phosphor/allowed_domains.json`)

```json
[
  "github.com",
  "npmjs.com",
  "pkg.go.dev"
]
```

**Workspace allow list** (project-local)

```json
[
  "api.internal.dev",
  "staging.example.com",
  "docs.company.local"
]
```

### Files Modified

- `internal/agent/tools/web_fetch.go` — security transport, allow lists, TUI prompt wiring.
- `internal/agent/tools/web_search.go` — sanitization, OTel tagging, random delay, nil-client panic.
- `internal/agent/coordinator.go` — `tuiAllowPrompt` field and wiring.
- `internal/agent/agentic_fetch_tool.go` — call site updated to pass `c.tuiAllowPrompt`.

---

## Hooks as Security Gateways

Phosphor hooks run user-defined shell commands before tool execution. They
can be used as security gateways to inspect, modify, or block tool calls:

```json
{
  "$schema": "https://github.com/hackafterdark/phosphor/blob/main/schema.json",
  "hooks": {
    "PreToolUse": [
      {
        "name": "block-bash",
        "matcher": "^bash$",
        "command": "echo 'bash is not allowed'"
      }
    ]
  }
}
```

- **`matcher`**: Regex pattern tested against the tool name. Empty means match
  all tools.
- **`timeout`**: Timeout in seconds for the hook command (default `30`).

Hooks run before permission checks and can return decisions to allow, deny, or
rewrite tool inputs. See [HOOKS.md](./HOOKS.md) for the full hook protocol.

---

## Summary Reference

| Control | Config Path | Type | Default |
|---------|-------------|------|---------|
| Ignore files | `.phosphorignore` | File | Respects `.gitignore` |
| Tool permission prompts | `permissions.allowed_tools` | Array | All tools prompt |
| Skip all permissions | CLI flag `--yolo` / `ctrl+y` | Flag | Off |
| Disable built-in tools | `options.disabled_tools` | Array | None |
| Disable skills | `options.disabled_skills` | Array | None |
| Disable agents | `agents.<name>.disabled` | Bool | False |
| Per-agent tool allow-list | `agents.<name>.allowed_tools` | Array | All tools |
| Per-agent MCP access | `agents.<name>.allowed_mcp` | Map | All MCPs |
| Disable MCP servers | `mcp.<name>.disabled` | Bool | False |
| Disable MCP tools | `mcp.<name>.disabled_tools` | Array | None |
| MCP tool allow-list | `mcp.<name>.enabled_tools` | Array | All tools |
| Redact MCP results | `observability.sensitive_mcp_servers` | Array | None |
| Disable providers | `providers.<name>.disable` | Bool | False |
| Lock down defaults | `options.disable_default_providers` | Bool | False |
| Disable provider updates | `options.disable_provider_auto_update` | Bool | False |
| LS depth limit | `tools.ls.max_depth` | Int | Unlimited |
| LS item limit | `tools.ls.max_items` | Int | 1000 |
| Grep timeout | `tools.grep.timeout` | Duration | 5s |
| Network egress hardening | `tools.web_fetch.ip_allow_list` + `allow_raw_ips` | Array, Bool | False (FQDN required) |
| Hook security gates | `hooks.<event>` | Array | None |
| OTel sampling rate | `observability.sampling_rate` | Float | 1.0 |
| OTel endpoint | `observability.endpoint` | String | Empty (disabled) |
