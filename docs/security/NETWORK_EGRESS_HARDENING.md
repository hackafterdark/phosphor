# Network Egress Hardening

Phosphor's web-fetch and web-search tools now operate under an **Adaptive Trust** network firewall. Every outgoing HTTP request is intercepted by a `securityTransport` before execution, validated against a layered trust hierarchy, and logged as an OTel span.

## Architecture

```
┌─────────────────────────────────────────────────────────┐
│  WebFetch / WebSearch tool                              │
│  (fantasy.AgentTool closure)                            │
└────────────────────┬────────────────────────────────────┘
                     │ uses shared *http.Client
                     ▼
┌─────────────────────────────────────────────────────────┐
│  securityTransport (http.RoundTripper)                  │
│  ┌─────────────────────────────────────────────────┐    │
│  │  1. IP-block policy                             │    │
│  │     net.ParseIP → reject if raw IP              │    │
│  │  2. Layered trust hierarchy                     │    │
│  │     session allow list → workspace allow list   │    │
│  │     → global allow list → TUI prompt            │    │
│  │  3. OTel span + attributes                      │    │
│  └─────────────────────────────────────────────────┘    │
└────────────────────┬────────────────────────────────────┘
                     │ delegates to inner transport
                     ▼
┌─────────────────────────────────────────────────────────┐
│  http.DefaultTransport (or user-provided)               │
└─────────────────────────────────────────────────────────┘
```

## Components

### `internal/agent/tools/web_fetch.go`

- `securityTransport` — custom `http.RoundTripper` that intercepts each request
- `allowList` — in-memory session allow cache (cleared on session restart)
- `stripPort` — helper that removes `:port` from host (preserves IPv6 brackets)
- `globalAllowPath` — resolves `~/.phosphor/allowed_domains.json` or `$XDG_CONFIG_HOME/phosphor/allowed_domains.json`
- `workspaceAllowPaths` — checks `.phosphor/allowed_domains.json` and `.local/.phosphor/allowed_domains.json`
- `jsonUnmarshal` — tiny JSON helper for allow list files
- `FetchURLAndConvertWithTrust` — wraps `FetchURLAndConvert` with the trust check
- `NewWebFetchTool` — constructs tool with `allowFn` callback (TUI permission prompt)

### `internal/agent/tools/web_search.go`

- `NewWebSearchTool` — same security contract as `web_fetch`; panics if `client` is nil
- `sanitizeSearchResults` — regex scrubber redacts:
  - High-entropy keys (`sk_*`, `APIKEY_*`, 40+ alphanumeric) → partial redaction
  - Raw IP literals (IPv4 / IPv6) → `[REDACTED IP]`
- `randomDelay` — adds 100–150ms random delay per round-trip to stay under DDG's request-budget threshold
- OTel span tagged with `attribute.Int("gen_ai.tool.web_search.results", len(results))`

### `internal/agent/coordinator.go`

- `tuiAllowPrompt` field added to `coordinator` struct
- Wired in `NewCoordinator` via `c.permissions.Request` with synthetic tool-call ID (`tui:allow:<host>`)
- The TUI prompt shows "WebFetch is requesting access to [host]. Allow this domain?" and returns (allowed, nil) or (denied, nil)
- Auto-approved sessions skip the prompt via `c.permissions.SkipRequests()`

## Trust Hierarchy (The Hierarchy of Trust)

When `securityTransport` intercepts a request for `api.internal.dev`, it checks in this order:

1. **In-memory allow list** — session-local, populated by user prompt
2. **Workspace-level allow list** — `.phosphor/allowed_domains.json` or `.local/.phosphor/allowed_domains.json`
3. **Global-level allow list** — `~/.phosphor/allowed_domains.json` or `$XDG_CONFIG_HOME/phosphor/allowed_domains.json`
4. **Fallback to user prompt** — TUI shows "WebFetch is requesting access to [host]. Allow this domain?"
   - If **Allow** → host added to in-memory allow list and request resumes
   - If **Deny** → request blocked and host added to temporary deny list for current session

## File Formats

### Global allow list (`~/.phosphor/allowed_domains.json`)

```json
[
  "github.com",
  "npmjs.com",
  "pkg.go.dev"
]
```

### Workspace allow list (project-local)

```json
[
  "api.internal.dev",
  "staging.example.com",
  "docs.company.local"
]
```

Both files are plain JSON arrays of domain strings. Case-insensitive matching via `strings.EqualFold`.

## Configuration: Web Fetch Tool

Phosphor's `web_fetch` and `web_search` tools consult the user's `phosphor.json` for opt-in overrides to the IP-block policy.

### `cfg.Tools.WebFetch`

| Field | Type | Default | Description |
|---|---|---|---|
| `IPAllowList` | `[]string` | `[]` | CIDR or literal IPs allowed to be reached |
| `AllowRawIPs` | `bool` | `false` | Opt-in for raw IP access (default: FQDN required) |

### Example `phosphor.json`

```json
{
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

### How it works

When `securityTransport` intercepts a request to a raw IP:

1. **127.0.0.1 / ::1** (localhost) → always allowed, no config needed
2. **`IPAllowList`** → literal match or CIDR match via `matchCIDR` → allowed
3. **`AllowRawIPs = true`** → allowed (if no CIDR match)
4. **No match** → TUI prompt shows "WebFetch is requesting access to [host]. Allow this domain?"

## Security Properties

- **Fail-closed**: no network requests succeed until the agent confirms the domain is trusted
- **IP-block policy**: raw IP addresses (e.g., `10.x.x.x`, `192.168.x.x`, public IPs) are rejected — FQDNs required
- **Opt-in overrides**: `AllowRawIPs` + `IPAllowList` (CIDR) allow raw IPs for local/network development
- **Layered trust**: workspace + global allow lists + session allow list + user prompt
- **OTel observability**: every allow/deny/event is captured as an OTel span with structured attributes
- **Cacheable**: user approvals persist across requests (in-memory cache) but are session-local
- **Shared transport**: both `web_fetch` and `web_search` share the same `*http.Client` → same `securityTransport`

- **Fail-closed**: no network requests succeed until the agent confirms the domain is trusted
- **IP-block policy**: raw IP addresses (e.g., `10.x.x.x`, `192.168.x.x`, public IPs) are rejected — FQDNs required
- **Layered trust**: workspace + global allow lists + session allow list + user prompt
- **OTel observability**: every allow/deny/event is captured as an OTel span with structured attributes
- **Cacheable**: user approvals persist across requests (in-memory cache) but are session-local
- **Shared transport**: both `web_fetch` and `web_search` share the same `*http.Client` → same `securityTransport`

## Files Modified

- `internal/agent/tools/web_fetch.go` — added security transport, allow lists, TUI prompt wiring
- `internal/agent/tools/web_search.go` — added sanitization, OTel tagging, random delay, nil client panic
- `internal/agent/coordinator.go` — added `tuiAllowPrompt` field and wiring
- `internal/agent/agentic_fetch_tool.go` — updated call site to pass `c.tuiAllowPrompt`

## Notes

- `go.mod` requires Go 1.26.4+ (running 1.25.0 locally — LSP errors may appear for tooling checks)
- `fmt`, `net`, `strings`, `html/template`, `encoding/json`, `time`, `embed` imports are all used in body
- `maybeDelaySearch` removed from `web_search.go` (already present in `search.go`)
- `randomDelay` added for DDG request-budget threshold

## Future Improvements

- Persist session allow list to disk (opt-in)
- Support for deny-list files (`~/.phosphor/denied_domains.json`)
- Add `c.tuiAllowPrompt` to other tools (`download`, `sourcegraph`, `view`)
- Allow `c.tuiAllowPrompt` to be configured via `phosphor.json`
- Add `go.mod` upgrade path to 1.26.4 (currently using 1.25.0)
- Add `go.sum` entries for new dependencies (`encoding/json` is stdlib — no need)
- Add `go.work` file for multi-module setup (optional)
- Add `go.mod` documentation for new contributors (optional)

## References

- [Phosphor AGENTS.md](F:/hackafterdark/phosphor/AGENTS.md)
- [Phosphor docs](F:/hackafterdark/phosphor/docs/)
- [Phosphor code](F:/hackafterdark/phosphor/)

## Generated with Phosphor

💘 Generated with Phosphor

---
