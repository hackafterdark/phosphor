# Secret Protection & Anti-Exfiltration

Phosphor treats credentials, API keys, private-key material, and PII as data
that must never reach the model provider, the transcript, session storage, or a
log file. This document describes how the shipped defense-in-depth stack works:
where it scans, what each layer does, how it behaves under prompt injection and
hostile tool output, and how to configure it.

For the design record see `adr/0015-secret-protection-architecture.md`. For the
config-surface reference see `security/CONFIGURATION.md`. The related hardening
guides are `security/WORKSPACE_HARDENING.md`,
`security/ENVIRONMENT_HARDENING.md`, and `security/NETWORK_EGRESS_HARDENING.md`.

## Threat Model

The exposure surfaces are the places a secret can cross into a context that
leaves the machine:

1. **File reads** — `view`/`edit` can open a `.env`, an SSH private key, or a
   service-account JSON, whose bytes then become message history.
2. **Tool output** — build logs, CLIs, and MCP servers echo credentials back
   into results.
3. **Structured results** — JSON-producing tools embed secrets under
   secret-shaped keys (`api_key`, `session_token`, …).
4. **Subprocess environments** — a spawned shell command can dump the process
   environment.
5. **The provider wire** — everything above eventually rides inside the outbound
   request body to the model provider.

The concrete attack paths this stack defends against:

- **Read-then-forward.** The agent reads a secret file; the bytes reach the
  provider and the provider's infra. Mitigated by redacting before anything
  enters the transcript.
- **Prompt injection.** Hostile content (repo comments, fetched pages, MCP
  results) tries to command the agent to exfiltrate a credential. Mitigated by
  content-boundary sanitization plus, for the architectural tier, by removing
  the plaintext from the agent's reach entirely.
- **Accidental commit via edit.** The agent writes code containing a real key it
  saw in an earlier read. Mitigated by the write-path detection gate.
- **Supply chain.** A package postinstall script prints environment variables.
  Mitigated by the subprocess environment allow-list and the output scanner.
- **Telemetry side-channel.** Traces or logs capture a secret. Mitigated by the
  outbound redaction and observability redaction.

## Design Principles

- **No single load-bearing classifier.** Every layer targets a distinct leak
  path and fails independently, so one missed detector is not a leak. The
  provider-wire mask is a final backstop at the one point where leakage becomes
  irreversible.
- **Invisible by default.** Clean code sees no prompts and no friction. A
  credential is replaced in place by a neutral marker such as
  `<redacted:github-pat>` and the work continues. Only fingerprint metadata is
  recorded; the matched value is never logged or stored.
- **Fail-safe configuration.** Every `security.*` knob is tri-state; an unset
  value resolves to the safer behavior. Read-path policy is snapshotted at
  session start, so a mid-session environment or config change cannot silently
  disarm redaction.
- **Heuristic-first.** The core binary embeds no ML runtime. A classifier can be
  attached through the `PreToolUse` hook recipe where an operator wants one.
- **Performance.** The scanner is budgeted to under one millisecond per KB of
  content with a zero-allocation fast path. Clean output that matches no prefilter
  keyword passes through on a single cheap scan.

## The Redaction Pipeline

Every tool result is passed through the stack **before** the agent can see it.
The layers, in execution order, are:

```
Tool result
   │
   ├─ 1. Known-value registry scrub (always on, before anything togglable)
   ├─ 2. JSON key-drop (when the whole result is a JSON document)
   ├─ 3. Value scanner (gitleaks detector) under a context-selected scan mode,
   │       consulting + recording into the learned-secret memory
   ├─ 4. Sensitive-file whole-value redaction (path-matched files)
   ▼
Transcript / history / provider wire
   │
   └─ 5. Provider-wire last-resort mask (forced on) at send time
```

### 1. Value scanning of tool output

The gitleaks `detect` detector (700+ vendor-prefix rules plus per-rule entropy
gates) runs over every result: bash stdout/stderr, background `job_output`,
`view`, `grep`, and MCP results. A fast Aho-Corasick-style keyword prefilter
gates the expensive regex work, so most clean output skips through on a single
cheap pass.

Two scan modes are selected from a context flag on the scan:

- `ScanFull` — all detectors. Used for shell output, MCP results, and generic
  tool text.
- `ScanCodeFile` — source-file reads. The low-precision generic `KEY=value` /
  `apiKey":"value"` detector family is suppressed while the high-precision
  vendor-prefix, PEM, JWT, and connection-string detectors still run. This keeps
  code reads quiet without reopening the leak for anything already known to be
  sensitive (see the learned memory below). Controlled by
  `code_file_false_positive_mode`.

Custom rules can be added via `.phosphor/secret-rules.toml`.

### 2. Known-value registry

`pkg/secrets.Registry` is an always-on, untoggled exact-value scrubber for the
literal secrets Phosphor itself knows about. Values are registered at credential
resolution time — provider API keys, resolved environment/header values, and
custom-provider headers — and their URL-encoded and JSON-escaped forms are
scrubbed too, via a bounded `strings.Replacer` behind a first-byte prefilter.

Because it is exact-match against values you configured, it is the highest-
confidence layer in the stack and is never togglable off: there is no false-
positive concern for an exact match against a credential you supplied. It runs
before the togglable scanner, so a value Phosphor handed to itself can not leak
even with pattern scanning disabled.

### 3. Structured-JSON key-drop

When a whole tool result is a single JSON document, any field whose **key** is
secret-shaped (`api_key`, `session_token`, `credentials`, `private_key`, …) has
its value dropped by name to a non-reusable `<redacted:json:key>` marker. This is
cheaper and lower-noise than value scanning for the JSON case and runs as a
complement to it, not a replacement: the value scanner still runs afterward.
Controlled by `redact_json_keys` (on by default); the key set is extendable via
`json_secret_keys`.

### 4. Sensitive-file whole-value redaction

Files matching the built-in sensitive set (the `.env` family, rc files,
credential JSON, private-key material) have their assignment **values** replaced
with a non-reusable sentinel on read while the **keys** stay visible, so the agent
can still reason about the file's structure. `cat`/`source`-style bash reads get
the same treatment through argv detection, and the sensitive set is surfaced to the
system prompt as an advisory nudge.

The built-in set can be extended by `sensitive_file_patterns` but never narrowed.
Controlled by `redact_sensitive_files`.

### 5. Learned-secret memory

`pkg/secrets.LearnedSet` is a session-scoped ledger of HMAC-SHA256 digests —
keyed by a process-random secret, never the plaintext — of every value the scanner
judged to be a genuine secret. On a later scan, a finding whose digest is already
in the ledger is force-scrubbed even in a context where false-positive suppression
would have dropped it. This closes the escape hatch that `code_file_false_positive_mode`
would otherwise open: a credential that appeared once and was judged real cannot be
spared just because it later shows up inside a source file.

The ledger keeps digests only, never plaintext, so a memory dump or debug log can
not turn it into a disclosure. It is bounded and process-lifetime; digests are
serializable so a durable store can be added later without a redesign.
Controlled by `learned_secret_memory`.

### 6. Provider-wire last-resort mask

At send time, the exact outbound request body is scrubbed once more by the
registry plus the scanner (and an opt-in PII masker), independent of the
upstream layers. `wire_secret_redaction_force` keeps this mask on even when
`redact_outgoing_secrets` is set to `false`, because the wire is treated as a
hard boundary. Outgoing PII masking is separately opt-in via
`redact_outgoing_pii`.

This is also the only place a secret pasted directly into a chat prompt can be
caught, since the user's own input never passes a tool boundary.

### 7. Subprocess environment control

Shell commands run with an environment filtered by an allow-list plus a
secret-shaped deny predicate (`*_API_KEY`, `*_SECRET`, `*_TOKEN`, `*_PASSWORD`, …)
and an always-strip set, so even an over-broad operator `allowed_env` cannot hand a
credential-named variable to a child process. Proxy URLs have their credentials
scrubbed. This layer has no off switch. See `security/ENVIRONMENT_HARDENING.md`.

### 8. Static ReDoS guard for supplied regexes

Any regular expression arriving from operator or workspace configuration — custom
secret rules, allow-list patterns, hook matchers, log filters — is analysed against
the `regexp/syntax` AST **before** it reaches any engine (`pkg/saferegex`). The
analyzer rejects the classic catastrophic-repetition shapes so a hostile
`secret-rules.toml` can not hang the scanner on attacker-influenced bytes or, by
presenting a syntax error, disarm scanning entirely: a rejected rules file falls
back to the built-in detector set, and rejected hook matchers or log filters
degrade to skip/unfiltered with a warning. The scanning engines themselves are
RE2 (linear) by construction.

### Optional: reversible tokenization

With `tokenize_secrets` on, read-path redactions emit `<secret:kind:id>` tokens
whose original value is restored only when the agent writes back to a trusted
sensitive file (an `.env` round-trip that keeps working while the transcript only
ever holds tokens). It is off by default because the static non-reusable sentinels
are already safe; this is an opt-in convenience for round-tripping values.

## Architectural Egress Isolation (opt-in)

Every layer above is a *classifier*: it tries to recognise a secret at the
moment it would leak. A prompt-injected agent that simply POSTs a credential it
was handed is outside what recognition can catch. For that case there is an
opt-in tier, `security.egress_isolation`, that removes the plaintext from the
agent's reach instead of trying to spot it.

It is **off by default** and byte-for-byte inert when off. Enabling it is a
deliberate, documented posture for high-risk egress. It is implemented in
`pkg/egress` and armed once from the agent coordinator at the same frozen
startup snapshot the read-path uses.

### How it works

1. **Sealed sentinels.** With the tier on (`seal_detected_secrets`), a detected
   credential becomes an AES-256-GCM sealed token of the form
   `<secret@v1.<label>.<payload>.end>` in the transcript. The model sees only an
   inert handle, never the bytes. The token is sealed under a process-random key
   the agent can neither read nor forge; its label is bound as additional
   authenticated data, so it can not be replayed under a different label and any
   edit fails to open. The write gate defangs Phosphor's own tokens before
   detection, so an echoed copy of a token written into a file is not mistaken
   for a leaked secret.
2. **A single trusted resolver.** The only thing that turns a token back into
   plaintext is the egress broker, and only toward a destination the operator
   allowlisted over HTTPS (`allowed_hosts`, `https_only`). A token the broker can
   not resolve is refused, never forwarded as an opaque blob — so an inert handle
   never reaches a destination that could make something of it.
3. **In-process HTTP clients are broker-aware.** The fetch, web-fetch, web-search,
   download, sourcegraph, and MCP HTTP/SSE clients route through a broker-aware
   transport. When the broker is active the transport forces direct,
   policy-aware dialing so an environment-set proxy can not become a destination-
   policy bypass; when it is inactive the clients behave like a normal HTTP client.
4. **Subprocess routing.** With `route_subprocesses`, a network-allowed bash child
   inherits `HTTP_PROXY`/`HTTPS_PROXY` pointing at the authenticated loopback
   broker. The broker's `CONNECT` handling gives a connect-or-refuse destination
   guarantee for TLS tunnels, and full resolve-and-refuse for cleartext forwards.
5. **Destination policy.** Deny-by-default: an empty `allowed_hosts` permits no
   destination. `deny_private_ips` refuses loopback, private, link-local, and
   cloud-metadata ranges; it checks literal targets, re-checks after hostname
   resolution, and also guards the socket dialer after the OS resolver picks an
   address.

### Fail-closed and frozen at startup

If the tier is explicitly requested but the broker can not start, agent
construction fails rather than running without the advertised boundary. The
broker state is frozen once at startup: a prompt-injected tool that mutates the
environment or re-reads config mid-session can not arm or disarm it, because
nothing on the request path consults live configuration.

### Limits

This tier is **not** a full network namespace. It is a strong control across the
brokered paths, and a destination-policy control elsewhere. Specifically:

- Proxy-unaware programs, raw sockets, and non-HTTP protocols can bypass the
  broker; those remain governed by the banned-command list, the command/network
  policy, path confinement, and environment filtering.
- For `CONNECT` tunnels (HTTPS from a subprocess) the broker enforces only the
  destination policy; it can not resolve sentinels inside an established TLS
  tunnel it can not see into.
- The model-provider's own HTTP client is deliberately outside this tier: provider
  traffic has its own forced wire-redaction boundary, and routing it through the
  deny-by-default egress policy would require allowlisting the provider hosts.

Keep the default-on detection and confinement layers enabled; the egress tier is an
additional boundary, not a substitute for them.

## Configuration

The `security` block is tri-state; leaving any field unset selects the safer
default. A full reference with every path, type, and default lives in
`security/CONFIGURATION.md`. The most relevant controls:

| Control | Config path | Default |
|---|---|---|
| Value scanning (read path) | built-in, no user toggle | On |
| Outgoing secret mask | `security.redact_outgoing_secrets` | On |
| Wire mask forced on | `security.wire_secret_redaction_force` | On |
| Outgoing PII mask | `security.redact_outgoing_pii` | Off |
| Code-file FP suppression | `security.code_file_false_positive_mode` | On |
| Sensitive-file redaction | `security.redact_sensitive_files` | On |
| Extra sensitive globs | `security.sensitive_file_patterns` | Built-in set |
| Reversible tokenization | `security.tokenize_secrets` | Off |
| JSON key-drop | `security.redact_json_keys` | On |
| Extra JSON secret keys | `security.json_secret_keys` | Built-in set |
| Learned secret memory | `security.learned_secret_memory` | On |
| Egress isolation | `security.egress_isolation.enabled` | Off |
| Egress sealed sentinels | `security.egress_isolation.seal_detected_secrets` | On |
| Egress host allowlist | `security.egress_isolation.allowed_hosts` | Empty |
| Egress HTTPS-only | `security.egress_isolation.https_only` | On |
| Egress private-IP denial | `security.egress_isolation.deny_private_ips` | On |
| Route bash egress | `security.egress_isolation.route_subprocesses` | On |
| Egress body ceiling | `security.egress_isolation.max_body_bytes` | 8 MiB |

An example that turns on the egress tier for a known API host:

```json
{
  "security": {
    "egress_isolation": {
      "enabled": true,
      "allowed_hosts": ["api.example.com", "auth.example.com"],
      "https_only": true,
      "seal_detected_secrets": true,
      "deny_private_ips": true,
      "route_subprocesses": true
    }
  }
}
```

## What the Stack Does Not Cover

Stating the residual risk plainly, so the guarantees are not over-read:

- **Custom, novel, or obfuscated token formats.** A credential shape no rule
  knows, or one that has been base64/hex/XOR obfuscated, can pass the scanner.
  The `.phosphor/secret-rules.toml` rules and the opt-in ML hook are the
  intended answers.
- **First-occurrence exposure inside a source file.** With code-file FP
  suppression on, a never-before-seen generic secret printed inside source can
  pass once; it is then judged, learned, and scrubbed on every reappearance.
  That single pass-through is the deliberate cost of keeping code reads quiet.
- **The agent's own generated text and the user's typed prompts** are not a tool
  boundary; only the forced provider-wire mask can catch a credential that
  reaches the outbound body through those paths.
- **Non-text and out-of-band material** (compiled artifacts, network protocols
  other than the ones brokered) are outside this scanner's reach.
- **The egress tier, when off or partial.** Its strongest "a compromised agent
  can not move a real secret" guarantee holds across the brokered paths only; the
  detection and confinement layers carry the rest, and the subprocess boundary is
  proxy-respecting rather than a network namespace.

## Where It Lives in the Code

| Concern | Home |
|---|---|
| Value scanning, scan modes, wire mask | `pkg/agent/tools/secrets.go` |
| JSON key-drop | `pkg/agent/tools/json_key_redact.go` |
| Known-value registry | `pkg/secrets/registry.go` |
| Learned HMAC memory | `pkg/secrets/learned.go` |
| Redaction policy snapshot | `pkg/agent/tools/redaction_policy.go` |
| Subprocess environment filtering | `pkg/shell/env.go` |
| Prompt-injection / content-boundary sanitization | `pkg/security/externalcontent/` |
| ReDoS analyzer | `pkg/saferegex` |
| Egress isolation (sealed sentinels + broker + policy) | `pkg/egress` |
| Tier arming (frozen at startup) | `pkg/agent/coordinator.go` |
