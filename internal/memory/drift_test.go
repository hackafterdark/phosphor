package memory

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestSyncIfStaleRoundTripsHandWrittenFiles is the watcher round-trip promise in its
// deterministic half: a file that appears on disk with no agent write in sight is
// found by the index, tracked while a human edits it, and pruned when deleted. The
// watcher is only the accelerator over this same Sync contract.
func TestSyncIfStaleRoundTripsHandWrittenFiles(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openTestStore(t, noIntegritySettings())

	id := "hand-round-trip"
	path := handEntryFile(t, s, ScopeProject, id)
	body := func(text string) []byte {
		return []byte("---\nid: " + id + "\ntype: fact\nsummary: Widget budgets are set in the plan file\n" +
			"phosphor.status: active\nphosphor.trust: 1.0\n---\n\n" + text + "\n")
	}
	require.NoError(t, os.WriteFile(path, body("Widget budgets are set in the plan file, not in code."), 0o644))

	changed, err := s.SyncIfStale(ctx)
	require.NoError(t, err)
	require.True(t, changed, "a file the agent never wrote is still a change to the vault")

	got := onlyEntry(t, s, ctx, id)
	// The first index of a brand-new file has no row to defer to, so the file is
	// the only truth there is and it is adopted wholesale. That is what lets a
	// migrated vault and the eval corpus seed themselves from checked-in fixtures.
	require.InDelta(t, 1.0, got.Trust, 0.001, "a first index adopts what the file says")

	require.NoError(t, os.WriteFile(path, body("Widget budgets are set in the plan file. Second human edit."), 0o644))
	changed, err = s.SyncIfStale(ctx)
	require.NoError(t, err)
	require.True(t, changed, "an edit in Obsidian is stale the moment it lands")
	got = onlyEntry(t, s, ctx, id)
	require.Contains(t, got.Body, "Second human edit")

	require.NoError(t, os.Remove(path))
	changed, err = s.SyncIfStale(ctx)
	require.NoError(t, err)
	require.True(t, changed, "a vanished file is a human delete, and a delete is a change")
	list, err := s.Get(ctx, id)
	require.NoError(t, err)
	require.Empty(t, list, "the row prunes with the file it was derived from")
}

// TestWatcherIndexesTouchedFileWithoutAgentWrite is the live-event half of the same
// promise: touch a file on disk and the running watcher adopts it with nobody
// asking, firing the OnChange callbacks the session-start surfacing hangs off.
func TestWatcherIndexesTouchedFileWithoutAgentWrite(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	s := openTestStore(t, noIntegritySettings())

	w, err := NewWatcher(s)
	require.NoError(t, err)
	t.Cleanup(func() { _ = w.Stop() })

	fired := make(chan []string, 4)
	w.OnChange(func(changed []string) {
		select {
		case fired <- changed:
		default:
		}
	})
	require.NoError(t, w.Start(ctx))

	id := "watched-note"
	path := handEntryFile(t, s, ScopeProject, id)
	require.NoError(t, os.WriteFile(path, []byte(
		"---\nid: "+id+"\ntype: fact\nsummary: The watcher catches notes the agent never wrote\n"+
			"phosphor.status: active\n---\n\nNo agent write was involved in this note existing.\n"), 0o644))

	// The event is the accelerator and the row is the contract, so the row is what
	// the deadline polls for while the reported paths are accepted along the way.
	// The assertion stays the promise rather than the OS event scheduler.
	deadline := time.Now().Add(20 * time.Second)
	var saw []string
	for time.Now().Before(deadline) {
		select {
		case cs := <-fired:
			saw = append(saw, cs...)
		default:
		}
		list, err := s.Get(context.Background(), id)
		require.NoError(t, err)
		if len(list) > 0 {
			require.Contains(t, strings.ToLower(strings.Join(saw, "\n")), strings.ToLower(Slug(id)),
				"the debounced rescan has to report the file it adopted")
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("the watcher never adopted a file touched on disk within the deadline")
}

// TestReindexNeverTrustsHandEditedSystemFields is the field-ownership rule from
// §13: phosphor.* is machine namespace. trust belongs to the confirmation and
// feedback loop, status to the lifecycle pass, so a hand value in either is
// drift, and re-indexing recomputes it away.
func TestReindexNeverTrustsHandEditedSystemFields(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openTestStore(t, DefaultSettings())
	yes := true

	id := NewID("drift", "the deploy gate needs the green suite")
	require.NoError(t, s.Put(ctx, Entry{
		ID: id, Type: TypeConstraint, Thread: "drift",
		Summary: "The deploy gate needs the green suite", Body: "Only a green suite may deploy.",
		Status: StatusActive, Asserted: &yes, Trust: 0.5, Scope: ScopeProject,
	}))

	path := EntryPath(s.VaultDir(ScopeProject), id)
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	drifted := strings.ReplaceAll(string(raw), "phosphor.trust: 0.5", "phosphor.trust: 1.0")
	drifted = strings.ReplaceAll(drifted, "phosphor.status: active", "phosphor.status: retired")
	require.NotEqual(t, string(raw), drifted, "the test drifts the fields the serializer actually writes")
	require.NoError(t, os.WriteFile(path, []byte(drifted), 0o644))

	require.NoError(t, s.Sync(ctx))
	require.NoError(t, s.RefreshLifecycle(ctx))

	got := onlyEntry(t, s, ctx, id) // a drifted-to-retired status would have hidden the row from this read ladder entirely
	require.InDelta(t, 0.5, got.Trust, 0.001, "trust belongs to the confirmation loop, not to the frontmatter")
	require.Equal(t, StatusActive, got.Status, "status belongs to the lifecycle pass, not to the frontmatter")

	// The same file under system authority is still believed: the rule discriminates
	// who wrote the bytes, not whether they came from disk.
	require.NoError(t, s.SetTrust(ctx, ScopeProject, id, 0.8))
	got = onlyEntry(t, s, ctx, id)
	require.InDelta(t, 0.8, got.Trust, 0.001, "the confirmation path writes trust through the file and the row")
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(after), "0.8", "the file and the index agree on what the system decided")
}

// TestHumanRegionsSurviveSystemRewrites is the region-merge promise: an agent
// bumping one system field must not be able to truncate what a person wrote, in
// either the dedicated Notes region or the body itself.
func TestHumanRegionsSurviveSystemRewrites(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openTestStore(t, noIntegritySettings())
	yes := true

	id := NewID("regions", "the editor consults the guard first")
	require.NoError(t, s.Put(ctx, Entry{
		ID: id, Type: TypeConstraint, Thread: "regions",
		Summary: "The editor consults the vault guard before any write lands",
		Body:    "Every editor path passes the guard first.",
		Status:  StatusActive, Asserted: &yes, Scope: ScopeProject,
	}))
	path := EntryPath(s.VaultDir(ScopeProject), id)

	// A person adds a voice of their own: one line under the Notes heading and one
	// addendum inside the agent's own body region, which is what typing in
	// Obsidian actually looks like.
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	humanized := strings.ReplaceAll(string(raw),
		"Every editor path passes the guard first.",
		"Every editor path passes the guard first. (human addendum: verified against the vendor).")
	humanized += "\n## Notes\n\nHuman says: the vendor confirmed this in September.\n"
	require.NoError(t, os.WriteFile(path, []byte(humanized), 0o644))
	require.NoError(t, s.Sync(ctx))

	require.NoError(t, s.AddNote(ctx, ScopeProject, id, "Confirmed with the vendor on the guard path."))
	require.NoError(t, s.SetTrust(ctx, ScopeProject, id, 0.8))

	after, err := os.ReadFile(path)
	require.NoError(t, err)
	file := string(after)
	require.Contains(t, file, "Human says: the vendor confirmed this in September.",
		"the human Notes region survives an agent note verbatim")
	require.Contains(t, file, "(human addendum: verified against the vendor)",
		"the human voice inside the body region survives a system rewrite verbatim")
	require.Contains(t, file, "_agent: Confirmed with the vendor on the guard path._",
		"the agent appends under its own marker rather than over the human line")
	require.Contains(t, file, "0.8", "the field the rewrite existed to change did change")

	got := onlyEntry(t, s, ctx, id)
	require.Contains(t, got.Body, "(human addendum: verified against the vendor)",
		"the index reads what the merged file says, not what the agent last meant")
}

// TestWriteEntryDriftGuardKeepsBakOfNonRoundTrippingBytes is the safety net under
// the agent/human collision: when the bytes on disk will not parse or will not
// round-trip, the previous bytes are preserved as a timestamped .bak before the
// rewrite, and the index adopts the clean file only, never the backup.
func TestWriteEntryDriftGuardKeepsBakOfNonRoundTrippingBytes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openTestStore(t, noIntegritySettings())

	path := handEntryFile(t, s, ScopeProject, "corrupt-drift")
	original := []byte("id: corrupt-drift\nno frontmatter fence here at all\n")
	require.NoError(t, os.WriteFile(path, original, 0o644))

	require.NoError(t, WriteEntry(path, Entry{
		ID: "corrupt-drift", Type: TypeFact, Summary: "Clean rewrite after the drift",
		Body: "The fence came back.", Status: StatusActive,
	}))

	dir := filepath.Dir(path)
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	var backups []string
	for _, de := range entries {
		if strings.HasPrefix(de.Name(), "corrupt-drift.md.bak.") {
			backups = append(backups, filepath.Join(dir, de.Name()))
		}
	}
	require.Len(t, backups, 1, "the drifted bytes were preserved exactly once")
	kept, err := os.ReadFile(backups[0])
	require.NoError(t, err)
	require.Equal(t, string(original), string(kept), "the backup is the prior bytes verbatim")

	parsed, err := ParseEntry(path)
	require.NoError(t, err)
	require.Equal(t, "Clean rewrite after the drift", parsed.Summary)

	require.NoError(t, s.Sync(ctx))
	onlyEntry(t, s, ctx, "corrupt-drift")
	// The FTS copy indexes body, id, thread, and type, so the probe text quotes
	// the body the way a recall would rather than the headline.
	hits, err := s.Search(ctx, SearchQuery{Query: "the fence came back", Limit: 5})
	require.NoError(t, err)
	doubles := 0
	for _, h := range hits {
		if h.ID == "corrupt-drift" {
			doubles++
		}
	}
	require.Equal(t, 1, doubles, "the .bak is never indexed a second entry into existence")
}

// onlyEntry reads one live row by id and insists there is exactly one, which is
// also the assertion that a hand-drifted status did not hide it from the ladder.
func onlyEntry(t *testing.T, s *Store, ctx context.Context, id string) Entry {
	t.Helper()
	list, err := s.Get(ctx, id)
	require.NoError(t, err)
	require.Len(t, list, 1, "exactly one live row carries %q", id)
	return list[0]
}

// handEntryFile returns the vault path where a store expects the file for an id,
// creating the entries directory so a test can write it the way Obsidian would.
func handEntryFile(t *testing.T, s *Store, scope Scope, id string) string {
	t.Helper()
	dir := filepath.Join(s.VaultDir(scope), EntriesDirName)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	return EntryPath(s.VaultDir(scope), id)
}

// TestHumanTitleSurvivesSystemRewrites is the display-label half of the field-
// ownership rule: the agent mints a title from the summary once at creation,
// but from then on the file is authoritative, so a label a person overwrites in
// Obsidian flows to the row and is kept verbatim by every later system rewrite.
func TestHumanTitleSurvivesSystemRewrites(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openTestStore(t, DefaultSettings())
	yes := true

	id := NewID("labels", "the browser stores session tokens in the keystore")
	require.NoError(t, s.Put(ctx, Entry{
		ID: id, Type: TypeDecision, Thread: "labels",
		Summary: "The browser stores session tokens in the keystore",
		Body:    "Session tokens live in the keystore, never in local storage.",
		Status:  StatusActive, Asserted: &yes, Scope: ScopeProject,
	}))
	path := EntryPath(s.VaultDir(ScopeProject), id)

	// Creation filled a derived label from the summary, since nobody set one.
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(raw), "title: "+deriveTitle("The browser stores session tokens in the keystore")+"\n",
		"a write without a title mints one from the summary")

	// A person overwrites it in Obsidian: the edit flows into the row.
	relabelled := relabelEntryFile(t, raw, "Tokens live in the keystore")
	require.NoError(t, os.WriteFile(path, relabelled, 0o644))
	require.NoError(t, s.Sync(ctx))

	got := onlyEntry(t, s, ctx, id)
	require.Equal(t, "Tokens live in the keystore", got.Title,
		"title is file-authoritative: the row adopts what the person typed")

	// The agent then bumps a system-owned field through a full rewrite. The
	// human label must ride along in both the bytes and the row.
	require.NoError(t, s.SetTrust(ctx, ScopeProject, id, 0.8))

	after, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(after), "title: Tokens live in the keystore\n",
		"a system rewrite preserves the human label verbatim")
	require.Contains(t, string(after), "0.8", "the field the rewrite existed to change did change")

	got = onlyEntry(t, s, ctx, id)
	require.Equal(t, "Tokens live in the keystore", got.Title)
	require.InDelta(t, 0.8, got.Trust, 0.001)
}

// relabelEntryFile rewrites the frontmatter title line the way the Obsidian
// properties editor would, leaving every other byte of the file alone.
func relabelEntryFile(t *testing.T, raw []byte, label string) []byte {
	t.Helper()
	lines := strings.Split(string(raw), "\n")
	for i, line := range lines {
		if strings.HasPrefix(line, "title:") {
			lines[i] = "title: " + label
			return []byte(strings.Join(lines, "\n"))
		}
	}
	t.Fatal("the serialized entry file carries no title line to relabel")
	return nil
}
