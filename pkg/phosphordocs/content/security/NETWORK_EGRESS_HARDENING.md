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

## Bash Network Egress Policy

The "web_fetch" and "web_search" controls above do not govern arbitrary commands
started by the bash tool. The bash tool also ships an optional argv-based network
policy for built-in network commands such as "curl", "wget", "nc", "ssh", and
"scp".

"""json
{
  "tools": {
    "bash": {
      "network": {
        "enabled": true,
        "allowed_commands": ["curl", "wget"],
        "host_allowlist": [".github.com", "registry.npmjs.org"]
      }
    }
  }
}
"""

The policy is disabled by default. When it is absent or disabled, the built-in
network command deny list remains active. When it is enabled, commands listed in
"allowed_commands" may run if their destination argv hosts match "host_allowlist".
An empty "host_allowlist" allows any host that is not hard denied.

Hard-denied targets include RFC 1918 private ranges, IPv6 private equivalents,
loopback and unspecified addresses, link-local and metadata ranges, plus
"localhost", ".localhost", "metadata", and "metadata.google.internal". Those
targets cannot be allowed by configuration. User "tools.bash.banned_commands"
also override the policy.

This is not an OS packet filter. It checks expanded argv values after the
embedded shell has performed variable expansion. It does not intercept socket
syscalls, dynamically constructed destinations, URLs read from files, or child
processes spawned by allowed commands. OS network isolation, a brokered egress
proxy, or a network namespace remains the correct enforcement layer for stronger
guarantees.

## Security Properties

- **Fail-closed**: no network requests succeed until the agent confirms the domain is trusted
- **IP-block policy**: raw IP addresses are rejected for both IPv4 and IPv6 literal forms (e.g., `10.x.x.x`, `192.168.x.x`, `[::1]`, `[fd00::…]`) — FQDNs required
- **Resolved-address validation**: the same IP policy is enforced at dial time on the *resolved* socket address, so an approved hostname whose DNS answer rebinds into a private, link-local, or cloud-metadata range is refused at the socket (DNS-rebinding defense). Loopback stays allowed; `IPAllowList`/`AllowRawIPs` exempt like raw IPs; when an environment proxy is configured the operator's proxy is the egress decision and the strict path remains the egress broker tier
- **Opt-in overrides**: `AllowRawIPs` + `IPAllowList` (CIDR) allow raw IPs for local/network development
- **Layered trust**: workspace + global allow lists + session allow list + user prompt
- **OTel observability**: every allow/deny/event is captured as an OTel span with structured attributes
- **Cacheable**: user approvals persist across requests (in-memory cache) but are session-local
- **Shared transport**: both `web_fetch` and `web_search` share the same `*http.Client` → same `securityTransport`

## TLS Trust and CA Compromise

The IP-block policy and trust hierarchy govern *where* the agent may connect;
certificate validation governs *whether the peer is who it claims to be*. The
two layers are independent, and an AI agent depends on the second more than
ordinary software does: the agent carries provider API keys in every request,
fetches attacker-chosen URLs by design, and can download and execute code from
hosts it has never seen. A compromised certificate authority — one whose
intermediate keys leak and can sign certs for *any* domain — silently defeats
every hostname-level defense at once (allow lists, IP blocks, FQDN
requirements).

Phosphor performs no custom certificate handling. TLS verification is delegated
to the runtime trust store:

- **Windows / macOS**: the platform certificate store, so OS security updates
  and browser-grade revocation (CRLSets, OneCRL) cover CA compromise.
- **Linux**: Phosphor builds with `CGO_ENABLED=0`, so Go uses its compiled-in
  copy of the Mozilla root program. Keeping the Go toolchain patched —
  `go.mod` pins the minimum version — is therefore part of the security
  posture, not just a build detail.

Concretely: when an intermediate CA in a trusted root program is compromised,
the remedy is a toolchain/OS update, and users should update promptly the same
way they would for a browser.

## Files Modified

- `internal/agent/tools/web_fetch.go` — added security transport, allow lists, TUI prompt wiring
- `internal/agent/tools/web_search.go` — added sanitization, OTel tagging, random delay, nil client panic
- `internal/agent/coordinator.go` — added `tuiAllowPrompt` field and wiring
- `internal/agent/agentic_fetch_tool.go` — updated call site to pass `c.tuiAllowPrompt`

## Notes

- `go.mod` requires Go 1.27.3+ (patched trust store; see TLS Trust and CA Compromise above)
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

- [Phosphor AGENTS.md](https://github.com/hackafterdark/phosphor/blob/main/AGENTS.md)
- [Phosphor docs](https://github.com/hackafterdark/phosphor/tree/main/docs)
- [Phosphor code](https://github.com/hackafterdark/phosphor)

---
