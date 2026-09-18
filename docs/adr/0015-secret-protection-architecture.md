# 15. Secret Protection: Defense-in-Depth Redaction Architecture

- **Status:** Accepted
- **Date:** 2026-09-15
- **Authors:** Phosphor Team
- **Superseded By:** —

## Context

An agent like Phosphor routinely touches material that must never reach a model
provider or a log file: API keys, tokens, private keys, credential JSON, and
PII. The exposure surfaces are:

1. **File reads** — the `view`/`edit` tools can open `.env`, `~/.ssh` keys, or
   service-account JSON, whose contents then become message history.
2. **Tool output** — CLIs, `curl`s, build logs, and MCP servers echo
   credentials back into tool results.
3. **Structured results** — JSON-producing tools embed secrets under
   secret-shaped keys (`api_key`, `session_token`, …).
4. **Subprocess environments** — a spawned shell command can `printenv` or a
   buggy script can dump the process environment.
5. **The provider wire** — everything above eventually rides inside the
   outbound request body to the LLM provider.

A single value-scanner (gitleaks-style detectors) cannot cover these alone. Its
generic `KEY=value`/`apiKey":"value"` detector family false-positives heavily on
ordinary source code (config maps, fixtures, env-plumbing), so running it at
full force on code reads buries the agent in noise; turning it off to quiet
the noise reopens the leak. Credential *values* Phosphor itself resolves (the
provider API key, per-provider headers) are also passed through code paths the
scanner never sees. The architecture therefore composes several narrow,
each-one-bypassable-but-defaulted-on layers instead of relying on one
classifier.

## Decision

Adopt a layered redaction stack, every layer on by default (`security.*`
tri-state knobs where nil means the secure default), each targeting a distinct
leak path:

1. **Value scanning of tool output (gitleaks `detect`).** Every tool result is
   scanned before the agent sees it. Two scan modes are selected from the
   context flag of the scan: `ScanFull` (all detectors) for shell/MCP/generic
   output, and `ScanCodeFile` for source-file reads, where the low-precision
   generic detector family is suppressed while high-precision vendor-prefix,
   PEM, JWT, and connection-string detectors still run
   (`code_file_false_positive_mode`).

2. **Path-based whole-value redaction for sensitive files.** Files matching the
   built-in sensitive set (the `.env` family, rc files, credential JSON,
   private-key material) — optionally extended by `sensitive_file_patterns`,
   never narrowed — have their assignment values replaced with a non-reusable
   sentinel on read; keys stay visible so the agent can still reason about the
   file (`redact_sensitive_files`).

3. **Structured-JSON key-drop.** When a whole tool result is a JSON document,
   fields whose *key* is secret-shaped (built-in set, extendable via
   `json_secret_keys`) have their values dropped by name — cheaper and lower-noise
   than value scanning, run as a complement to it (`redact_json_keys`).

4. **Known-value registry (`pkg/secrets.Registry`).** Always-on, untoggled
   exact-value scrubbing of literal secrets Phosphor knows about. Values are
   registered at credential-resolution time (provider API keys, resolved
   env/header values, custom-provider headers in `buildProvider`); template
   placeholders containing `$` are skipped so `${FOO}`-style configs survive.
   The registry scrubs *before* the togglable scanner, so a value Phosphor
   handed to itself can never leak even with scanning disabled.

5. **Learned-secret HMAC memory (`pkg/secrets.LearnedSet`).** A session-scoped
   classifier ledger. When the scanner judges a value to be a genuine secret,
   its HMAC-SHA256 digest (keyed by a process-random 32-byte secret; the
   plaintext is never stored) is remembered. On later scans, a generic
   finding whose value is already in the ledger is *restored* even where
   false-positive suppression would have dropped it — so a secret once judged
   real cannot be spared just because it reappears inside a source file
   (`learned_secret_memory`).

6. **Provider-wire last-resort mask.** The exact outbound request body is
   scrubbed at send time (registry + scanner) regardless of upstream layers.
   `wire_secret_redaction_force` keeps this on even when
   `redact_outgoing_secrets` is set to false: the wire is treated as a hard
   boundary (outgoing PII masking is opt-in via `redact_outgoing_pii`).

7. **Subprocess environment control.** Shell commands receive an environment
   filtered by an allow-list plus a secret-shaped deny predicate, so a
   compromised or buggy child process cannot trivially dump credential
   variables.

8. **Optional reversible tokenization.** With `tokenize_secrets` on, read-path
   redactions emit `<secret:kind:id>` tokens whose original value is restored
   only when the agent writes back to a trusted sensitive file — an `.env`
   round-trip that keeps working while the transcript only ever holds tokens.
   Off by default; static sentinels are already safe.

9. **Static ReDoS guard for user-supplied regexes (`pkg/saferegex`).** Every
   regex that arrives from operator or workspace configuration — custom secret
   rule `regex`/`path` and allowlist `regexes`/`paths`, hook `matcher`s,
   observability log filter patterns — is analysed against the
   `regexp/syntax` AST before it reaches any engine. The analyzer rejects
   the classic catastrophic families (unbounded quantifiers nested in
   unbounded quantifiers, ambiguous alternations under an unbounded
   quantifier, oversized repetition bounds and bounded expansions) so a
   hostile `.phosphor/secret-rules.toml` can neither hang the scanner on
   attacker-influenced bytes nor — via a syntax-style poison — disarm secret
   scanning entirely: a rejected rules file falls back to the built-in
   gitleaks ruleset, and rejected hook matchers and log filters degrade to
   skip/unfiltered with warnings.

10. **Architectural egress isolation (opt-in, off by default).** Every mechanism
    above is a *classifier*: it tries to recognise a secret at the moment it
    would leak. A prompt-injected agent that simply POSTs a credential it was
    handed is outside what recognition can catch, so this tier removes the
    plaintext from the agent's reach instead. With `security.egress_isolation.enabled`
    on, a detected credential becomes an AES-256-GCM *sealed sentinel*
    (`<secret@v1.…>`) in the transcript — inert bytes the model can neither read
    nor forge and that the write gate defangs so an echoed copy is not mistaken
    for a leak. Only the loopback egress broker (`pkg/egress`) can open a
    sentinel, and only toward a host the operator allowlisted over HTTPS; a
    sentinel it cannot resolve is refused, never forwarded. A network-allowed
    child process is pointed at the broker via `HTTP_PROXY`/`HTTPS_PROXY` so its
    egress is gated by the same destination allowlist (a connect-or-refuse
    guarantee for TLS tunnels, full resolve-and-refuse for cleartext forwards).
    Off by default it is byte-for-byte inert; enabling it is a deliberate,
    documented posture for high-risk egress, mirroring the credential model in
    the OpenClaw gateway.

## Rationale / Why This Approach

- **No single classifier is trustworthy enough to be load-bearing.** Value
  scanners are high-recall but noisy on code; key-drop is low-noise but only
  works on structured data; path rules are precise but only work on files.
  Layering lets each mechanism run where it is accurate, and the wire mask
  backstops all of them at the one point where leakage is irreversible.
- **FP suppression + learned memory are complementary, not contradictory.**
  `ScanCodeFile` removes the *unknown*-generic-noise problem (the common case);
  the learned ledger removes the *known*-secret escape hatch it would
  otherwise create. Together: quiet code reads, no spared secrets.
- **The registry is always-on and untoggled** because credential values are
  the highest-confidence secrets the system ever holds — Phosphor resolved
  them itself. Opt-outs for scanning exist for noise reasons; noise cannot
  apply to an exact match against a value you configured.
- **HMAC, never plaintext, in the learned memory.** Storing judged-secret
  plaintext in a process-global set would turn a memory dump or a debug log
  into a secret disclosure and would duplicate the very values redaction
  exists to remove. A keyed digest preserves the only property detection
  needs ("have I seen this exact value before?") while keeping the raw
  material out of reach; keying (vs. plain SHA-256) preserves
  offline-confirmation resistance for high-entropy-but-guessable values.
  The digest is deliberately the HMAC output, not a length extension of the
  input — a construction bug here once leaked plaintext prefixes into tokens
  and is now pinned by a regression test.
- **No at-rest persistence of learned digests (yet).** Encrypting them at rest
  would require a master key as trusted as the secrets themselves; storing
  the HMAC key next to the digests collapses the anti-offline-confirm
  property. `LearnedSet` is serializable by digest and bounded (FIFO), so a
  future KMS-backed store can be added without redesign; for now the memory
  is process-lifetime.
- **Tri-state config with secure defaults.** Each knob is `*bool`: nil (unset)
  resolves to the safer value (redaction on; PII off because its heuristics
  false-positive on ordinary text), so a missing `security` block fails safe
  and opt-outs are explicit, auditable decisions.

## Implementation

| Layer | Home | Key knobs |
|---|---|---|
| Value scanning / scan modes | `pkg/agent/tools/secrets.go` (`redactSecretsForTool`, `redactSecretsAt`) | `redact_outgoing_secrets`, `code_file_false_positive_mode` |
| Sensitive-file whole-value redaction | `pkg/agent/tools` read path + `pkg/config` (`DefaultSensitiveFilePatterns`) | `redact_sensitive_files`, `sensitive_file_patterns` |
| JSON key-drop | `pkg/agent/tools/json_key_redact.go` | `redact_json_keys`, `json_secret_keys` |
| Known-value registry | `pkg/secrets/registry.go` (fed from `pkg/config` + `pkg/agent/coordinator.go`) | none (always-on) |
| Learned HMAC memory | `pkg/secrets/learned.go`, consulted in `filterFindings` | `learned_secret_memory` |
| Wire last-resort mask | `pkg/agent/tools/secrets.go` (`RedactSecretsForWire`) | `wire_secret_redaction_force`, `redact_outgoing_pii` |
| Subprocess env control | `pkg/shell/env.go` | (allow-list + deny predicate) |
| Tokenization | redaction token store | `tokenize_secrets` |
| Egress isolation (sealed sentinels + broker) | `pkg/egress` (`SecretStore`, `Proxy`, `RoundTripper`), armed from `pkg/agent/coordinator.go` | `egress_isolation.{enabled, seal_detected_secrets, allowed_hosts, https_only, deny_private_ips, route_subprocesses}` |
| Static ReDoS analyzer | `pkg/saferegex` (wired into `secret_rules.go`, `config.ValidateHooks`, `hooks.Runner`, `log.compileFilters`) | none (always-on) |

Pipeline order per tool result: registry scrub (always) → JSON key-drop (JSON
documents) → scanner under `SecretsEnabled()` with the context-flagged scan
mode, recording judged values into the learned ledger and consulting it when
filtering findings. Policy values are snapshotted once per session
(`pkg/agent/tools/redaction_policy.go`) and passed from the coordinator, so a
config change cannot half-apply mid-session.

## Consequences

**Positive**

- Secret material is blocked at every ingress (files, tool output, JSON,
  child environments) and at the egress boundary (the wire), so no single
  missed detector is sufficient for a leak.
- Code reads are quiet by default: generic-detector noise is suppressed, but
  only for values that have never been judged sensitive.
- Phosphor's own configured credentials can never round-trip back into
  transcript or history, even with all togglable layers off.
- Redaction decisions are deterministic and testable: exact-value registry,
  key-name drop, and digest equality rather than purely heuristic matching.

**Negative / trade-offs**

- Learned memory is process-scoped: a restart forgets judged values, and
  cross-process sharing would expose the HMAC key — accepted for now,
  revisitable via a KMS-backed digest store.
- The registry and learned sets are bounded (FIFO, capped capacity); under
  pathological churn the oldest entries are evicted.
- With false-positive mode on, a *never-before-seen* generic secret printed
  inside a source file can pass through; it is then judged, learned, and
  scrubbed on reappearance — first-occurrence exposure in code files remains
  the deliberate residual risk that buys low noise.
- Wire-level masking can, in edge cases, alter a payload that contained an
  exact registered value as innocent text (e.g. pasting your own API key in
  prose); this is the accepted cost of treating the egress boundary as hard.
