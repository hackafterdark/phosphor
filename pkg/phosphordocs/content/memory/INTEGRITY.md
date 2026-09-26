# The Tamper Seal (Memory Integrity)

## What it is

Each memory entry can be sealed with a **keyed digest** — an HMAC-SHA256 over
the entry's machine-authored content — so the index can refuse to inject or
recall a row whose content was changed outside a system write. This is
**tamper-evidence, not encryption**. The note stays a human-readable Obsidian
markdown file with its prose in the clear; only its *authenticity* is sealed.

That distinction is the whole point, and it sets the expectation correctly:

| The seal gives you | The seal does not give you |
|--------------------|----------------------------|
| Detection of offline edits to a sealed field by someone **without** the key | Confidentiality — the content is plaintext; never store secrets in memory |
| A fail-closed posture when the key is missing or foreign | Protection against an attacker who can read the key (they can forge any seal) |
| Tamper-evidence across DB edits, file edits, and store reopen | Any protection when the seal is explicitly opted out (`"integrity": false` turns tamper-detection off with it) |

It is **on by default**. The derived index lives in the workspace, where
anything that can write the project can write it, so the seal is the posture
the system is built to run under. The one duty it creates — a signing key
whose custody is the operator's — is answered by minting it loudly: the first
session that creates a key announces it, and the status surfaces a short key
fingerprint until the corpus says otherwise (`Settings.IntegrityEnabled`,
`store.go`).

---

## How a seal is made and checked

Signing is a plain keyed hash — the key is mixed *into* the hash, not applied
to it afterward, so there is no "encrypt the checksum" step. The whole primitive
is `hmac.New(sha256.New, key)` over the canonical form of the entry
(`integrity.go:202-211`):

```go
func hmacTag(key, data []byte) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write(data)
	return mac.Sum(nil)
}
func signEntry(key []byte, e Entry) string { return hex.EncodeToString(hmacTag(key, macCanonical(e))) }
```

Verification recomputes the tag and compares it in **constant time** with
`hmac.Equal`, rejecting an empty, malformed, or wrong-length digest before the
compare (`integrity.go:216-225`). An empty/malformed/foreign seal therefore
never verifies, and a failed compare leaks no timing about how deep the first
difference lies.

`signFor` is a no-op when the seal is off (`Mac = ""`), which is what keeps an
unsigned vault's serialized bytes identical to what the pre-feature code wrote
(`integrity.go:231-240`).

### What is signed

`macCanonical` renders the *signed projection*: the machine-authored content, the
identity it is keyed by, and the classification that steers recall
(`integrity.go:180-200`). Each field is emitted length-prefixed as
`key[<len>]:<value>\n`, which is what makes the form **injective** — a body that
happens to contain a `\n` or `summary=` line cannot forge a second field,
because the digest is computed over byte counts, not separators (`integrity.go:161-169`).

```
phosphor-memory-mac/v1\n        (domain-separating prefix)
id[<n>]:<id>\n
type[<n>]:<normalized type>\n
thread[<n>]:<thread>\n
summary[<n>]:<summary>\n
body[<n>]:<TrimSpace(body)>\n
source / expires / supersedes / from_decision / owner / created
asserted[<n>]:<0|1>\n
```

### What is deliberately exempt

| Exempt from the seal | Why |
|----------------------|-----|
| `trust`, `status`, `hot_score`, `recall_count`, `helpful_count`, timestamps, `pinned` | The system mutates them in place on bookkeeping updates; signing them would break the digest on every such update |
| `title` | The human display label — a person relabels it freely in Obsidian |
| `notes` (the `## Notes` block) | The region the design explicitly hands the human to write in |
| `tags`, `links` | See below — a recorded decision, not an oversight |

**`tags` and `links` are exempt by a recorded decision.** `tags` maps to
Obsidian's native tag property — the single most hand-edited field in the whole
vault (the properties pane and inline `#tag` both write it) — and `links` is
free-form YAML a person may prune or repair. Neither is a column in the SQLite
row, so the machine cannot restate them on a re-seal and the agent does not own
them the way it owns the body. Sealing them would quarantine a note the moment a
person touched a tag in Obsidian, and a key rotation could not re-derive them
from the index, so the seal would fail closed against legitimate human editing.
The recall surfaces they steer (the tag `EXISTS` lateral-recall path and the
wikilink graph) are therefore trusted at the same level as the `## Notes` region
they sit beside.

The consequence to hold in mind: **tampering a sealed field is detected;
tampering an exempt field is not.** If you need `tags` or `links` sealed, they
must be added to `macCanonical` *and* persisted so the re-seal path can restate
them, and hashed as a normalized set (sorted, deduped, case-folded) so an
Obsidian reorder or a case variant does not fail the entry closed. That is a
larger change than adding a field, which is why it was not taken.

---

## Where the key lives

The signing key is a 32-byte value minted from the CSPRNG (`integrity.go:115-119`)
and stored at the **global vault root**:

```
~/.phosphor/memory/integrity.key        (mode 0600, hex-encoded)
```

- It is written as **hex**, not raw bytes, so a key whose first or last byte
  happens to be a whitespace byte cannot be silently trimmed away on read and
  fail the vault closed against a key that was never touched — the bug that
  motivated the hex storage (`integrity.go:67-71`).
- It is written **owner-only (`0600`)** and never through the shared staging
  path the entries use (`integrity.go:90-102`).
- **One key serves both banks.** The store resolves its key directory as the
  global vault's dir (`vaultKeyDir`, `integrity.go:276-281`), so project memory
  and global memory are sealed under the same key, and the vault-protection
  guard that already covers that directory covers the key too.
- It lives in the user's home, never in the repo working tree, so it is not a
  path an agent tool or a repo commit can reach.

### On Windows

`0600` is a POSIX permission bit. On Windows it is **advisory and not honored
against your own account** — the key file is protected by whatever the
`%USERPROFILE%` folder's ACLs grant, the same as your SSH keys or browser
profile. The `0600` assertion in the tests is therefore gated to non-Windows
platforms (`integrity_test.go`, `TestIntegrityKeyPersistsPerVaultDir`).

---

## The fail-closed posture

Missing or unusable key ⇒ **the store refuses to open, not just to recall**. A
sealed vault that cannot load its key fails loudly rather than degrading to a
"nothing verifies" half-state: `initIntegrity` returns the load error and
`Open` tears the handle down instead of handing back a store whose writes would
report success while every one of its rows silently quarantined at read time
(`initIntegrity`, `openStore`). Reads that survive a usable key still gate on it —
`entryVerifies` returns `false` for an enabled store holding an empty key, and
`true` for a disabled store regardless — and `Put` refuses to write a sealed
entry it could not sign (`ErrNoIntegrityKey`), so the fail-closed posture cannot
be used to grow a corpus of un-verifiable rows.

Two guards sit underneath the load so a missing key cannot do quiet damage:

- **A fresh key is never minted over a corpus that already carries seals.** On
  load, an absent key mints a new one only when no entry is sealed yet (a
  pre-seal vault adopting the feature). If any row already carries a `mac`, the
  mint is refused with `ErrNoIntegrityKey` and the message points at
  `restore`/`rotate` — minting a different key over sealed rows would orphan
  them permanently (`loadOrCreateIntegrityKey`, `corpusHasSealedEntries`).
- **Recovery stays reachable.** Because a keyless sealed vault no longer opens
  normally, the `/memory key` command opens it through a one-off **maintenance**
  handle (`OpenOptions.Maintenance`, `openMemoryMaintenanceStore`) that tolerates
  the missing key just so you can run `restore`/`rotate` against it. Restoring a
  phrase writes the key and the live handle adopts it in place
  (`ReloadIntegrityKey`) and the failed-open latch clears, so recall returns under
  the original key without a process restart.

### Trust is established at the enablement moment

The first time a bank is opened under the seal, `bootstrapOnce` seals every
entry that carries no digest, signing whatever bytes are then on disk under a
freshly minted key. That is the moment trust is granted: the pass trusts the
vault as intact when it first meets it. It **syncs before it blesses** — the
derived index is disposable, so a restored or rebuilt vault has no rows until a
sync writes them, and rows a sync writes while unsigned would otherwise sit
quarantined for exactly that; blessing signs those rows and lifts their
quarantine, so a vault restored from files opens whole instead of opening
empty. The guard is a per-bank `integrity_sealed` flag in the **global** bank's
`meta` table — the one index a workspace-level tamperer cannot reach — so each
bank is blessed exactly once and a later open cannot re-run the pass; otherwise
an attacker who deleted one file's `phosphor.mac` line would have the index
quietly re-bless the tampered bytes. Unsigned bytes arriving *after* a bank's
blessing window are held out of recall by the same posture: the review queue,
not the file system, is where human-authored memory enters
(`markBankSealed`).

### Re-sealing does not clobber the human's words

The re-seal path (`resign`, used by bootstrap and rotation) rebuilds the entry
from the SQLite row, but the row has **no column** for `tags`, `links`, or the
human `## Notes` region — those live only in the markdown and the side tables.
A re-seal is a pure re-stamp, so the row would otherwise be materialized back
over the file and silently drop all three. `resign` therefore re-reads the file
when it is present and carries `tags`, `links`, and `notes` across
(`integrity.go:390-424`). Because those three are outside the signed projection,
adopting them cannot skew the digest, and the recall gate still verifies against
the row it scans. This is the same "read the file when present" idiom the
ordinary rewrite path uses (`read.go:381-391`).

---

## Threat model

The seal protects a specific adversary: someone who can **write to the vault
files or the SQLite database while the agent is not running, but cannot read the
key file**.

| Scenario | Covered? |
|----------|----------|
| An offline edit to a sealed field of a note or a DB row, by a party without the key | ✅ detected — the row is quarantined out of recall |
| Stripping a `phosphor.mac` line to get a tampered note re-blessed | ✅ caught — the bootstrap guard forbids re-running the pass |
| Hand-editing `trust`/`status` in frontmatter to self-elevate | ✅ the index re-derives them from the row (`store.go:791-802`); and they are exempt from the seal by design, gated separately — see [POISONING.md](POISONING.md) |
| Editing `tags`/`links`/`notes` to plant content | ❌ not detected — those fields are exempt by decision (above) |
| An attacker who can read `integrity.key` | ❌ they hold the whole property and can forge any seal |
| Full account compromise | ❌ they get the vault *and* the key — the same residual risk any on-disk secret carries |

Reduce the second and third lines the way you would any secret: keep the key
backup offline, and never write secrets into memory (it is plaintext, and the
write path refuses credentials as a backstop — see
[POISONING.md](POISONING.md#secrets-scan-on-write)).

---

## Key operations

The seal ships on. You touch the root `memory` block of `phosphor.json` to opt
out — or to opt back in after an opt-out:

```json
{
  "memory": {
    "integrity": false
  }
}
```

The gate rides the feature switch: with `memory.enabled` on (its own default),
the seal is on unless `integrity` is explicitly `false`
(`Memory.IntegrityEnabled`, `config.go`).

Then drive the key with the `/memory key` family (`memoryKeyCommand`,
`ui/model/memory.go`):

| Command | What it does |
|---------|--------------|
| `/memory key` | status: seal on/off, key present with its short fingerprint, sealed / unsigned / quarantined counts, and how many rows were held out of recall at read time this session (`ReportIntegrity`) |
| `/memory key show` | prints the key as a **BIP39** mnemonic so you can back it up offline (`IntegrityKeyMnemonic`) |
| `/memory key restore <phrase>` | writes the key back from a BIP39 phrase, adopts it into the live handle, and clears the failed-open latch so recall resumes without a restart (`RestoreIntegrityKeyFromMnemonic`, `ReloadIntegrityKey`). Reachable even when the keyless vault will not open normally, via the maintenance handle |
| `/memory key rotate confirm` | mints a fresh key and re-seals the whole corpus in one action; requires the literal word `confirm` (`RotateIntegrityKey`) |

`/memory status` prints the same integrity picture beside the corpus counts —
seal state, key fingerprint, the sealed/unsigned/quarantined tallies, a loud
line the session that first creates the key, and a count of entries held out of
recall because their seal failed at read time. The Memory sidebar warns on the
same numbers when they need action.

Back up the mnemonic the moment you enable the seal. It is the recovery path for
a lost or corrupted key: `show` it, store it like any other secret, and `restore`
it to bring every entry signed under the original key back to verifying. A lost
key fails the vault closed for recall — and the store then refuses to open
normally — but `/memory key restore` remains reachable through the maintenance
handle described above, so the phrase is what stands between a lost key and the
only true last resort: re-sealing the corpus from scratch under a fresh key,
which forfeits the tamper-evidence on every entry written before.

### The BIP39 encoding

`/memory key show` renders the raw 32 bytes as a BIP39 mnemonic over the
canonical 2048-word list (`bip39_english.txt`, `integrity.go:470-560`). It is
**passphrase-free**: the key *is* the secret, the phrase is only its text form,
so the encoding is a pure function of the key bytes and a backup phrase carries
no second secret. The trailing checksum word is the leading `entropy_bits/32`
bits of `SHA256(entropy)` — entropy alone — which is what lets `restore` catch a
single mistyped word (`integrity.go:486-495`). The encoder and decoder are
pinned against the canonical spec vectors and the embedded wordlist is verified
byte-identical to the canonical list (`integrity_test.go`,
`TestBIP39MatchesTheCanonicalSpecVectors` / `TestBIP39WordlistIsCanonical`).

---

## Related

- [OVERVIEW.md](OVERVIEW.md) — how memory works end to end.
- [POISONING.md](POISONING.md) — the other poisoning defenses (sanitize,
  write-time content scan, the single-writer guard, the inferred/`pending` rule,
  quarantine), of which the seal is one layer.
