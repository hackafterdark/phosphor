# Memory Poisoning Defenses

## The threat

Memory is the highest-privilege injection surface in the product. Its content
is replayed into the **system prompt of every future session**, so a poisoned
entry is not a one-turn problem — it persists until somebody notices it, and it
carries the authority of the system turn rather than the lower trust of a tool
result. From the package's own words (`security.go:14-18`):

> Memory is the highest-privilege injection surface in the product: its content
> is replayed into the system prompt of every future session, so a poisoned entry
> persists until somebody notices it. Everything that enters or leaves the vault
> therefore passes the same gates that web and MCP content pass, plus a secret
> scan on the way in.

The defenses are **layered**. No single one is the guarantee; together they make
the four realistic poisoning routes each fail at a specific gate:

| Route a poisoning would take | The gate that stops it |
|------------------------------|------------------------|
| An agent writing a control-token / jailbreak payload into a memory body | the write-time content scan + the read-path sanitizer |
| A subagent or cron prompt poisoning the shared vault | the single-writer / primary-context rule |
| The agent quietly "remembering" a plausible-but-wrong assumption as fact | the inferred→`pending` rule |
| Something reaching the prompt with elevated trust it did not earn | the trust floor + the index re-derivation of `trust`/`status` |
| An offline edit to the vault or DB while the agent is off | the tamper seal — see [INTEGRITY.md](INTEGRITY.md) |

---

## 1. The single-writer fence

The vault is written **only** through the `memory` tool. The generic `edit`,
`write`, `append`, and `multiedit` tools refuse any target that resolves into a
vault (`vaultWriteGuard`, `pkg/agent/tools/vaultguard.go:14-23`, backed by
`memory.IsVaultPath`). This is what lets schema, sanitization, and index sync be
*guarantees* rather than hopes — there is one door in, and it runs every check.

On top of that, only the **primary** agent context may commit memories: a
subagent's throwaway goal or a cron system prompt cannot contaminate the shared
vault, though reads stay open (`gate.go:177-182`).

---

## 2. The content scan on the way in

`CheckContent` (`security.go:52-89`) runs on **both** gate paths
(`gate.go:183` for `Add`, and again in `Apply`), and it fails the write closed on
two things before a file is ever written:

**(a) A strict injection grammar.** After defanging, any residual ChatML-class
control token or instruction-override phrasing is refused (`security.go:91-106`).
The grammar (`injectionPattern`) matches, among others: leftover `<|…|>` control
tokens, `[im_start]`/`[im_end]`, forged `<system>`/`<user>`/`<assistant>` role
tags, tool-call forgery markers (`[call]`, `[call_end]`, `[endoftext]`),
"ignore … instructions", "disregard … instructions", "new system prompt",
"developer mode", and "you are/now (no longer) (not) a … ai/assistant/model"
role rewrites.

**(b) A credential scan.** The gitleaks detector runs over the content, and a
match is refused. The **matched secret is never echoed** — only its rule
identity — so a blocked write cannot leak the credential into the transcript
(`security.go:73-88`). And if the detector itself is unavailable the write is
**refused**, not waved through: it fails closed rather than let an unscanned
credential become a permanent part of the system prompt (`security.go:62-68`).

> Practical consequence: memory is the wrong place for a secret even though it
> refuses one for you. It is plaintext on disk (see the confidentiality caveat in
> [INTEGRITY.md](INTEGRITY.md)); the credential scan is a backstop, not a vault.

---

## 3. Sanitization on the way out (and every re-read)

`Sanitize` defangs ChatML-class control tokens and homoglyph smuggling, and it is
idempotent and inert on clean text (`security.go:37-50`). It runs **not only on
write but on every read from disk**, because humans, Obsidian plugins, and vault
sync also write these files — a file that was clean when the agent wrote it can
have been edited by something else since. The assembled injection block and every
recall/`memory_read` render pass through it
(`lifecycle.go:219-220`, `read.go:61,223`, `tools/memory_read.go:85-92`).

---

## 4. Inferred content can never self-promote

This is the rule that kills the most realistic poisoning failure — *"the agent
filled a gap with a plausible assumption, committed it, and is now confidently
wrong forever"* (`gate.go:187-193`):

```go
inferred := e.Inferred()
if inferred {
	e.Status = StatusPending
}
```

An entry is **inferred** unless `asserted` is explicitly set — `Inferred()` treats
a nil `asserted` as inferred, because *the safe default is the one that cannot
pollute the hot window* (`doc.go:220-225`). Inferred entries are forced to
`StatusPending`, and the Tier-A candidate query requires `asserted = 1`, so an
inference is **structurally unable** to reach the always-injected window: it can
only ever sit as a Tier-B draft awaiting confirmation (`lifecycle.go:78`). The
gate's `hardOverride` likewise blocks promoting an inferred statement unconfirmed,
and `safe()` never auto-commits one (`gate.go:645-648`, `623-626`). Only the
human's `confirm` (which sets `asserted`) and the trust bump it grants
(`0.5 → 0.8`) let an entry cross into Tier A.

---

## 5. Quarantine, and fail-closed

A row is **quarantined** — held out of recall and injection, not deleted — when
it cannot be trusted:

- a frontmatter that fails to parse (`*ParseError` → `dropRow`,
  `frontmatter.go:23-31`, `75-81`, `store.go:753-765`);
- a tamper seal that fails to verify at index time → `quarantined = 1`
  (`store.go:776-784`);
- and even without a re-index, the read gate drops a row whose seal fails when a
  query scans it (`store.go:1131-1149`, and the `entryVerifies` checks in the
  search/neighbor paths).

Every read path filters `quarantined = 0` — recall, the injected window, and the
corpus counts alike (the status/`Report` tallies exclude quarantined rows from the
active/pending/retired figures and the type histogram, so a held-out entry is not
miscounted as live). If the tamper seal is on but the signing key is **missing**,
the store fails **loudly**: `Open` refuses to hand back a keyless sealed handle
(`initIntegrity` returns the load error) rather than open a store whose writes
would look committed yet quarantine at read time, and `Put` refuses to write what
it could not sign. A fresh key is never minted over an already-sealed corpus (that
would orphan it), and `/memory key restore` stays reachable through a maintenance
handle so the operator can recover the key — see
[INTEGRITY.md](INTEGRITY.md#the-fail-closed-posture).

---

## 6. Trust is not self-granting

The auto-promotion path admits an entry to Tier A only above `AutoPromoteMinTrust`
(`0.75`), and only *confirmed* entries clear `0.8` — the unreviewed default is
`0.5`. So **unreviewed content cannot structurally self-inject** into every prompt
(`store.go:85-93`). The operator can pull any entry down explicitly: `/memory
demote` drops trust to `0.2` and unpins; `/memory dispute` drops trust and stamps
a "verify before relying on it" note. And because `trust`/`status` are system
fields that the index **re-derives from the row** for non-system writes
(`store.go:791-802`), a hand-edit of `phosphor.trust:` in the frontmatter cannot
raise an entry's standing in the index.

---

## Defense summary

| # | Defense | Where | Fails closed? |
|---|---------|-------|---------------|
| 1 | Single-writer fence + primary-context rule | `vaultguard.go:14-23`, `gate.go:177-182` | yes (refused) |
| 2 | Injection grammar + credential scan on write | `security.go:52-106`, `gate.go:183` | yes |
| 3 | Sanitizer on write and every read | `security.go:37-50` | defangs, inert on clean |
| 4 | Inferred → `pending`, never Tier A | `gate.go:187-193`, `lifecycle.go:78` | yes |
| 5 | Quarantine of unparseable / failed-seal rows | `store.go:753-784`, `1131-1149` | yes |
| 6 | Trust floor + index re-derivation | `store.go:85-93`, `791-802` | structural |
| 7 | Tamper seal (offline edits) | `integrity.go` | yes — [INTEGRITY.md](INTEGRITY.md) |

The seal (7) is one of these seven, not the whole of them — which is why the
exempt fields (`tags`, `links`, `notes`, and the human `title`) being unsealed is
a bounded concern rather than a hole: they remain subject to the content scan on
write (2), the sanitizer on read (3), quarantine (5), and the trust/recall gates
(6), even though the seal alone would not notice an edit to them.

---

## Related

- [OVERVIEW.md](OVERVIEW.md) — how memory works end to end.
- [INTEGRITY.md](INTEGRITY.md) — the tamper seal and its threat model.
