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
- [Secret Redaction](#secret-redaction)

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

- `pkg/agent/tools/web_fetch.go` — security transport, allow lists, TUI prompt wiring.
- `pkg/agent/tools/web_search.go` — sanitization, OTel tagging, random delay, nil-client panic.
- `pkg/agent/coordinator.go` — `tuiAllowPrompt` field and wiring.
- `pkg/agent/agentic_fetch_tool.go` — call site updated to pass `c.tuiAllowPrompt`.

### Bash Network Egress Policy

The bash tool blocks network utilities by default. An optional opt-in policy
can allow selected command argv targets without disabling the rest of the shell
deny list.

```json
{
  "$schema": "https://github.com/hackafterdark/phosphor/blob/main/schema.json",
  "tools": {
    "bash": {
      "network": {
        "enabled": true,
        "allowed_commands": ["curl", "wget", "nc"],
        "host_allowlist": [".github.com", "registry.npmjs.org", "10.0.0.0/8"]
      }
    }
  }
}
```

| Field | Type | Default | Description |
|---|---|---|---|
| `enabled` | `bool` | `false` | Enables the network egress policy for bash commands. |
| `allowed_commands` | `[]string` | `[]` | Built-in network commands permitted when `enabled` is `true`. Empty allows all built-in network commands. |
| `host_allowlist` | `[]string` | `[]` | Hostnames, FQDN suffixes, IPs, or CIDR ranges permitted as outbound destinations. Empty permits any host that is not hard denied. |

Supported host patterns:

- Exact host or IP: `github.com`, `127.0.0.1`
- FQDN suffix: `.github.com`, `*.github.com`
- CIDR range: `10.0.0.0/8`, `fd00::/8`

Hard-denied targets cannot be overridden by `host_allowlist`:

- RFC 1918 private ranges and IPv6 private equivalents
- Loopback and unspecified addresses
- Link-local and metadata ranges including `169.254.0.0/16`
- `localhost`, `.localhost`, `metadata`, and `metadata.google.internal`

When `tools.bash.network` is absent or disabled, known network commands remain
blocked. User `tools.bash.banned_commands` always win over the network policy.
Commands outside the network command list are unaffected unless they are added
to `allowed_commands`; when they are added, destination arguments are checked
with the same host policy.

The policy is argv-based. It inspects expanded command arguments, not OS-level
socket syscalls. Commands that construct destinations dynamically after process
start, read URLs from files, or invoke other network-capable child processes
may bypass argv inspection. This is not a replacement for OS network isolation,
a brokered egress proxy, or provider-side network controls.

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
  all tools. Matchers are validated at load time: a pattern with invalid
  syntax or a regular-expression denial-of-service shape (e.g. an unbounded
  quantifier wrapped in another one, `(a+)+`) is rejected with a config error,
  and a matching invalid pattern seen at run time skips the hook with a
  warning rather than disabling it.
- **`timeout`**: Timeout in seconds for the hook command (default `30`).

Hooks run before permission checks and can return decisions to allow, deny, or
rewrite tool inputs. See the [hooks README](../hooks/README.md) for the full
hook protocol.

### Stage-3 ML PII Classifier Hook

The default secret path is the in-process gitleaks detector: heuristic rules,
RE2 regexes, entropy gates, and no model runtime. For users who want an
opt-in ML layer, Phosphor ships an advanced hook recipe instead of embedding a
model in the agent loop.

The current hook event is **input-only**. `PreToolUse` receives:

```json
{
  "event": "PreToolUse",
  "session_id": "session-id",
  "cwd": "/workspace",
  "tool_name": "view",
  "tool_input": {
    "file_path": "README.md"
  }
}
```

There is no `tool_result` field today. That means this recipe can classify
tool arguments, command text, file targets, and content being written, but it
cannot inspect the eventual output of `view`, `grep`, `bash`, or MCP tools.
Future result-based scanning needs a separate `PostToolUse` event.

Enable the recipe in project-level `phosphor.json`:

```json
{
  "$schema": "https://github.com/hackafterdark/phosphor/blob/main/schema.json",
  "hooks": {
    "PreToolUse": [
      {
        "name": "pii-classify",
        "matcher": "^(view|grep|glob|bash|edit|write|multiedit|append|job_output)$|^mcp_",
        "command": "./scripts/pii-classify.sh",
        "timeout": 10
      }
    ]
  }
}
```

The matcher intentionally uses real Phosphor tool names: `view`, not `read`.
MCP tools are matched by their `mcp_` prefix because registered MCP tool names
have the shape `mcp_<server>_<tool>`.

Make the script executable:

```bash
chmod +x scripts/pii-classify.sh
```

Point it at any out-of-process classifier. The classifier reads candidate text
from stdin and prints JSON to stdout:

```json
{
  "decision": "pii",
  "confidence": 0.91,
  "reason": "email-like and national-id-like strings"
}
```

Example environment setup:

```bash
export PII_CLASSIFY_CMD="python ./my_pii_classifier.py"
export PII_CONFIDENCE_THRESHOLD="0.8"
export PII_CLASSIFY_FILES="1"
```

Environment variables used by `scripts/pii-classify.sh`:

| Variable | Default | Meaning |
|---|---|---|
| `PII_CLASSIFY_CMD` | empty | Shell command that reads text from stdin and prints classifier JSON. Empty means the hook fails open. |
| `PII_CONFIDENCE_THRESHOLD` | `0.8` | Minimum score treated as PII unless the classifier returns `{"decision":"pii"}`. |
| `PII_CLASSIFY_FILES` | `0` | When `1`, also read `file_path` inputs into the classifier. This gives `view` calls a content check before the tool runs, but it still cannot cover `grep` or `bash` stdout. |

A minimal classifier adapter can be written around an ONNX model or an Ollama
served model. The wrapper contract is small enough that any language works:

```python
#!/usr/bin/env python3
import json
import sys

text = sys.stdin.read()

# Replace this block with the ONNX/Ollama classifier call.
# Example models: LFM2.5-Encoder-350M-PII-Detector or deeppass2-bert.
score = 0.0
decision = "safe"

if score >= 0.8:
    decision = "pii"

print(json.dumps({"decision": decision, "confidence": score}))
```

The hook is opt-in, so the core binary stays zero-ML. A classifier crash,
timeout, missing `jq`, or missing model is treated as "no opinion" rather than
blocking the agent.

### Custom Secret Rules

Phosphor loads `.phosphor/secret-rules.toml` when present. It uses the gitleaks
custom-rule style and is merged on top of the gitleaks default config while
preserving the built-in allow-list behavior.

Example `.phosphor/secret-rules.toml`:

```toml
[[rules]]
id = "acme-service-token"
description = "Acme internal service token"
regex = '''\bacme-(?:prod|stg)-([a-f0-9]{32})\b'''
secretGroup = 1
entropy = 3.5
keywords = ["acme-prod-", "acme-stg-"]
tags = ["acme", "company"]

  [[rules.allowlists]]
  description = "Ignore documentation examples"
  paths = ['''(^|/)docs/.*\.md$''']
  regexTarget = "match"
  regexes = ['''acme-(?:prod|stg)-[a-f0-9]{32}''']

[[allowlists]]
description = "Known fake test fixture"
stopwords = ["acme-prod-0123456789abcdef0123456789"]
```

Supported top-level fields are:

| Field | Behavior |
|---|---|
| `[[rules]]` | Adds or replaces a gitleaks rule by `id`. |
| `[[rules.allowlists]]` | Rule-scoped allow-list. Supports `condition`, `commits`, `paths`, `regexes`, `regexTarget`, `stopwords`. |
| `[[rules.required]]` | Composite rule dependency. Supports `id`, `withinLines`, `withinColumns`. |
| `[[allowlists]]` | Global allow-list. Supports `targetRules` to attach the allow-list to specific rule IDs. |
| `[allowlist]` | Deprecated gitleaks singular global allow-list form; accepted for compatibility. |

If the file is missing, the default detector is used. If it is malformed or
contains an invalid rule, Phosphor logs a warning and keeps the built-in gitleaks
ruleset active rather than disabling secret scanning. The same fallback applies
when a pattern has a regular-expression denial-of-service shape (e.g.
`(a+)+` or `(a|aa)+`): it is rejected at load time, so a poisoned rules file can
neither disable scanning nor hang it while Phosphor scans attacker-influenced
output.

---

## Secret Redaction

Phosphor runs a layered redaction stack so secret material (API keys, tokens,
private keys, credential JSON) and — opt-in — PII never reaches the provider
request body or the session transcript. The layers are deliberately
independent: each covers a different leak path (file reads, tool output,
structured JSON results, child processes, the network boundary), and each is
on by default so a missing `security` block fails safe.

Every boolean knob below is **tri-state**: leaving it unset (omitting the key)
selects the secure default shown in the table; set it explicitly to `false`
only to opt out.

```json
{
  "$schema": "https://github.com/hackafterdark/phosphor/blob/main/schema.json",
  "security": {
    "redact_outgoing_secrets": true,
    "redact_outgoing_pii": false,
    "redact_sensitive_files": true,
    "sensitive_file_patterns": ["secrets-*.yaml"],
    "tokenize_secrets": false,
    "code_file_false_positive_mode": true,
    "wire_secret_redaction_force": true,
    "redact_json_keys": true,
    "json_secret_keys": ["internal_token"],
    "learned_secret_memory": true
  }
}
```

### Value Scanning of Tool Output

Every tool result is scanned with gitleaks detectors before the agent sees it;
detection replaces the secret value with a sentinel (the assignment key stays
visible so surrounding text keeps its meaning). Source-code reads use a
code-file scan mode: `code_file_false_positive_mode` (default `true`) drops
the low-precision generic `KEY=value` / `"apiKey": "value"` detector family
that false-positives on ordinary configuration maps, while the high-precision
vendor-prefix, PEM, JWT, and connection-string detectors still run. Custom
rules and allow-lists are configured via `.phosphor/secret-rules.toml` (see
[Custom Secret Rules](#custom-secret-rules)).

### Sensitive-File Whole-Value Redaction

When the agent reads a file that matches the sensitive set — the `.env`
family, rc files, credential JSON, private-key material — `redact_sensitive_files`
(default `true`) replaces every assignment value with a non-reusable sentinel
and, for opaque key files, the whole content. `sensitive_file_patterns`
**extends** the built-in set; it can never remove from it. The built-in
patterns are:

```text
.env  .env.*  *.env  .npmrc  .netrc  _netrc  .git-credentials  .envrc
credentials*.json  *service-account*.json  secrets.*
id_rsa  id_dsa  id_ecdsa  id_ed25519  *.pem  *.p12  *.pfx
```

### Known-Value Registry (always on)

Secret values Phosphor resolves itself — provider API keys, resolved env-var
and header values from provider configuration — are registered at resolution
time and scrubbed everywhere by exact match. This layer has **no config knob**:
an exact match against a credential you configured cannot be noisy, so it stays
on even when value scanning is opted out. Values short than a minimum length
and template placeholders (containing `$`) are not registered, so `${FOO}`
style config survives untouched.

### Learned Secret Memory

Once the scanner judges a value to be a genuine secret, its keyed HMAC digest
is remembered for the session (`learned_secret_memory`, default `true`). If
that same value reappears somewhere a per-context false-positive rule would
have spared it — e.g. a generic `KEY=value` hit inside a source file — the
value is recognised and scrubbed anyway. Only digests are kept, never
plaintext, and the memory is per-process: it is cleared on restart.

### Structured-JSON Key-Drop

For tool results that are a whole JSON document (MCP servers, JSON-emitting
CLIs), any field whose *key* is secret-shaped (`api_key`, `*_secret`,
`session_token`, …) has its value dropped by key name — cheaper and lower on
false positives than value scanning, which still runs as a complement.
`redact_json_keys` gates the layer (default `true`); `json_secret_keys`
extends the built-in key set case-insensitively and can never narrow it.

### Provider-Wire Last-Resort Mask

The exact outbound request body is scrubbed at send time: the registry first,
then the scanner. `redact_outgoing_secrets` (default `true`) gates the mask;
`wire_secret_redaction_force` (default `true`) keeps it on even when
`redact_outgoing_secrets` is set to `false` — the wire is treated as a hard
boundary, so fully disabling wire masking requires setting both to `false`.
PII masking (email, SSN, phone, IP, credit-card) on the same body is opt-in
via `redact_outgoing_pii` (default `false`) because its heuristics
false-positive on ordinary code and text.

### Reversible Tokenization (opt-in)

With `tokenize_secrets` (default `false`) the read path emits
`<secret:kind:id>` tokens instead of static sentinels, and the original value
is restored only when the agent writes back to a trusted sensitive file — an
`.env` round-trip that keeps working while the transcript only ever holds
tokens. Off by default because the static sentinels are already safe.

### Subprocess Environment Control

Shell commands spawned by the bash tool receive an environment filtered by an
allow-list plus a secret-shaped deny predicate, so a buggy or compromised child
process cannot trivially dump credential variables. This layer has no knob.

### Credential Egress Isolation (opt-in)

Detection stops accidental exposure and unknown credential shapes; it cannot stop
an already-prompt-injected agent from POSTing a value that it was allowed to see.
`security.egress_isolation` addresses that malicious-exfiltration case by moving
the plaintext out of the agent's reach. When enabled, detected credentials may be
replaced by an AES-256-GCM sealed token such as `<secret@v1.github-pat.…>`. The
transcript and model only carry that inert handle; only the loopback broker can
open it, and only when the request is aimed at an operator-allowlisted HTTPS
destination.

```json
{
  "$schema": "https://github.com/hackafterdark/phosphor/blob/main/schema.json",
  "security": {
    "egress_isolation": {
      "enabled": true,
      "allowed_hosts": ["api.example.com", "auth.example.com"],
      "https_only": true,
      "seal_detected_secrets": true,
      "deny_private_ips": true,
      "route_subprocesses": true,
      "max_body_bytes": 8388608
    }
  }
}
```

The default host list is empty, so an enabled broker denies every destination until
`allowed_hosts` names the permitted targets. `deny_private_ips` checks literal
targets, repeats the check after hostname resolution, and also guards the socket
dialer after the OS resolver selects an address. When the tier is requested but
the loopback broker cannot start, agent construction fails closed rather than
running without the advertised boundary.

With `route_subprocesses` enabled, bash child processes receive
`HTTP_PROXY`/`HTTPS_PROXY` and `NO_PROXY` for the authenticated loopback broker.
This gates cooperating CLIs, but it is not a network namespace: proxy-unaware
programs, raw sockets, and non-HTTP protocols can still bypass the broker. Keep
banned binaries, command/network policy, path confinement, and environment filtering
enabled. In-process fetch/web/search/download/sourcegraph/MCP HTTP clients use the
policy-aware egress transport when the broker is active and force direct dialing so
an environment proxy cannot bypass destination checks.

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
| Bash network egress policy | `tools.bash.network` | Object | Disabled |
| Hook security gates | `hooks.<event>` | Array | None |
| ML PII hook recipe | `hooks.PreToolUse[].command` + `scripts/pii-classify.sh` | Hook | Disabled |
| Custom secret rules | `.phosphor/secret-rules.toml` | File | Absent |
| Wire secret mask | `security.redact_outgoing_secrets` | Tri-state Bool | On |
| Wire PII mask | `security.redact_outgoing_pii` | Tri-state Bool | Off |
| Sensitive-file redaction | `security.redact_sensitive_files` | Tri-state Bool | On |
| Extra sensitive globs | `security.sensitive_file_patterns` | Array | Built-in set |
| Secret tokenization | `security.tokenize_secrets` | Tri-state Bool | Off |
| Code-file FP suppression | `security.code_file_false_positive_mode` | Tri-state Bool | On |
| Wire force mask | `security.wire_secret_redaction_force` | Tri-state Bool | On |
| JSON secret key-drop | `security.redact_json_keys` | Tri-state Bool | On |
| Extra JSON secret keys | `security.json_secret_keys` | Array | Built-in set |
| Learned secret memory | `security.learned_secret_memory` | Tri-state Bool | On |
| Egress isolation | `security.egress_isolation.enabled` | Bool | Off |
| Egress sealed sentinels | `security.egress_isolation.seal_detected_secrets` | Tri-state Bool | On |
| Egress host allowlist | `security.egress_isolation.allowed_hosts` | Array | Empty |
| Egress HTTPS-only mode | `security.egress_isolation.https_only` | Tri-state Bool | On |
| Egress private-IP denial | `security.egress_isolation.deny_private_ips` | Tri-state Bool | On |
| Route bash egress | `security.egress_isolation.route_subprocesses` | Tri-state Bool | On |
| Egress body ceiling | `security.egress_isolation.max_body_bytes` | Int | 8 MiB |
| OTel sampling rate | `observability.sampling_rate` | Float | 1.0 |
| OTel endpoint | `observability.endpoint` | String | Empty (disabled) |
