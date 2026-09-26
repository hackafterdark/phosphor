package memory

import (
	"context"
	"database/sql"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// onIntegrity is the tri-state "explicitly on" value the store reads to turn the
// tamper seal on. It is a pointer because nil (off) and false (off) are distinct
// from true in the rest of the settings surface.
func onIntegrity() *bool { on := true; return &on }

func ptrTrue() *bool { yes := true; return &yes }

// openStoreAt opens a store over caller-chosen throwaway dirs so two handles can
// share one pair of vaults the way the shared-store cache shares them in the app,
// while an isolated test still never touches the real ~/.phosphor.
func openStoreAt(t *testing.T, settings Settings, globalDir, workspaceDir string) *Store {
	t.Helper()
	s, err := Open(OpenOptions{
		GlobalDir:    globalDir,
		WorkspaceDir: workspaceDir,
		Settings:     settings,
		Shared:       false,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// integrityKeyFile is the path the store keeps its signing key at, for a test that
// needs to reach or remove it.
func integrityKeyFile(s *Store) string {
	return filepath.Join(s.vaultKeyDir(), integrityKeyFileName)
}

func integritySettings() Settings {
	s := DefaultSettings()
	s.Integrity = onIntegrity()
	return s
}

// entryFile is the on-disk path of an entry's markdown, mirroring how Put names
// the file so a test can reach the bytes the seal is written into.
func entryFile(s *Store, scope Scope, id string) string {
	return EntryPath(s.VaultDir(scope), id)
}

// sealOf reads the phosphor.mac currently on an entry's file, which is the seal a
// rebuild would carry.
func sealOf(t *testing.T, s *Store, scope Scope, id string) string {
	t.Helper()
	e, err := ParseEntry(entryFile(s, scope, id))
	require.NoError(t, err)
	return e.Mac
}

// rewriteFile loads an entry's file, mutates it in the way a caller describes, and
// writes the bytes back WITHOUT touching the seal. That is precisely the tamper the
// feature is meant to catch: content changed under a seal that was not re-made.
func rewriteFile(t *testing.T, s *Store, scope Scope, id string, mutate func(*Entry)) {
	t.Helper()
	path := entryFile(s, scope, id)
	e, err := ParseEntry(path)
	require.NoError(t, err)
	mutate(&e)
	out, err := Serialize(e)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, out, 0o644))
}

// ---------------------------------------------------------------------------
// Canonical form: sign / verify / tamper
// ---------------------------------------------------------------------------

func baseEntry() Entry {
	yes := true
	return Entry{
		ID:       "integrity-seal",
		Type:     TypeDecision,
		Thread:   "integrity",
		Summary:  "The release is gated behind the green suite",
		Body:     "Only a green suite may ship the release.",
		Tags:     []string{"release", "gate"},
		Created:  "2026-01-01T00:00:00Z",
		Status:   StatusActive,
		Asserted: &yes,
	}
}

func TestMACSignVerifyRoundTrip(t *testing.T) {
	t.Parallel()
	key := []byte("0123456789abcdef0123456789abcdef") // 32 bytes
	e := baseEntry()
	e.Mac = signEntry(key, e)
	require.NotEmpty(t, e.Mac)
	require.True(t, verifyEntry(key, e), "a freshly signed entry verifies under its key")
}

func TestMACDetectsContentTampering(t *testing.T) {
	t.Parallel()
	key := []byte("0123456789abcdef0123456789abcdef")
	for _, tc := range []struct {
		name   string
		mutate func(*Entry)
	}{
		{"body", func(e *Entry) { e.Body = "Only a green suite may ship the release, and the hotfix too." }},
		{"summary", func(e *Entry) { e.Summary = "The release is NOT gated" }},
		{"id", func(e *Entry) { e.ID = "someone-elses-id" }},
		{"thread", func(e *Entry) { e.Thread = "attacker" }},
		{"type", func(e *Entry) { e.Type = TypeFact }},
		{"source", func(e *Entry) { e.Source = "attacker@host" }},
		{"asserted", func(e *Entry) { yes := true; e.Asserted = &yes; e.Body += " (now asserted)" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := baseEntry()
			e.Mac = signEntry(key, e)
			require.True(t, verifyEntry(key, e))
			tc.mutate(&e)
			require.False(t, verifyEntry(key, e), "tampering the %s must break the seal", tc.name)
		})
	}
}

func TestMACExemptsHumanRegions(t *testing.T) {
	t.Parallel()
	key := []byte("0123456789abcdef0123456789abcdef")

	t.Run("title", func(t *testing.T) {
		e := baseEntry()
		e.Title = "Release gate"
		e.Mac = signEntry(key, e)
		e.Title = "A person relabelled it in Obsidian"
		require.True(t, verifyEntry(key, e), "the display title is not sealed; a human may relabel it")
	})
	t.Run("notes", func(t *testing.T) {
		e := baseEntry()
		e.Mac = signEntry(key, e)
		e.Notes = "Human added: ping the platform team before enabling."
		require.True(t, verifyEntry(key, e), "the Notes region is the human's to write in")
	})
}

func TestMACRejectsMissingAndForeignSeal(t *testing.T) {
	t.Parallel()
	key := []byte("0123456789abcdef0123456789abcdef")
	other := []byte("ffffffffffffffffffffffffffffffff")

	e := baseEntry()
	require.False(t, verifyEntry(key, e), "an entry with no seal does not verify")

	e.Mac = signEntry(other, e)
	require.False(t, verifyEntry(key, e), "a seal made under a different key does not verify")

	e.Mac = "not-hex"
	require.False(t, verifyEntry(key, e), "a malformed seal does not verify")
}

func TestMACCanonicalFormIsInjective(t *testing.T) {
	t.Parallel()
	key := []byte("0123456789abcdef0123456789abcdef")
	// A body that smuggles in a second field's separator must not be able to masquerade
	// as that field, because the reader trusts the length prefix, not the delimiter.
	e := baseEntry()
	e.Body = "fine\nsummary[26]:forged content here"
	e.Mac = signEntry(key, e)
	require.True(t, verifyEntry(key, e))

	forged := baseEntry()
	forged.Summary = "forged content here"
	require.False(t, verifyEntry(key, forged) && forged.Mac == e.Mac,
		"a crafted body must not collide with an honest entry's seal")
}

// ---------------------------------------------------------------------------
// Off-mode byte-compatibility: the seal must be invisible when it is off
// ---------------------------------------------------------------------------

func TestSerializeOmitsSealWhenUnsigned(t *testing.T) {
	t.Parallel()
	e := baseEntry()
	e.Mac = "" // an unsigned vault never stamps a seal
	out, err := Serialize(e)
	require.NoError(t, err)
	require.NotContains(t, string(out), "phosphor.mac",
		"an entry written without integrity must not carry a seal line, so its bytes match the pre-seal format")

	e.Mac = "deadbeef"
	sealed, err := Serialize(e)
	require.NoError(t, err)
	require.Contains(t, string(sealed), "phosphor.mac: deadbeef")
}

func TestStoreIntegrityOffLeavesNoSeal(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openTestStore(t, noIntegritySettings()) // the explicit opt-out is the off posture

	id := NewID("off", "No seal is written when the seal is off")
	require.NoError(t, s.Put(ctx, Entry{
		ID: id, Type: TypeFact, Thread: "off", Summary: "No seal is written when the seal is off",
		Body: "Plain as it was before the feature.", Status: StatusActive, Scope: ScopeGlobal,
	}))

	raw, err := os.ReadFile(entryFile(s, ScopeGlobal, id))
	require.NoError(t, err)
	require.NotContains(t, string(raw), "phosphor.mac")

	got, err := s.Get(ctx, id)
	require.NoError(t, err)
	require.Len(t, got, 1, "an unsigned entry is still recalled")

	status, err := s.ReportIntegrity(ctx)
	require.NoError(t, err)
	require.False(t, status.Enabled)
}

// ---------------------------------------------------------------------------
// End to end through the store
// ---------------------------------------------------------------------------

func TestStoreIntegrityQuarantinesTamperedEntry(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openTestStore(t, integritySettings())

	id := NewID("seal", "The gate blocks a tampered release")
	require.NoError(t, s.Put(ctx, Entry{
		ID: id, Type: TypeDecision, Thread: "seal", Summary: "The gate blocks a tampered release",
		Body: "Only a green suite may ship.", Status: StatusActive, Asserted: ptrTrue(), Scope: ScopeGlobal,
	}))

	got, err := s.Get(ctx, id)
	require.NoError(t, err)
	require.Len(t, got, 1, "a sealed entry is recalled while its content is untouched")

	// Tamper the body under the original seal: exactly what an offline edit to the
	// vault must not be able to smuggle into the prompt.
	rewriteFile(t, s, ScopeGlobal, id, func(e *Entry) {
		e.Body = "Only a green suite may ship. EDITED BY AN ATTACKER."
	})
	require.NoError(t, s.Sync(ctx))

	got, err = s.Get(ctx, id)
	require.NoError(t, err)
	require.Empty(t, got, "a tampered seal drops the entry out of recall")

	block, err := s.TierABlock(ctx)
	require.NoError(t, err)
	require.NotContains(t, block, "ATTACKER", "the tampered body must not reach the injected window")
}

func TestStoreIntegrityTitleAndNotesEditsSurvive(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openTestStore(t, integritySettings())

	id := NewID("human", "A person edits the label and the notes")
	require.NoError(t, s.Put(ctx, Entry{
		ID: id, Type: TypeFact, Thread: "human", Summary: "A person edits the label and the notes",
		Body: "Machine prose that must stay sealed.", Notes: "first human note",
		Status: StatusActive, Scope: ScopeGlobal,
	}))

	// The two regions the design gives a human to write in must be editable without
	// tripping the seal, or Obsidian use would quarantine every note a person touches.
	rewriteFile(t, s, ScopeGlobal, id, func(e *Entry) {
		e.Title = "Relabelled by a person"
		e.Notes = "first human note\n\nand a second one, added in Obsidian"
	})
	require.NoError(t, s.Sync(ctx))

	got, err := s.Get(ctx, id)
	require.NoError(t, err)
	require.Len(t, got, 1, "editing the human regions must not quarantine the entry")
	require.Equal(t, "Relabelled by a person", got[0].Title)
}

func TestStoreIntegritySurvivesReopen(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	settings := integritySettings()
	global, workspace := t.TempDir(), t.TempDir()

	// Two handles over one pair of vaults, as the shared store cache produces in the
	// app: the second must load the same key the first created and re-verify, not mint
	// a fresh one and quarantine the corpus.
	first := openStoreAt(t, settings, global, workspace)
	id := NewID("reopen", "The seal must verify across a store restart")
	require.NoError(t, first.Put(ctx, Entry{
		ID: id, Type: TypeFact, Thread: "reopen", Summary: "The seal must verify across a store restart",
		Body: "Stable bytes.", Status: StatusActive, Scope: ScopeGlobal,
	}))
	require.NoError(t, first.Close())

	second := openStoreAt(t, settings, global, workspace)
	got, err := second.Get(ctx, id)
	require.NoError(t, err)
	require.Len(t, got, 1, "a reopened store verifies entries sealed under the persisted key")
}

func TestStoreIntegrityRotateReSealsCorpus(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openTestStore(t, integritySettings())

	ids := []string{}
	for _, text := range []string{"one", "two", "three"} {
		id := NewID("rotate", "Entry "+text+" under the rotate path")
		require.NoError(t, s.Put(ctx, Entry{
			ID: id, Type: TypeFact, Thread: "rotate", Summary: "Entry " + text + " under the rotate path",
			Body: "body " + text, Status: StatusActive, Scope: ScopeGlobal,
		}))
		ids = append(ids, id)
	}
	before := sealOf(t, s, ScopeGlobal, ids[0])

	n, err := s.RotateIntegrityKey(ctx)
	require.NoError(t, err)
	require.Equal(t, len(ids), n, "rotation re-seals every entry")

	after := sealOf(t, s, ScopeGlobal, ids[0])
	require.NotEqual(t, before, after, "the seal changes under the new key")

	for _, id := range ids {
		got, err := s.Get(ctx, id)
		require.NoError(t, err)
		require.Len(t, got, 1, "an entry re-sealed under the new key still verifies under it")
	}

	// A stale seal, produced under the retired key, must not pass under the new one.
	// A fabricated seal also changes the file bytes, so the sync is forced to re-index
	// it (restoring the pre-rotation seal verbatim would be byte-identical to an
	// already-indexed version and the hash check would rightly skip it).
	rewriteFile(t, s, ScopeGlobal, ids[0], func(e *Entry) { e.Mac = strings.Repeat("a", 64) })
	require.NoError(t, s.Sync(ctx))
	got, err := s.Get(ctx, ids[0])
	require.NoError(t, err)
	require.Empty(t, got, "a seal that does not recompute under the active key is refused")
}

// TestStoreIntegrityReSealPreservesTagsLinksAndNotes guards the rotate path against
// the data-loss the re-seal is prone to: resign rebuilds each entry from the entries
// row, which has no column for tags, links, or the human Notes region, so a naive
// re-stamp serializes all three out of the markdown. resign must carry them back from
// the on-disk file instead.
func TestStoreIntegrityReSealPreservesTagsLinksAndNotes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openTestStore(t, integritySettings())

	id := NewID("reseal", "A re-seal must not erase the unsealed human and tag fields")
	require.NoError(t, s.Put(ctx, Entry{
		ID: id, Type: TypeFact, Thread: "reseal", Summary: "A re-seal must not erase the unsealed fields",
		Body: "Machine prose that must stay sealed.", Notes: "a human note that must survive",
		Tags:   []string{"keepme", "another"},
		Links:  []string{"some-target", "2026-01-01-x"},
		Status: StatusActive, Scope: ScopeGlobal,
	}))

	before, err := ParseEntry(entryFile(s, ScopeGlobal, id))
	require.NoError(t, err)
	require.Equal(t, []string{"keepme", "another"}, before.Tags, "the tags were written as given")
	require.Equal(t, []string{"some-target", "2026-01-01-x"}, before.Links, "the links were written as given")
	require.Equal(t, "a human note that must survive", before.Notes, "the Notes region was written as given")

	if _, err := s.RotateIntegrityKey(ctx); err != nil {
		require.NoError(t, err)
	}

	after, err := ParseEntry(entryFile(s, ScopeGlobal, id))
	require.NoError(t, err)
	require.Equal(t, before.Tags, after.Tags, "re-sealing must not drop the tags")
	require.Equal(t, before.Links, after.Links, "re-sealing must not drop the links")
	require.Equal(t, before.Notes, after.Notes, "re-sealing must not drop the human Notes region")

	got, err := s.Get(ctx, id)
	require.NoError(t, err)
	require.Len(t, got, 1, "the re-sealed entry still verifies and recalls under the new key")
}

// TestStoreIntegrityReSealRestampsOverEditedNotes pins the §9 stamp's second
// writer: a human edits the Notes region after the row was last stamped, and a
// rotate re-seals through resign, which adopts the file's human fields. The stamp
// written into the file must cover the bytes the file carries at seal time, not
// the stale projection the row last indexed.
func TestStoreIntegrityReSealRestampsOverEditedNotes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openTestStore(t, integritySettings())

	id := NewID("reseal", "A re-seal over edited Notes must restamp what the file actually carries")
	require.NoError(t, s.Put(ctx, Entry{
		ID: id, Type: TypeFact, Thread: "reseal", Summary: "A re-seal over edited Notes restamps the file",
		Body: "Machine prose that must stay sealed.", Notes: "the note as first written",
		Status: StatusActive, Scope: ScopeGlobal,
	}))

	path := entryFile(s, ScopeGlobal, id)
	parsed, err := ParseEntry(path)
	require.NoError(t, err)
	parsed.Notes = "a human edited the note after the last index pass"
	out, err := Serialize(parsed)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, out, 0o644))

	if _, err := s.RotateIntegrityKey(ctx); err != nil {
		require.NoError(t, err)
	}

	after, err := ParseEntry(path)
	require.NoError(t, err)
	require.Equal(t, "a human edited the note after the last index pass", after.Notes,
		"the human edit survives the re-seal")
	require.Equal(t, sanitizedStamp(after.Body, after.Notes), after.Sanitized,
		"the file's stamp covers the bytes the file now carries, not the last indexed ones")

	list, err := s.Get(ctx, id)
	require.NoError(t, err)
	require.Len(t, list, 1, "the re-sealed entry still verifies and recalls")
	require.Equal(t, after.Sanitized, list[0].Sanitized,
		"the row carries the same stamp the re-seal wrote into the file")
}

// TestSealBlessesFilesThatPredateItsFirstPass pins the silent-amnesia footgun
// shut: the derived index is disposable, and a vault can arrive as nothing but
// markdown — a restored backup, a synced folder, an index deleted and rebuilt.
// A blessing pass that ran against an empty index would seal zero rows, set its
// guard, and leave the sync that followed to quarantine every unsigned file it
// walked, so the corpus would open as an empty memory with no explanation. The
// pass therefore syncs before it blesses, and what it finds comes back recallable.
func TestSealBlessesFilesThatPredateItsFirstPass(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	global, workspace := t.TempDir(), t.TempDir()

	entries := filepath.Join(ProjectVaultDir(workspace), EntriesDirName)
	require.NoError(t, os.MkdirAll(entries, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(entries, "restored-note.md"), []byte(
		"---\nid: restored-note\ntype: fact\nsummary: A note that arrived from the vault, not from a write\n"+
			"phosphor.status: active\n---\n\nIt existed before this store ever opened.\n"), 0o644))

	s := openStoreAt(t, integritySettings(), global, workspace)
	got, err := s.Get(ctx, "restored-note")
	require.NoError(t, err)
	require.Len(t, got, 1, "a restored corpus comes back recallable, not quarantined whole")

	status, err := s.ReportIntegrity(ctx)
	require.NoError(t, err)
	require.Zero(t, status.Unsigned, "the blessing pass sealed the file it found")
	require.Zero(t, status.Quarantined)
	require.Zero(t, status.DroppedVerify)
}

// TestHandWrittenBytesAfterTheBlessingStayQuarantined states the posture that
// replaced hand-editing as the collaboration model: the blessing window is one
// event per bank, for the corpus that predates the seal. Bytes written to disk by
// anything but a system write after it are unsigned content with no witness, and
// the tamper posture holds them out of recall where the review queue — not the
// file system — is where human-authored memory enters.
func TestHandWrittenBytesAfterTheBlessingStayQuarantined(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openTestStore(t, integritySettings())

	require.NoError(t, s.Put(ctx, Entry{
		ID: NewID("blessed", "the store has had its blessing pass"), Thread: "blessed",
		Type: TypeFact, Summary: "The store has had its blessing pass", Body: "body",
		Status: StatusActive, Scope: ScopeProject,
	}))

	path := handEntryFile(t, s, ScopeProject, "after-blessing")
	require.NoError(t, os.WriteFile(path, []byte(
		"---\nid: after-blessing\ntype: fact\nsummary: A file dropped on disk after the window closed\n"+
			"phosphor.status: active\n---\n\nNo system write authored these bytes.\n"), 0o644))
	require.NoError(t, s.Sync(ctx))

	got, err := s.Get(ctx, "after-blessing")
	require.NoError(t, err)
	require.Empty(t, got, "unsigned content arriving after the blessing is held out of recall")
	status, err := s.ReportIntegrity(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(1), status.Quarantined, "and the operator can see that it was")
}

// TestVerifyDropsAreCountedWhereTheyHappen is the other half of the fail-closed
// promise: holding a tampered row out of recall has to be visible, or "fails
// closed" and "forgets everything" are the same observable. A direct write to the
// index — the exact tampering the seal exists for — must surface as a count the
// status reports, not only as a log line nobody reads.
func TestVerifyDropsAreCountedWhereTheyHappen(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openTestStore(t, integritySettings())

	e := Entry{ID: NewID("drops", "tampering must be visible not silent"), Type: TypeFact,
		Thread: "drops", Summary: "Tampering must be visible, not silent", Body: "clean body",
		Status: StatusActive, Scope: ScopeProject}
	require.NoError(t, s.Put(ctx, e))
	require.NoError(t, s.tx(ctx, s.bankFor(ScopeProject), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "UPDATE entries SET body = ? WHERE id = ?", "tampered body", e.ID)
		return err
	}))

	before, err := s.ReportIntegrity(ctx)
	require.NoError(t, err)

	got, err := s.Get(ctx, e.ID)
	require.NoError(t, err)
	require.Empty(t, got, "the tampered row is held out of recall")
	hits, err := s.Search(ctx, SearchQuery{Query: "clean", Limit: 10})
	require.NoError(t, err)
	for _, h := range hits {
		require.NotEqual(t, e.ID, h.ID)
	}

	after, err := s.ReportIntegrity(ctx)
	require.NoError(t, err)
	require.Greater(t, after.DroppedVerify, before.DroppedVerify,
		"read-time seal failures are counted where they happen")
}

// TestReportIntegrityCarriesFingerprintAndMintHint pins what the operator sees at
// the two moments that matter: the key's first creation, when custody has to be
// announced, and every ordinary look afterwards, where a short non-secret
// fingerprint lets a person confirm a restored key is the one the corpus was
// sealed under without printing anything about the key itself.
func TestReportIntegrityCarriesFingerprintAndMintHint(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	global, workspace := t.TempDir(), t.TempDir()

	first := openStoreAt(t, integritySettings(), global, workspace)
	st, err := first.ReportIntegrity(ctx)
	require.NoError(t, err)
	require.True(t, st.Enabled)
	require.True(t, st.KeyPresent)
	require.True(t, st.KeyJustMinted, "the session that mints the key says so")
	require.Len(t, st.KeyFingerprint, 12, "a short public fingerprint identifies the key")

	require.NoError(t, first.Close())
	second := openStoreAt(t, integritySettings(), global, workspace)
	st2, err := second.ReportIntegrity(ctx)
	require.NoError(t, err)
	require.False(t, st2.KeyJustMinted, "a later open adopts the key without re-announcing it")
	require.Equal(t, st.KeyFingerprint, st2.KeyFingerprint, "the fingerprint is stable across opens")
}

// TestLegacySealFlagKeepsACorpusUnblessable: the per-bank blessing flags superseded
// a single store-wide one, and a corpus sealed under the old form must not become
// blessable now that the guard is per bank — whoever stripped a digest to set a
// digest-free row on fire must not get a fresh blessing window from the migration.
// The legacy row is honored as having sealed everything.
func TestLegacySealFlagKeepsACorpusUnblessable(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	global, workspace := t.TempDir(), t.TempDir()

	seed := openStoreAt(t, noIntegritySettings(), global, workspace)
	id := NewID("legacy", "a corpus sealed under the old single flag")
	require.NoError(t, seed.Put(ctx, Entry{
		ID: id, Type: TypeFact, Thread: "legacy",
		Summary: "A corpus sealed under the old single flag", Body: "body",
		Status: StatusActive, Scope: ScopeGlobal,
	}))
	require.NoError(t, seed.tx(ctx, seed.bankFor(ScopeGlobal), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			"INSERT INTO meta(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value",
			integritySealKey, "1")
		return err
	}))
	require.NoError(t, seed.Close())

	sealed := openStoreAt(t, integritySettings(), global, workspace)
	got, err := sealed.Get(ctx, id)
	require.NoError(t, err)
	require.Empty(t, got, "the legacy flag stands: no blessing, and an unsigned row is not recallable")
	st, err := sealed.ReportIntegrity(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(1), st.Unsigned, "and the state stays visible to the operator")
}

// TestStoreIntegrityBootstrapAtEnablementPreservesTagsLinksAndNotes reproduces the
// exact moment the bug bites in production: an operator flips memory.integrity on over
// an already-populated vault, and the once-at-open bootstrap seals every existing
// entry through resign. The tags, links, and Notes of those notes must survive that
// first seal.
func TestStoreIntegrityBootstrapAtEnablementPreservesTagsLinksAndNotes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	global, workspace := t.TempDir(), t.TempDir()

	// Seed the corpus while the seal is still off, the way a live vault looks the
	// moment integrity is first turned on.
	off := openStoreAt(t, noIntegritySettings(), global, workspace)
	id := NewID("bootstrap", "Enabling integrity over a seeded vault must preserve its fields")
	require.NoError(t, off.Put(ctx, Entry{
		ID: id, Type: TypeFact, Thread: "bootstrap", Summary: "Enabling integrity over a seeded vault",
		Body: "Machine prose that must stay sealed.", Notes: "a human note that must survive",
		Tags:   []string{"keepme", "another"},
		Links:  []string{"some-target", "2026-01-01-x"},
		Status: StatusActive, Scope: ScopeGlobal,
	}))
	require.NoError(t, off.Close())

	// Re-opening with the seal on drives bootstrapOnce, which seals the unsigned corpus
	// through resign. Without the file re-read, this is where the tags, links, and
	// Notes would be silently stripped.
	on := openStoreAt(t, integritySettings(), global, workspace)

	e, err := ParseEntry(entryFile(on, ScopeGlobal, id))
	require.NoError(t, err)
	require.Equal(t, []string{"keepme", "another"}, e.Tags, "bootstrapping the seal must not drop the tags")
	require.Equal(t, []string{"some-target", "2026-01-01-x"}, e.Links, "bootstrapping the seal must not drop the links")
	require.Equal(t, "a human note that must survive", e.Notes, "bootstrapping the seal must not drop the Notes region")

	got, err := on.Get(ctx, id)
	require.NoError(t, err)
	require.Len(t, got, 1, "an entry bootstrapped into the seal verifies and still recalls")
}

func TestStoreIntegrityMissingKeyFailsClosed(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	// An enabled store whose key cannot be found must refuse to recall anything rather
	// than treat an unverifiable corpus as trusted.
	s := openTestStore(t, integritySettings())
	id := NewID("closed", "Missing key means nothing verifies")
	require.NoError(t, s.Put(ctx, Entry{
		ID: id, Type: TypeFact, Thread: "closed", Summary: "Missing key means nothing verifies",
		Body: "body", Status: StatusActive, Scope: ScopeGlobal,
	}))
	require.NoError(t, os.Remove(integrityKeyFile(s)))
	initErr := s.initIntegrity(ctx)
	require.Error(t, initErr, "an enabled store with a lost key has to report the loss instead of opening")
	require.ErrorIs(t, initErr, ErrNoIntegrityKey)

	writeErr := s.Put(ctx, Entry{
		ID: NewID("closed", "a keyless store must refuse the write"), Type: TypeFact,
		Thread: "closed", Summary: "a keyless store must refuse the write",
		Body: "body", Status: StatusActive, Scope: ScopeGlobal,
	})
	require.ErrorIs(t, writeErr, ErrNoIntegrityKey)

	got, err := s.Get(ctx, id)
	require.NoError(t, err)
	require.Empty(t, got, "with no usable key present, the store fails closed")
}

// ---------------------------------------------------------------------------
// Key management and BIP39
// ---------------------------------------------------------------------------

func TestOpenIntegrityRefusesToMintAKeyOverASealedCorpus(t *testing.T) {
	t.Parallel()

	globalDir := t.TempDir()
	workspaceDir := t.TempDir()
	settings := integritySettings()
	s := openStoreAt(t, settings, globalDir, workspaceDir)
	id := NewID("lost", "a sealed corpus cannot be reopened after its key disappears")
	require.NoError(t, s.Put(context.Background(), Entry{
		ID: id, Type: TypeFact, Thread: "lost", Summary: "a sealed corpus cannot be reopened after its key disappears",
		Body: "sealed body", Status: StatusActive, Scope: ScopeGlobal,
	}))
	require.NoError(t, s.Close())

	require.NoError(t, os.Remove(filepath.Join(globalDir, integrityKeyFileName)))
	_, err := Open(OpenOptions{
		GlobalDir:    globalDir,
		WorkspaceDir: workspaceDir,
		Settings:     settings,
		Shared:       false,
	})
	require.Error(t, err, "an existing sealed corpus cannot be opened by silently minting a different key")
	require.ErrorContains(t, err, ErrNoIntegrityKey.Error())
}

func TestOpenIntegrityMintsAMissingKeyForAnUnsignedCorpus(t *testing.T) {
	t.Parallel()

	globalDir := t.TempDir()
	workspaceDir := t.TempDir()
	off := openStoreAt(t, noIntegritySettings(), globalDir, workspaceDir)
	require.NoError(t, off.Put(context.Background(), Entry{
		ID: NewID("unsigned", "an unsigned vault can adopt a new key"), Type: TypeFact,
		Thread: "unsigned", Summary: "an unsigned vault can adopt a new key",
		Body: "unsigned body", Status: StatusActive, Scope: ScopeGlobal,
	}))
	require.NoError(t, off.Close())

	on := openStoreAt(t, integritySettings(), globalDir, workspaceDir)
	status, err := on.ReportIntegrity(context.Background())
	require.NoError(t, err)
	require.True(t, status.Enabled)
	require.True(t, status.KeyPresent)
	require.Zero(t, status.Quarantined, "the newly sealed legacy corpus must remain recallable")
}

func TestIntegrityKeyPersistsPerVaultDir(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	k1, minted, err := loadOrCreateKeyFor(dir)
	require.NoError(t, err)
	require.True(t, minted, "the first load of a vault without a key mints one")
	require.Len(t, k1, integrityKeyBytes)

	k2, minted, err := loadOrCreateKeyFor(dir)
	require.NoError(t, err)
	require.False(t, minted, "an existing key is adopted, not minted again")
	require.Equal(t, k1, k2, "the same key is loaded on every open, it is minted once")

	info, err := os.Stat(filepath.Join(dir, integrityKeyFileName))
	require.NoError(t, err, "the key is persisted at the vault root")
	require.NotZero(t, info.Size())
	// Unix carries an owner-only mode; Windows reports its own default, so the exact
	// bits are asserted only where they are honoured.
	if runtime.GOOS != "windows" {
		require.Equal(t, os.FileMode(0o600), info.Mode().Perm(), "the key is owner-only")
	}
}

func TestBIP39WordlistIsCanonical(t *testing.T) {
	t.Parallel()
	require.Len(t, bip39Words, 2048, "BIP39 fixes an 2048-word (2^11) list")
	require.Equal(t, "abandon", bip39Words[0])
	require.Equal(t, "zoo", bip39Words[2047])
	for i := 1; i < len(bip39Words); i++ {
		require.True(t, bip39Words[i] > bip39Words[i-1], "the list is in strictly ascending order at %d", i)
	}
}

func TestBIP39RoundTripsEveryValidSize(t *testing.T) {
	t.Parallel()
	for bytesLen := 16; bytesLen <= 32; bytesLen += 4 { // 128..256 bits, multiples of 32
		entropy := make([]byte, bytesLen)
		for i := range entropy {
			entropy[i] = byte(i*7 + 1)
		}
		phrase, err := bip39Encode(entropy)
		require.NoError(t, err)
		words := strings.Fields(phrase)
		bits := bytesLen * 8
		require.Len(t, words, (bits+bits/32)/11, "word count follows the entropy+checksum width at %d bytes", bytesLen)

		back, err := bip39Decode(phrase)
		require.NoError(t, err)
		require.Equal(t, entropy, back)
	}
}

func TestBIP39ChecksumTracksTheEntropy(t *testing.T) {
	t.Parallel()
	// BIP39 derives the checksum word from the entropy, so a one-bit change to the key has
	// to move the phrase; that is what turns a mis-transcribed backup into a caught error
	// rather than a silent neighbour key. There is no passphrase term to bind: in BIP39 the
	// passphrase stretches a phrase into a seed and is never mixed into the mnemonic itself.
	base := make([]byte, 32)
	for i := range base {
		base[i] = byte(i)
	}
	flipped := append([]byte{}, base...)
	flipped[0] ^= 0x01

	a, err := bip39Encode(base)
	require.NoError(t, err)
	b, err := bip39Encode(flipped)
	require.NoError(t, err)
	require.NotEqual(t, a, b, "a one-bit change to the entropy must change the mnemonic")

	again, err := bip39Encode(base)
	require.NoError(t, err)
	require.Equal(t, a, again, "the mnemonic is a deterministic function of the entropy")
}

func TestBIP39DetectsATamperedWord(t *testing.T) {
	t.Parallel()
	entropy := make([]byte, 32)
	for i := range entropy {
		entropy[i] = byte(i * 3)
	}
	phrase, err := bip39Encode(entropy)
	require.NoError(t, err)
	words := strings.Fields(phrase)
	require.Len(t, words, 24)

	// Replacing a word with a different valid word has to be caught by the checksum,
	// which is what turns a mis-heard or mis-transcribed backup into an error rather
	// than a silently wrong key.
	flipped := append([]string{}, words...)
	flipped[11] = bip39Words[(wordIndexOf(flipped[11])+97)%len(bip39Words)]
	require.NotEqual(t, flipped[11], words[11])
	_, err = bip39Decode(strings.Join(flipped, " "))
	require.Error(t, err)

	_, err = bip39Decode(strings.Join(words[:20], " "))
	require.Error(t, err, "a truncated phrase is rejected")

	_, err = bip39Decode(phrase + " nonsense")
	require.Error(t, err, "a word outside the list is rejected")
}

func TestIntegrityKeyMnemonicRoundTrips(t *testing.T) {
	t.Parallel()
	key := make([]byte, integrityKeyBytes)
	for i := range key {
		key[i] = byte(i * 5)
	}
	phrase, err := bip39Encode(key)
	require.NoError(t, err)
	back, err := bip39Decode(phrase)
	require.NoError(t, err)
	require.Equal(t, key, back, "a backed-up phrase restores the exact signing key")
}

func wordIndexOf(word string) int {
	for i, w := range bip39Words {
		if w == word {
			return i
		}
	}
	return -1
}

// TestBIP39MatchesTheCanonicalSpecVectors checks the encoder and decoder against the
// official BIP39 English test vectors from trezor/python-mnemonic. A round-trip only proves
// encode and decode are mutual inverses, so a self-consistent-but-wrong encoder or a wrong
// embedded wordlist sails through every other test; these vectors tie the implementation to
// the real specification and to the real 2048-word list. The vectors carry a passphrase only
// for their seed column, which BIP39 keeps out of the mnemonic, so the phrase here is a pure
// function of the entropy.
func TestBIP39MatchesTheCanonicalSpecVectors(t *testing.T) {
	t.Parallel()
	vectors := []struct{ entropy, mnemonic string }{
		{"00000000000000000000000000000000", "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about"},
		{"7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f", "legal winner thank year wave sausage worth useful legal winner thank yellow"},
		{"80808080808080808080808080808080", "letter advice cage absurd amount doctor acoustic avoid letter advice cage above"},
		{"ffffffffffffffffffffffffffffffff", "zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo wrong"},
		{"000000000000000000000000000000000000000000000000", "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon agent"},
		{"7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f", "legal winner thank year wave sausage worth useful legal winner thank year wave sausage worth useful legal will"},
		{"808080808080808080808080808080808080808080808080", "letter advice cage absurd amount doctor acoustic avoid letter advice cage absurd amount doctor acoustic avoid letter always"},
		{"ffffffffffffffffffffffffffffffffffffffffffffffff", "zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo when"},
		{"0000000000000000000000000000000000000000000000000000000000000000", "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon art"},
		{"7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f", "legal winner thank year wave sausage worth useful legal winner thank year wave sausage worth useful legal winner thank year wave sausage worth title"},
		{"8080808080808080808080808080808080808080808080808080808080808080", "letter advice cage absurd amount doctor acoustic avoid letter advice cage absurd amount doctor acoustic avoid letter advice cage absurd amount doctor acoustic bless"},
		{"ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff", "zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo vote"},
		{"9e885d952ad362caeb4efe34a8e91bd2", "ozone drill grab fiber curtain grace pudding thank cruise elder eight picnic"},
		{"6610b25967cdcca9d59875f5cb50b0ea75433311869e930b", "gravity machine north sort system female filter attitude volume fold club stay feature office ecology stable narrow fog"},
		{"68a79eaca2324873eacc50cb9c6eca8cc68ea5d936f98787c60c7ebc74e6ce7c", "hamster diagram private dutch cause delay private meat slide toddler razor book happy fancy gospel tennis maple dilemma loan word shrug inflict delay length"},
		{"c0ba5a8e914111210f2bd131f3d5e08d", "scheme spot photo card baby mountain device kick cradle pact join borrow"},
		{"6d9be1ee6ebd27a258115aad99b7317b9c8d28b6d76431c3", "horn tenant knee talent sponsor spell gate clip pulse soap slush warm silver nephew swap uncle crack brave"},
		{"9f6a2878b2520799a44ef18bc7df394e7061a224d2c33cd015b157d746869863", "panda eyebrow bullet gorilla call smoke muffin taste mesh discover soft ostrich alcohol speed nation flash devote level hobby quick inner drive ghost inside"},
		{"23db8160a31d3e0dca3688ed941adbf3", "cat swing flag economy stadium alone churn speed unique patch report train"},
		{"8197a4a47f0425faeaa69deebc05ca29c0a5b5cc76ceacc0", "light rule cinnamon wrap drastic word pride squirrel upgrade then income fatal apart sustain crack supply proud access"},
		{"066dca1a2bb7e8a1db2832148ce9933eea0f3ac9548d793112d9a95c9407efad", "all hour make first leader extend hole alien behind guard gospel lava path output census museum junior mass reopen famous sing advance salt reform"},
		{"f30f8c1da665478f49b001d94c5fc452", "vessel ladder alter error federal sibling chat ability sun glass valve picture"},
		{"c10ec20dc3cd9f652c7fac2f1230f7a3c828389a14392f05", "scissors invite lock maple supreme raw rapid void congress muscle digital elegant little brisk hair mango congress clump"},
		{"f585c11aec520db57dd353c69554b21a89b20fb0650966fa0a9d6f74fd989d8f", "void come effort suffer camp survey warrior heavy shoot primary clutch crush open amazing screen patrol group space point ten exist slush involve unfold"},
	}
	for _, v := range vectors {
		entropy, err := hex.DecodeString(v.entropy)
		require.NoError(t, err, "vector entropy %q is not hex", v.entropy)

		got, err := bip39Encode(entropy)
		require.NoError(t, err)
		require.Equal(t, v.mnemonic, got, "encoding %q", v.entropy)

		back, err := bip39Decode(v.mnemonic)
		require.NoError(t, err)
		require.Equal(t, entropy, back, "decoding the vector for %q", v.entropy)
	}
}

// ---------------------------------------------------------------------------
// Benchmarks
// ---------------------------------------------------------------------------

func BenchmarkMACSign(b *testing.B) {
	key := []byte("0123456789abcdef0123456789abcdef")
	e := baseEntry()
	for b.Loop() {
		_ = signEntry(key, e)
	}
}

func BenchmarkMACVerify(b *testing.B) {
	key := []byte("0123456789abcdef0123456789abcdef")
	e := baseEntry()
	e.Mac = signEntry(key, e)
	for b.Loop() {
		_ = verifyEntry(key, e)
	}
}
