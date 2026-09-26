package memory

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// openTestStore opens a store over throwaway vaults so a test never touches the
// user's real ~/.phosphor/memory directory.
func openTestStore(t *testing.T, settings Settings) *Store {
	t.Helper()
	s, err := Open(OpenOptions{
		GlobalDir:    t.TempDir(),
		WorkspaceDir: t.TempDir(),
		Settings:     settings,
		Shared:       false,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// noIntegritySettings is the explicit opt-out from the tamper seal, which ships
// on. Tests that exercise the hand-edit machinery — the drift guards, the watcher
// adoption, the first-index adoption of fixture files — ask for it: under the
// seal, changing entry bytes from outside a system write is exactly the tampering
// the seal refuses, so the unsigned posture is the one those paths can be tested
// through. The seal's own posture has its coverage in the integrity tests.
func noIntegritySettings() Settings {
	s := DefaultSettings()
	off := false
	s.Integrity = &off
	return s
}

func TestPutAndSearchRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openTestStore(t, DefaultSettings())

	yes := true
	e := Entry{
		ID:       NewID("release-flow", "Ship the indexer behind a feature flag"),
		Type:     TypeDecision,
		Thread:   "release-flow",
		Summary:  "Ship the indexer behind a feature flag",
		Body:     "The FTS5 indexer is gated until the provenance dashboard lands.",
		Tags:     []string{"fts", "rollout"},
		Status:   StatusActive,
		Asserted: &yes,
	}
	require.NoError(t, s.Put(ctx, e))

	hits, err := s.Search(ctx, SearchQuery{Query: "FTS5 indexer", Limit: 5})
	require.NoError(t, err)
	require.NotEmpty(t, hits, "a freshly written entry must be searchable")
	require.Equal(t, e.Thread, hits[0].Thread)

	byThread, err := s.ByThread(ctx, "release-flow", 10)
	require.NoError(t, err)
	require.Len(t, byThread, 1)

	got, err := s.Get(ctx, hits[0].ID)
	require.NoError(t, err)
	require.Len(t, got, 1)
	// A second read must be able to see the recall counter advance; the first read
	// returns the row as stored, so advance it once more before asserting.
	_, _ = s.Get(ctx, hits[0].ID)
	again, err := s.Get(ctx, hits[0].ID)
	require.NoError(t, err)
	require.Greater(t, again[0].RecallCount, 0, "reading an entry records a recall")
}

func TestRetiredEntriesAreNotSearched(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openTestStore(t, DefaultSettings())

	yes := true
	e := Entry{
		ID:   NewID("t", "Prefer the FTS5 path for search"),
		Type: TypePreference, Thread: "t", Summary: "Prefer the FTS5 path for search",
		Body: "search uses sqlite fts5", Status: StatusActive, Asserted: &yes,
	}
	require.NoError(t, s.Put(ctx, e))
	require.NoError(t, s.SetStatus(ctx, e.Scope, e.ID, StatusRetired, ""))

	// The row survives as a tombstone...
	_, err := s.Get(ctx, e.ID)
	require.NoError(t, err)
	// ...but it is no longer active, so default search (active-only) hides it.
	hits, err := s.Search(ctx, SearchQuery{Query: "fts5 sqlite search", Limit: 5})
	require.NoError(t, err)
	for _, h := range hits {
		require.NotEqual(t, e.ID, h.ID, "a retired entry must not surface in search")
	}
}

func TestTierAOnlyIncludesAssertedEntries(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openTestStore(t, DefaultSettings())

	yes, no := true, false
	asserted := Entry{
		Type: TypeDecision, Thread: "rel", ID: NewID("rel", "asserted decision"),
		Summary: "The rollout uses blue-green deploys", Body: "blue green deploys",
		Status: StatusActive, Asserted: &yes, Trust: 0.9,
	}
	inferred := Entry{
		Type: TypeDecision, Thread: "rel", ID: NewID("rel", "inferred guess"),
		Summary: "Guessed that canary is required", Body: "canary required guess",
		Status: StatusActive, Pinned: true, Asserted: &no,
	}
	require.NoError(t, s.Put(ctx, asserted))
	require.NoError(t, s.Put(ctx, inferred))
	require.NoError(t, s.RefreshLifecycle(ctx))

	block, err := s.TierABlock(ctx)
	require.NoError(t, err)
	require.Contains(t, block, asserted.ID, "an asserted hot entry belongs in the injected window")
	require.NotContains(t, block, inferred.ID, "an inferred entry may never reach Tier A, even pinned")
}

func TestTierABlockRespectsByteBudget(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	settings := DefaultSettings()
	settings.MaxInjectBytes = 1000
	settings.ThreadHintMaxLines = 0 // drop the hint so the budget is only entry lines
	s := openTestStore(t, settings)

	yes := true
	// A corpus that clearly overflows the budget: a dozen decision entries, each
	// with a long-ish summary.
	pad := strings.Repeat("deploys ", 18)
	pinnedID := NewID("cap", "the pinned one")
	var allIDs []string
	for i := range 12 {
		summary := fmt.Sprintf("pinned decision marker alpha one two three four five six seven eight nine ten %s", pad)
		if i == 0 {
			summary = fmt.Sprintf("UNIQUEPINNEDSENTINEL %s", pad)
		}
		e := Entry{
			Type: TypeDecision, Thread: fmt.Sprintf("t%d", i),
			ID: NewID(fmt.Sprintf("t%d", i), summary), Summary: summary,
			Body: strings.Repeat("body ", 20), Status: StatusActive, Asserted: &yes,
			Pinned: i == 0,
			// The auto path carries a trust floor the unreviewed 0.5 default does
			// not clear; these fixtures stand for confirmed content, and the point
			// of the test is the budget trim, not the gate.
			Trust: 0.8,
		}
		if i == 0 {
			pinnedID = e.ID
		}
		allIDs = append(allIDs, e.ID)
		require.NoError(t, s.Put(ctx, e))
	}
	require.NoError(t, s.RefreshLifecycle(ctx))

	block, err := s.TierABlock(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, block)

	// The pinned, highest-ranked survivor must be present...
	require.Contains(t, block, pinnedID, "the pinned entry is the last thing eviction claims")
	// ...and the budget must actually have trimmed the tail of the corpus.
	included := 0
	for _, id := range allIDs {
		if strings.Contains(block, id) {
			included++
		}
	}
	require.Less(t, included, len(allIDs), "over-budget entries are trimmed, not injected")
	require.Greater(t, included, 0, "at least the top entries are injected")
	// The whole block stays a bounded distance from the ceiling; the corpus it drew
	// from is many times larger, which is the point of the cap.
	require.Less(t, len(block), settings.MaxInjectBytes+len(tierAOpen)+len(tierAPreamble)+len(tierAClose)+128)
}

func TestGateRefusesNonPrimaryWrites(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openTestStore(t, DefaultSettings())

	g := NewGate(s, nil, DefaultPolicy())

	yes := true
	req := WriteRequest{
		SessionID: "sub",
		Primary:   false, // a subagent / cron context
		Entry: Entry{
			Type: TypeDecision, Summary: "Some throwaway goal", Body: "subagent scratch",
			Asserted: &yes,
		},
	}
	out, err := g.Add(ctx, req)
	require.NoError(t, err)
	require.Equal(t, StatusRefused, out.Status)

	// Recall still works for the same non-primary context: reads are never gated.
	require.NoError(t, s.Put(ctx, Entry{
		ID:   NewID("readable", "readable fact"),
		Type: TypeFact, Summary: "readable fact", Body: "recall works anywhere", Status: StatusActive, Asserted: &yes,
	}))
	hits, err := s.Search(ctx, SearchQuery{Query: "recall works", Limit: 5})
	require.NoError(t, err)
	require.NotEmpty(t, hits)
}

func TestGateInfersOpsAndBlocksInferredPromotion(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openTestStore(t, DefaultSettings())

	g := NewGate(s, nil, Policy{AskMode: AskModeAuto, Adaptive: false})

	// An inferred decision asked for as active/pinned must be forced to a pending
	// Tier B draft, never committed as an asserted hot-set write.
	out, err := g.Add(ctx, WriteRequest{
		SessionID: "s", Primary: true,
		Entry: Entry{Type: TypeDecision, Pinned: true, Status: StatusActive, Summary: "a plausible guess", Body: "guess body"},
	})
	require.NoError(t, err)
	require.NotEqual(t, StatusCommitted, out.Status, "an inferred write cannot be auto-committed")

	got, err := s.Get(ctx, out.ID)
	require.NoError(t, err)
	require.Empty(t, got, "the inferred candidate was queued, not written")
}

func TestClassifyPureHelpers(t *testing.T) {
	t.Parallel()

	yes := true
	base := Entry{
		Type: TypeDecision, Thread: "x", Summary: "Use SQLite for the index",
		Body: "we use sqlite for the local index", Asserted: &yes,
	}

	// No neighbours means it is new.
	require.Equal(t, OpAdd, classify(base, nil))

	// A near-duplicate with the same subject and no contradiction refines.
	refine := base
	refine.Body = "we use sqlite for the local index and store blobs there too"
	require.Equal(t, OpRefine, classify(refine, []Entry{base}))

	// A direct contradiction with a clean replacement supersedes.
	conflict := Entry{
		Type: TypeDecision, Thread: "x", Summary: "Use SQLite for the index",
		Body: "we no longer use sqlite, switched to duckdb for the local index", Asserted: &yes,
	}
	require.Equal(t, OpSupersede, classify(conflict, []Entry{base}))

	// Pure predicate helpers.
	require.True(t, sameSubject(base, refine))
	require.False(t, sameSubject(base, Entry{Type: TypeFact, Summary: "totally unrelated zebra"}))
	require.True(t, contradicts(conflict, base))
	require.False(t, contradicts(refine, base))
	require.True(t, isScoped(Entry{Summary: "scoped to the backend only"}))
}

func TestPolicyHelpers(t *testing.T) {
	t.Parallel()

	yes := true

	require.True(t, ValidAskMode("ask"))
	require.True(t, ValidAskMode("AUTO"))
	require.False(t, ValidAskMode("sometimes"))

	def := DefaultPolicy()
	require.Equal(t, AskModeBalanced, def.AskMode)
	require.Greater(t, def.MinSamples, 0)

	require.Equal(t, "archive-adds", categoryFor(OpAdd, Entry{Type: TypeFact, Asserted: &yes}, "add/fact/B/project"))
	require.Equal(t, "hot-set", categoryFor(OpAdd, Entry{Type: TypeDecision}, "add/decision/A/project"))
	require.Equal(t, "inferred", categoryFor(OpAdd, Entry{Type: TypeFact}, "add/fact/B/project"))

	require.NotEmpty(t, SuggestCategory("policy here"))
	require.Empty(t, SuggestCategory("zzzz-unlikely-token"))

	require.Contains(t, BucketFor(OpAdd, Entry{Type: TypeFact, Pinned: true}), "/A/")
}

func TestOpenClampsARunawayInjectBudgetToTheCeiling(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	const runaway = 4 * 1024 * 1024
	s := openTestStore(t, Settings{MaxInjectBytes: runaway})
	stats, err := s.Report(ctx)
	require.NoError(t, err)
	require.Equal(t, MaxInjectBytesCeiling, stats.InjectLimit,
		"the always-injected block is a standing context tax, so config may not size it past the ceiling")
	require.Equal(t, runaway, stats.InjectLimitRequested,
		"the clamp has to stay reportable: what was asked for survives beside what is enforced")

	const sane = 6 * 1024
	s = openTestStore(t, Settings{MaxInjectBytes: sane})
	stats, err = s.Report(ctx)
	require.NoError(t, err)
	require.Equal(t, sane, stats.InjectLimit)
	require.Equal(t, sane, stats.InjectLimitRequested,
		"a budget under the ceiling is the config's own, unmutated")

	s = openTestStore(t, Settings{})
	stats, err = s.Report(ctx)
	require.NoError(t, err)
	require.Equal(t, DefaultSettings().MaxInjectBytes, stats.InjectLimit)
	require.Zero(t, stats.InjectLimitRequested, "nobody asked for a budget beyond the shipped default")
}

func TestClampInjectBytes(t *testing.T) {
	t.Parallel()
	require.Equal(t, MaxInjectBytesCeiling, ClampInjectBytes(MaxInjectBytesCeiling+1))
	require.Equal(t, MaxInjectBytesCeiling, ClampInjectBytes(MaxInjectBytesCeiling))
	require.Equal(t, 4096, ClampInjectBytes(4096))
	require.Zero(t, ClampInjectBytes(0), "zero must stay recognizable as unset")
	require.Equal(t, -5, ClampInjectBytes(-5), "negatives must stay recognizable as unset")
}

func TestListPendingShowsTheOperatorTheDraftsTheAgentLadderCannotSee(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openTestStore(t, DefaultSettings())

	put := func(id, created string, status Status) {
		require.NoError(t, s.Put(ctx, Entry{
			ID: id, Type: TypeDecision, Thread: "review",
			Summary: "summary " + id, Body: "body " + id,
			Created: created, Status: status, Scope: ScopeProject,
		}))
	}
	put("first-draft", "2026-09-24T09:00:00Z", StatusPending)
	put("second-draft", "2026-09-25T09:00:00Z", StatusPending)
	put("live-entry", "2026-09-25T10:00:00Z", StatusActive)

	list, err := s.ListPending(ctx, 10)
	require.NoError(t, err)
	require.Equal(t, 2, len(list))
	require.Equal(t, "second-draft", list[0].ID, "the newest draft leads: review reads like an inbox")
	for _, e := range list {
		require.NotEqual(t, "live-entry", e.ID, "the live corpus is not review's subject")
		require.Equal(t, ScopeProject, e.Scope, "review has to know which bank each draft came from")
	}

	list, err = s.ListPending(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, 1, len(list), "the console bounds its page like every other listing surface")
}
