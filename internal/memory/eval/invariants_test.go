package eval

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/hackafterdark/phosphor/internal/memory"
	"github.com/stretchr/testify/require"
)

// TestEvalT0TokenBudget measures the standing cost of the always-injected block over
// the seeded corpus, in the memory package's own deterministic token stream.
//
// The bar is section 11's floor. A block that quietly out-grows it turns every session
// into a tax, and because the block is the part the provider prefix cache is priced
// against, the size and the stability are the same problem wearing different clothes.
func TestEvalT0TokenBudget(t *testing.T) {
	s, _ := newCorpusStore(t, corpusSettings())
	ctx := context.Background()

	block, err := s.TierABlock(ctx)
	require.NoError(t, err)

	tokens := tokenCount(block)
	t.Logf("t0 = %d tokens over %d bytes (bar %d)", tokens, len(block), BarT0Tokens)
	require.LessOrEqual(t, tokens, BarT0Tokens, "the always-injected block has a token ceiling")

	// The block is a function of the vault and nothing else. Two renders of an
	// unchanged vault that differ by a word are a cache miss on every prompt, so this
	// is measured rather than assumed.
	again, err := s.TierABlock(ctx)
	require.NoError(t, err)
	require.Equal(t, block, again, "renders of an unchanged vault must be byte identical")

	// A session adopts a frozen window; a second session over the same vault sees the
	// same bytes, which is the whole prefix-cache argument.
	first, err := s.SnapshotFor(ctx, "eval-session-a")
	require.NoError(t, err)
	second, err := s.SnapshotFor(ctx, "eval-session-b")
	require.NoError(t, err)
	require.Equal(t, first, second, "sessions opened against an unchanged vault adopt the same window")

	// The corpus is not empty by accident: assert the block actually carries the
	// asserted fixtures, or a passing budget would only be measuring an absence.
	require.NotEmpty(t, admittedIDs(t, block), "the corpus has asserted entries that belong in the window")
}

// TestEvalInjectByteCapHolds is the codex borrow from section 8, stated as the
// invariant it is: whatever the corpus grows into, the injected block may not.
//
// It is deliberately measured on a vault pushed past its own ceiling rather than on
// the curated corpus, where the cap would never be reached and the assertion would
// pass for the wrong reason.
func TestEvalInjectByteCapHolds(t *testing.T) {
	settings := memory.DefaultSettings()
	settings.MaxInjectBytes = 1400

	dir := t.TempDir()
	s, err := memory.Open(memory.OpenOptions{GlobalDir: dir, Settings: settings, Shared: false})
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	ctx := context.Background()
	yes := true

	const writers = 60
	for i := range writers {
		require.NoError(t, s.Put(ctx, memory.Entry{
			ID:       memory.NewID("overbudget", fmt.Sprintf("padded entry %02d", i)),
			Type:     memory.TypeFact,
			Thread:   "overbudget",
			Summary:  fmt.Sprintf("Padded entry %02d carrying enough prose to be worth counting", i),
			Body:     strings.Repeat("filler ", 28) + fmt.Sprintf("%02d", i),
			Status:   memory.StatusActive,
			Pinned:   true,
			Asserted: &yes,
			Scope:    memory.ScopeGlobal,
		}))
	}
	require.NoError(t, s.RefreshLifecycle(ctx))

	stats, err := s.Report(ctx)
	require.NoError(t, err)
	require.Greater(t, stats.Hot, 1, "the vault has to be over budget for this to be a test")

	block, err := s.TierABlock(ctx)
	require.NoError(t, err)

	require.LessOrEqual(t, len(block), settings.MaxInjectBytes,
		"the cap is a trim, not a warning: the block may never exceed it")

	admitted := admittedIDs(t, block)
	require.NotEmpty(t, admitted, "trimming to nothing would pass the ceiling and fail the feature")
	require.Less(t, len(admitted), stats.Hot,
		"the vault is over budget, so entries have to have been left out")

	t.Logf("cap held: %d/%d bytes, %d of %d eligible entries admitted", len(block), settings.MaxInjectBytes, len(admitted), stats.Hot)
}

// TestEvalNonPrimaryWritesRefused is the contamination fence from section 5: a
// subagent, a cron run, or any other non-primary context may read the vault and may
// never write it. Anything below total refusal is a hole, so the bar is absolute.
func TestEvalNonPrimaryWritesRefused(t *testing.T) {
	s, _ := newCorpusStore(t, corpusSettings())
	ctx := context.Background()
	g := memory.NewGate(s, nil, memory.DefaultPolicy())

	const attempts = 8
	var refused int
	for i := range attempts {
		out, err := g.Add(ctx, memory.WriteRequest{
			SessionID: "eval-subagent",
			Primary:   false,
			Entry: memory.Entry{
				Type:     memory.TypeDecision,
				Thread:   "throwaway",
				Summary:  fmt.Sprintf("A throwaway goal from a borrowed context %d", i),
				Body:     "A subagent's transient objective must never reach the shared vault.",
				Asserted: ptr(true),
				Scope:    memory.ScopeGlobal,
			},
		})
		require.NoError(t, err)
		if out.Status == memory.StatusRefused {
			refused++
			continue
		}
		t.Errorf("non-primary write was not refused: status=%q op=%q rationale=%q", out.Status, out.Op, out.Rationale)
	}

	rate := float64(refused) / float64(attempts)
	t.Logf("non-primary refusal rate %.2f over %d attempts (bar %.2f)", rate, attempts, BarNonPrimaryRefusal)
	require.GreaterOrEqual(t, rate, BarNonPrimaryRefusal, "every non-primary write has to be refused")

	// The fence has to be a fence on the write path and not a broken store: the same
	// content from the primary context is accepted, and reading stays open throughout.
	out, err := g.Add(ctx, memory.WriteRequest{
		SessionID: "eval-primary",
		Primary:   true,
		Entry: memory.Entry{
			Type:     memory.TypeFact,
			Thread:   "throwaway",
			Summary:  "The same write from the primary context is allowed",
			Body:     "Proves the refusal above is a policy and not the store being unable to write.",
			Asserted: ptr(true),
			Scope:    memory.ScopeGlobal,
		},
	})
	require.NoError(t, err)
	require.NotEqual(t, memory.StatusRefused, out.Status, "a primary write must not be refused")

	hits, err := s.Search(ctx, memory.SearchQuery{Query: "subagent transient objective shared vault", Limit: 5})
	require.NoError(t, err)
	for _, h := range hits {
		require.NotEqual(t, "throwaway", h.Thread, "a refused write must not have left a row behind")
	}
}

// TestEvalInjectedWindowIsStable is the thrash gate. The injected block is what the
// provider prefix cache is priced against, so a window that reorders on a wobble is a
// per-session re-pay, and hysteresis exists specifically to stop it.
func TestEvalInjectedWindowIsStable(t *testing.T) {
	settings := memory.DefaultSettings()
	settings.MaxInjectBytes = 900

	dir := t.TempDir()
	s, err := memory.Open(memory.OpenOptions{GlobalDir: dir, Settings: settings, Shared: false})
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	ctx := context.Background()
	yes := true

	// Three incumbents that fit, plus one rival that does not, all scoring within the
	// hysteresis band of each other so the edge is genuinely contested.
	incumbents := []string{"alpha", "bravo", "charlie"}
	for _, name := range incumbents {
		require.NoError(t, s.Put(ctx, memory.Entry{
			ID:       memory.NewID("hysteresis", name),
			Type:     memory.TypeFact,
			Thread:   "hysteresis",
			Summary:  capitalize(name) + " holds the window open with a settled fact",
			Body:     strings.Repeat("settled ", 12) + name,
			Status:   memory.StatusActive,
			Pinned:   true,
			Trust:    0.5,
			Asserted: &yes,
			Scope:    memory.ScopeGlobal,
		}))
	}
	rivalID := memory.NewID("hysteresis", "delta")
	require.NoError(t, s.Put(ctx, memory.Entry{
		ID:       rivalID,
		Type:     memory.TypeFact,
		Thread:   "hysteresis",
		Summary:  "Delta is the challenger waiting on the edge of the budget",
		Body:     strings.Repeat("settled ", 12) + "delta",
		Status:   memory.StatusActive,
		Pinned:   true,
		Trust:    0.45,
		Asserted: &yes,
		Scope:    memory.ScopeGlobal,
	}))
	require.NoError(t, s.RefreshLifecycle(ctx))

	before := admittedIDs(t, mustBlock(t, s, ctx))
	require.NotEmpty(t, before)
	require.NotContains(t, before, rivalID, "the rival starts outside the window")

	// A nudge inside the hysteresis band is not a win: the window has to hold still,
	// which is the entire point of the margin.
	require.NoError(t, s.SetTrust(ctx, memory.ScopeGlobal, rivalID, 0.46))
	require.NoError(t, s.RefreshLifecycle(ctx))
	churned := admittedIDs(t, mustBlock(t, s, ctx))
	require.Equal(t, before, churned, "a within-band wobble must not churn the injected window")

	// A clear win is a win. If this never admits the rival, the window is frozen rather
	// than stable, and a decision the data supports is being refused.
	require.NoError(t, s.SetTrust(ctx, memory.ScopeGlobal, rivalID, 0.95))
	require.NoError(t, s.RefreshLifecycle(ctx))
	admitted := admittedIDs(t, mustBlock(t, s, ctx))
	require.NotEqual(t, before, admitted, "a rival that clearly outscores an incumbent has to be able in")

	t.Logf("window held at %d entries across %d renders of an unchanged vault", len(before), 2)
}

// mustBlock renders the window or fails the test, so the churn assertions read as
// comparisons rather than as error handling.
func mustBlock(t *testing.T, s *memory.Store, ctx context.Context) string {
	t.Helper()
	block, err := s.TierABlock(ctx)
	require.NoError(t, err)
	return block
}

func ptr(v bool) *bool { return &v }

// capitalize uppercases the first byte of an all-ASCII word. strings.Title is
// deprecated for mis-handling Unicode word boundaries, and these names are ASCII by
// construction, so the helper is both sufficient and exact.
func capitalize(word string) string {
	if word == "" {
		return word
	}
	return strings.ToUpper(word[:1]) + word[1:]
}

// TestEvalAutoPromoteTrustFloorAndFlag is the context-poisoning gate. The
// automatic path into the always-injected window has to stop at unreviewed
// trust, the flag has to be able to collapse the window to pins alone, and
// neither may touch the read path: a parked entry stays findable, never gone.
func TestEvalAutoPromoteTrustFloorAndFlag(t *testing.T) {
	ctx := context.Background()

	build := func(t *testing.T, mutate func(*memory.Settings)) (string, string, string, *memory.Store) {
		t.Helper()
		settings := memory.DefaultSettings()
		settings.MaxInjectBytes = 6000
		settings.ThreadHintMaxLines = 0
		if mutate != nil {
			mutate(&settings)
		}
		s, err := memory.Open(memory.OpenOptions{GlobalDir: t.TempDir(), Settings: settings, Shared: false})
		require.NoError(t, err)
		t.Cleanup(func() { _ = s.Close() })
		autoID := memory.NewID("autopro", "confirmed and hot")
		coldID := memory.NewID("autopro", "unreviewed and hot")
		pinID := memory.NewID("autopro", "pinned and unreviewed")
		for _, e := range []memory.Entry{
			{ID: autoID, Type: memory.TypeDecision, Thread: "autopro", Summary: "The confirmed hot entry earned its bytes", Body: "confirmed body", Status: memory.StatusActive, Trust: 0.8, Asserted: ptr(true), Scope: memory.ScopeGlobal},
			{ID: coldID, Type: memory.TypeDecision, Thread: "autopro", Summary: "The unreviewed hot entry stayed searchable ground", Body: "unreviewed body", Status: memory.StatusActive, Trust: 0.5, Asserted: ptr(true), Scope: memory.ScopeGlobal},
			{ID: pinID, Type: memory.TypeDecision, Thread: "autopro", Summary: "The pinned unreviewed entry kept its place", Body: "pinned body", Status: memory.StatusActive, Pinned: true, Trust: 0.5, Asserted: ptr(true), Scope: memory.ScopeGlobal},
		} {
			require.NoError(t, s.Put(ctx, e))
		}
		require.NoError(t, s.RefreshLifecycle(ctx))
		return autoID, coldID, pinID, s
	}

	t.Run("trust floor", func(t *testing.T) {
		autoID, coldID, pinID, s := build(t, nil)
		block := mustBlock(t, s, ctx)
		require.Contains(t, block, autoID, "confirmed content clears the floor and rides the hot path")
		require.Contains(t, block, pinID, "a pin is its own vouch and skips the floor")
		require.NotContains(t, block, coldID, "unreviewed content may never self-inject into every prompt")

		require.Contains(t, block, "trust 0.80", "the confirmed entry discloses its trust to the reader")
		require.Contains(t, block, "trust 0.50", "an unreviewed pin still shows the reader it is unreviewed")
		require.Contains(t, block, "updated just now", "age rides with the bytes so old cannot masquerade as fresh")

		hits, err := s.Search(ctx, memory.SearchQuery{Query: "unreviewed hot entry stayed searchable ground", Limit: 5})
		require.NoError(t, err)
		var found bool
		for _, h := range hits {
			found = found || h.ID == coldID
		}
		require.True(t, found, "the floor parks an entry from injection, it does not hide it from search")
	})

	t.Run("flag off", func(t *testing.T) {
		autoID, coldID, pinID, s := build(t, func(settings *memory.Settings) { settings.AutoPromote = ptr(false) })
		block := mustBlock(t, s, ctx)
		require.Contains(t, block, pinID, "the pins-only posture is the point of the flag")
		require.NotContains(t, block, autoID, "with the flag pulled even confirmed content needs a pin to ride along")
		require.NotContains(t, block, coldID)
	})
}

// TestEvalAutoPromoteShareCap proves a recall-flood cannot evict the curated
// window: the automatic path is capped at its share of the budget while the
// pins it would otherwise displace are never starved by it.
func TestEvalAutoPromoteShareCap(t *testing.T) {
	settings := memory.DefaultSettings()
	settings.MaxInjectBytes = 1400
	settings.AutoPromoteSharePct = 50
	settings.ThreadHintMaxLines = 0
	s, err := memory.Open(memory.OpenOptions{GlobalDir: t.TempDir(), Settings: settings, Shared: false})
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	ctx := context.Background()

	pinIDs := []string{memory.NewID("share", "pinned a"), memory.NewID("share", "pinned b")}
	for i, id := range pinIDs {
		require.NoError(t, s.Put(ctx, memory.Entry{
			ID: id, Type: memory.TypeFact, Thread: "share",
			Summary: fmt.Sprintf("Pinned sentinel %d holds the curated ground", i),
			Body:    "pinned body",
			Status:  memory.StatusActive, Pinned: true, Trust: 0.8, Asserted: ptr(true), Scope: memory.ScopeGlobal,
		}))
	}
	var autoIDs []string
	for i := range 9 {
		id := memory.NewID("share", fmt.Sprintf("flood %02d", i))
		require.NoError(t, s.Put(ctx, memory.Entry{
			ID: id, Type: memory.TypeFact, Thread: "share",
			Summary: fmt.Sprintf("Flood entry %02d recalled its way toward the window %s", i, strings.Repeat("look ", 10)),
			Body:    strings.Repeat("flood ", 20),
			Status:  memory.StatusActive, Trust: 0.9, Asserted: ptr(true), Scope: memory.ScopeGlobal,
		}))
		autoIDs = append(autoIDs, id)
	}
	require.NoError(t, s.RefreshLifecycle(ctx))

	block := mustBlock(t, s, ctx)
	require.LessOrEqual(t, len(block), settings.MaxInjectBytes, "the total ceiling still holds on top of the share ceiling")
	for _, id := range pinIDs {
		require.Contains(t, block, id, "the share cap bounds the automatic path, never the pins")
	}

	admitted, bytes := 0, 0
	for _, line := range strings.Split(block, "\n") {
		for _, id := range autoIDs {
			if strings.Contains(line, "("+id+")") {
				admitted++
				bytes += len(line)
			}
		}
	}
	shareCeiling := settings.MaxInjectBytes * settings.AutoPromoteSharePct / 100
	require.Greater(t, admitted, 0, "the automatic path still contributes; the cap only bounds it")
	require.Less(t, admitted, len(autoIDs), "the flood has to have been held at the share ceiling")
	require.LessOrEqual(t, bytes, shareCeiling, "non-pinned content may never claim more than its share of the budget")
	t.Logf("share cap held: %d of %d auto entries at %d bytes under a %d-byte share ceiling, both pins intact",
		admitted, len(autoIDs), bytes, shareCeiling)
}
