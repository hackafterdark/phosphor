# Memory System Overview

## Overview

Phosphor's memory is a durable, cross-session store of atomic facts the agent
records about a project and about its operator: decisions made, constraints
ruled out, requirements stated, references gathered, plans in flight, and
preferences expressed. It lives entirely in-process and on local disk — there
are no embeddings, no external service, and no background model pipeline.

The subsystem is a small number of cooperating parts:

- a **markdown vault** that is the source of truth — one file per atomic fact;
- a **disposable SQLite / FTS5 index** derived from that markdown;
- the **agent-facing tools** (`memory`, `memory_search`, `memory_read`) that
  write through a governed gate;
- the **recall/injection layer** that decides what reaches the system prompt.

The package doc comment states the design invariants the whole subsystem exists
to protect (`internal/memory/doc.go:1-18`):

> - Markdown is truth, the database is cache. Deleting `memory.db` and
>   rescanning the vault reproduces the index byte-for-byte.
> - No embeddings, no external service, no background model pipeline.
>   Everything runs in-process and is deterministic.
> - The memory tool is the only writer. `edit`/`write` cannot touch the vault,
>   which is what lets schema, sanitization and indexing be enforced.
> - Nothing is ever hard-deleted; supersede and retire are tombstones, so a
>   wrong automatic decision stays recoverable.
> - Memory records asserted truth. An inferred statement can only ever land as
>   a pending Tier B draft, never in the always-injected Tier A window.

The last two are security properties; they are covered in detail in
[POISONING.md](POISONING.md). The tamper seal that protects entries from offline
tampering is covered in [INTEGRITY.md](INTEGRITY.md).

---

## On-disk layout

Each *scope* is a self-contained **bank**: a directory holding its own entries
and its own SQLite index. Two scopes exist (`internal/memory/doc.go:25-32`):

| Scope | Vault root | Crosses projects? |
|-------|------------|-------------------|
| `global` | `~/.phosphor/memory` (`GlobalVaultDir`, `vault.go:28-30`) | yes — the only scope that does |
| `project` | `<workspace>/.phosphor/memory` (`ProjectVaultDir`, `vault.go:33-35`) | no |

Inside each bank (`vault.go:13-24`, `store.go:383`):

```
<vault>/
  entries/
    <slug>.md            one file per atomic fact
  .phosphor-index/
    memory.db            the derived, disposable SQLite index
```

The project bank also gets a `.gitignore` ignoring `.phosphor-index/`,
`.obsidian/`, `*.tmp`, and `*.bak.*` so the derived index and Obsidian's
metadata never land in version control (`vault.go:83-105`). The signing key the
tamper seal uses lives at the global bank root — see
[INTEGRITY.md](INTEGRITY.md#where-the-key-lives).

The vault is *Obsidian-native*: a person can open it in Obsidian, and the
project vault is laid out so it works as an Obsidian vault directly. This is why
the schema keeps machine fields namespaced away from human ones (below).

---

## The `Entry` model

An `Entry` is one atomic fact — one file, one index row (`doc.go:141-188`).
Identity is the `ID`, never the path, so an Obsidian rename or move cannot break
a reference. Fields fall into three **ownership** regions, which is the single
most important thing to understand about the format:

| Region | Fields | Who owns it |
|--------|--------|-------------|
| **agent-authored** | `summary`, `body`, `type`, `thread`, `source`, `expires` | written through the `memory` tool |
| **human** | `title`, `tags`, `links`, and the `## Notes` block | a person, in Obsidian |
| **system** (frontmatter under `phosphor.*`) | `trust`, `status`, `pinned`, `recall_count`, `helpful_count`, `timestamps`, `supersedes`, `from_decision`, `owner`, `asserted`, `mac` | recomputed on re-index |

The system fields live under the `phosphor.*` namespace precisely so a person's
own Obsidian properties can never collide with the machine's (`doc.go:159-176`).

`body` and `notes` are split from one markdown region by a `## Notes` heading
(`frontmatter.go:96-107`): everything above it is the agent's sealed prose,
everything below it is the human's free-form annotation area. `Serialize`
re-joins them; a person writing under `## Notes` and the agent rewriting `body`
do not clobber one another (`frontmatter_test.go:74`).

### Enumerations

- **`Type`** — the taxonomy: `decision`, `constraint`, `requirement`,
  `open_question`, `reference`, `plan`, `preference`, `fact`, `pattern`,
  `environment`, `task`, `policy`. The set is closed so retrieval can filter on
  it and the gate can reason about blast radius (`doc.go:34-58`). An
  unrecognized string degrades to `fact`, the least consequential kind
  (`doc.go:76-84`).
- **`Status`** — lifecycle: `active`, `cold`, `retired`, `merged`, `pending`
  (`doc.go:101-110`). `pending` is the quarantine for unconfirmed inferences;
  `retired`/`merged` are tombstones (nothing is hard-deleted).
- **`Owner`** — who authored a region: `agent`, `human`, `system`
  (`doc.go:131-139`).
- **`Op`** — the write intent / classification outcome: `add`, `refine`,
  `qualify`, `supersede`, `merge`, `retire`, `pin`, `unpin`, `note`, `confirm`,
  `ignore`, `no_op` (`doc.go:112-129`).

`Trust` is a float ranking an entry carries into recall and injection. The
default for unreviewed content is `0.5`; only an explicit user confirmation
raises it (to `0.8`), and `demote`/`dispute` drop it to `0.2`. That floor is
what makes the auto-promotion gate trustworthy — see
[POISONING.md](POISONING.md#inferred-vs-asserted).

---

## The write path

The `memory` tool (`internal/memory/tools/memory.go`) never writes the vault
directly. Every write is routed through a **gate** so the schema, sanitization,
classification, and index sync are all enforced in one place
(`tools/memory.go:124-134` → `gate.Record`).

```
memory tool            gate.Record            gate.Add / gate.Apply          store.Put
  params        →   route by op        →   CheckContent (sanitize + scan)  →  Normalized()
                                            inferred → StatusPending            derive title
                                            NewID                              signFor()  ← the seal
                                            (tag enrichment if empty)           WriteEntry (atomic, .bak)
                                            near() neighbours                   indexEntry/putRow (one tx)
                                            classify() → Op
                                            BucketFor → decide (policy/dial)
                                            ask → permission / queue proposal
                                            commit
```

Notable points:

- **Content is scanned on the way in** — injection tokens and credentials are
  refused before a file is ever written (`gate.go:183`, `security.go:55-89`).
- **Inferred content is forced to `pending`** and structurally cannot reach the
  always-injected window (`gate.go:187-193`).
- **`Normalized()`** fills defaults so a caller cannot produce a half-formed
  record: `type=fact`, `status=active`, `owner=agent`, `trust=0.5`, plus tag
  canonicalization and dedupe (`doc.go:190-218`).
- **`Put` signs the entry before it writes the markdown**, so the file carries
  the seal a rebuild would reproduce (`store.go:720-747`, `signFor` at
  `integrity.go:234`).
- The index upsert re-derives `trust`/`status` from the row for non-system
  writes, so a hand-edited frontmatter value cannot smuggle elevated trust into
  the index (`store.go:791-802`).

`WriteEntry` is an atomic temp+rename whole-file replacement that keeps a
`.bak.<ts>` when the existing file would not round-trip — which is what makes an
agent rewrite safe next to a person editing the same vault in Obsidian
(`frontmatter.go:183-225`).

---

## The read path: Tier A and Tier B

Memory reaches the model by two distinct routes.

### Tier A — the always-injected window

`Store.TierABlock` (`lifecycle.go:107-185`) assembles a small, budgeted block
that is present in every prompt. Candidates must be `asserted = 1`, `status =
'active'`, not quarantined, and promotable (`lifecycle.go:62-78`). Promotion is
index arithmetic (it spends no model call): an entry is admitted if it is
pinned, or — when auto-promotion is on — if its `hot_score` clears
`PromoteThreshold` and its `trust` clears `AutoPromoteMinTrust`. Two guards keep
the window stable and bounded:

- **Hard byte budget** — the whole block is trimmed to `MaxInjectBytes` by
  dropping the lowest-scoring entries, never by warning (`store.go:64-68`).
- **Share ceiling** — the non-pinned path can claim at most `AutoPromoteSharePct`
  of the budget, so a recall-flood can never evict a curated pinned set.
- **Hysteresis** — an entrant must beat an eviction victim by `HysteresisDelta`
  so two topics cannot ping-pong the block and bust the provider prefix cache.

It is injected once per session, frozen for that session's prefix-cache
stability, and re-spliced across compaction (`tools/memory.go:307-386`).

### Tier B — on-demand recall

The agent pulls memory explicitly through `memory_search` (BM25 over the FTS
body, metadata filters, trust-ranked) and `memory_read` (full bodies by id, the
last rung of the index→summary→body ladder). Underneath these are `Search`,
`Get`, `Neighbors` (one hop over the `edges` graph), `ByThread`, and the
mutators (`SetStatus`, `SetTrust`, `SetPinned`, `AddNote`).

**Every read path funnels through a verification gate.** `queryEntries` drops
any row whose tamper seal no longer recomputes, so a row tampered directly in the
database is held out of the prompt even if it was never re-indexed after the
edit (`store.go:1131-1149`; the same `entryVerifies` check sits in the search
and neighbor paths). With the seal off the gate admits every row, which is the
pre-seal behaviour byte-for-byte.

---

## Configuration

Memory is configured by the root-level `memory` block in `phosphor.json`
(a sibling of `providers`/`agents`). `EnabledOrAuto` defaults the feature **on**;
`provider: "off"|"none"` or `enabled: false` turns it off (`config.go:1439-1453`).

The tuning knobs the store reads (the store takes them as `Settings` so it stays
free of a config dependency), with shipped defaults from
`DefaultSettings` (`store.go:138-150`):

| Setting | Default | Meaning |
|---------|---------|---------|
| `max_inject_bytes` | `8192` | budget for the always-injected block; values above `32768` are clamped to that ceiling |
| `max_unused_days` | `90` | age unused entries out to `cold` |
| `hysteresis_delta` | `0.15` | margin an entrant must beat an eviction victim by |
| `promote_threshold` | `1.0` | `hot_score` needed to consider an entry for promotion |
| `auto_promote_min_trust` | `0.75` | trust floor the auto-promotion path must clear |
| `auto_promote_share_pct` | `50` | max share of the budget the non-pinned path may claim |
| `thread_hint_max_lines` | `5` | cap on the names-only continuation hint |
| `write_retries` | `24` | replays of a write contended by another instance |

One tri-state still defaults off because it costs something: `distill` spends a
model call (`DistillEnabled`, `config.go:1476-1482`) and is on only when
explicitly asked for. The tamper seal (`IntegrityEnabled`, `config.go`; see
[INTEGRITY.md](INTEGRITY.md)) defaults **on** — the sealed index is the posture
the system runs under, and the key it mints announces itself for backup the
session it is created. `auto_promote` also defaults on (it is free), and its
`false` collapses Tier A to a pins-only posture.

---

## Managing it from the TUI

The `/memory` slash family inspects and steers a vault. There is no `/memory
status`/`list`/`search` keyword — bare `/memory` is status, and `/memory sources
<query>` is the corpus search. Every command runs under an 8s timeout
(`ui/model/memory.go:23`).

| Command | What it does |
|---------|--------------|
| `/memory` | status: corpus counts, injected/cap bytes, vault path, active threads |
| `/memory sources [query]` | with a query: a corpus search card; without: this session's provenance |
| `/memory fsck` | full rescan, reconciles the index from the markdown |
| `/memory budget` | injected bytes/% of cap, corpus + tag counts, by-type breakdown |
| `/memory review` | list unconfirmed drafts and queued proposals with their decision ids |
| `/memory review confirm\|ignore\|retire <id…>` | answer a draft (active / cold / tombstone) or a proposal (accept / decline) |
| `/memory policy [rule]` | lists `type:policy` entries; with an arg writes one as human-asserted |
| `/memory pin <id…>` / `unpin` | move an entry across the Tier-A line |
| `/memory demote <id…>` | unpin and drop trust to `0.2` |
| `/memory dispute <id…>` | drop trust and add a "verify before relying on it" note |
| `/memory retire <id…>` | tombstone an entry |
| `/memory cite <id…>` | print one-line provenance per id |
| `/memory key [status\|show\|restore\|rotate]` | the tamper-seal key — see [INTEGRITY.md](INTEGRITY.md) |

(`ui/model/memory.go:29-54`, dispatch to `handleMemorySlashCommand` in
`ui/model/ui.go:4259`.)

### The Memory sidebar panel

The right-hand panel is a glance surface, not an inspection surface: three lines
of stats (`memoryInfo`, `ui/model/memory.go:112`), refreshed off the store at
most every two seconds, with the per-entry detail deliberately left to
`/memory sources` and the vault files themselves. A panel in a healthy vault
looks like this:

```
Memory
Inject 3.1/8 KB ▓▓▓▓░░░░░░
42 active · 3 pending · 7 recalled
```

| Field | Meaning |
|-------|---------|
| `Inject 3.1/8 KB` + bar | Bytes the always-injected Tier A block currently costs against the effective cap, and the fill meter of that budget (`Injected`/`InjectLimit` from `Report`, `internal/memory/read.go`). These bytes ride on every prompt. When the bar is full, further entries — the lowest-scoring ones — are trimmed from the block, so a fresh pin may quietly not make the window until another is unpinned. |
| `42 active` | Corpus entries with status `active` across both banks (project + global): alive, recallable, and eligible to be injected. Retired tombstones, quarantined entries, and pending drafts are not in this number. |
| `3 pending` | Drafts written as unconfirmed inferences, waiting on a human. The `memory` tool stores anything it did not witness as `pending`, and a pending entry can never enter the always-injected window; confirming it (`/memory cite <id>` to inspect first) promotes it into the active corpus. Nonzero here means a review queue. |
| `7 recalled` | Distinct memories this session has put in front of the model so far — every entry a memory tool result has surfaced (write, search, or read), counted once per entry id, whatever role it carried. Session-scoped, and derived from the stored transcript, so a re-opened session reports what actually happened in it. `/memory sources` (no query) is the expanded version with summaries and citations. |

Reviewing the pending count is deliberate, and the review surface is
`/memory review`: a bare listing of the drafts and queued proposals, with
`confirm`, `ignore`, and `retire` to answer them by id. It exists because
nothing else closes that loop — the aging pass only touches rows that have
been used (`lifecycle.go`'s cold path gates on `last_used`), and a draft is
never recalled, so an unreviewed draft would otherwise sit pending forever.

What the review surface does *not* need is a frontmatter edit, and frontmatter
will not do the job either way: `status` and `trust` are the system-owned lane.
On any re-index of an existing row the index row wins for those fields and a
hand edit is re-indexed away as drift (`store.go:822-839`) — with or without
the seal — which is also what stops a tampered file from promoting itself. The
seal guards the *content* fields: a hand edit to a signed field fails verify
and quarantines the entry (`store.go:815-821`, see
[INTEGRITY.md](INTEGRITY.md)). So the file stays human-authoritative for what
an entry says, never for where it stands in its lifecycle.

The two decision doors are:

1. **`/memory review`** — lists drafts (`id [type] in "thread" — summary
   (why)`) and proposals (`#number op [bucket] — summary`).
   `/memory review confirm|ignore|retire <id…>` answers them: confirming a
   draft promotes it to `active` at the confirmed trust level, ignoring it
   makes it `cold` (searchable forever, never injected), retiring tombstones
   it. Proposals decided this way commit their queued bytes on a confirm and
   feed the gate's per-bucket risk learning on a decline.
2. **Through the agent** — the same lane via `memory(op=confirm, id=…)`, which
   rides the ordinary tool-permission prompt, so the human is still the one
   signing even when the agent drives.

And one door that is just leaving it: **do nothing.** Pending drafts cannot
reach a prompt and nothing ages them out; the count keeps waiting for you.

The panel's voice stays quiet while things are healthy. A seal problem speaks
up instead, as one extra line: amber `⚠ N unsigned` when entries lack a valid
signature under an enabled tamper seal, red `integrity key missing` when the
seal is on without its key, and red `quarantined N` when poisoned-looking
entries stand held out of recall (see [INTEGRITY.md](INTEGRITY.md)). If none of
those lines is visible, nothing needs you.

---

## Further reading

- [INTEGRITY.md](INTEGRITY.md) — the tamper seal: the HMAC key, what is signed
  and what is deliberately exempt, the threat model, and key operations.
- [POISONING.md](POISONING.md) — the defense against a poisoned memory entry
  reaching the system prompt: sanitization, the write-time content scan, the
  single-writer guard, the inferred/`pending` rule, and quarantine.
