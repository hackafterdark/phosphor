package eval

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/hackafterdark/phosphor/internal/memory"
	"github.com/stretchr/testify/require"
)

// TestEvalRateLimitFloorDefersWrites is section 11's "floor defers writes" metric and
// section 8's promise that memory is never the thing that trips a rate limit. The
// floor has to be read by the write path, not merely declared, so the case is driven
// against the real gate over the real store: an entry that commits freely at a
// healthy budget is parked as a proposal once the same budget sits at the floor, and
// a search over the very same store still returns its hits — deferral guards the
// token-spending write, never the cheap read.
func TestEvalRateLimitFloorDefersWrites(t *testing.T) {
	ctx := context.Background()

	newGate := func() (*memory.Store, *memory.Gate) {
		s, _ := newCorpusStore(t, corpusSettings())
		policy := memory.DefaultPolicy()
		policy.AskMode = memory.AskModeAuto
		policy.RateLimitFloorPct = 25
		return s, memory.NewGate(s, nil, policy)
	}
	reference := func(low bool) memory.WriteRequest {
		return memory.WriteRequest{
			SessionID: "eval-floor",
			Primary:   true,
			LowBudget: low,
			Entry: memory.Entry{
				Type:     memory.TypeReference,
				Thread:   "eval-floor",
				Summary:  "The workspace search index is backed by SQLite FTS5 with BM25 ranking",
				Body:     "A reference note that commits on its own at a healthy budget.",
				Asserted: ptr(true),
				Scope:    memory.ScopeGlobal,
			},
		}
	}

	// At a healthy budget the same auto-commit-able reference is committed outright.
	_, g := newGate()
	healthy, err := g.Add(ctx, reference(false))
	require.NoError(t, err)
	require.Equal(t, memory.StatusCommitted, healthy.Status,
		"a reference note commits on its own at a healthy budget under the auto dial")

	// At the floor it is parked, not committed: the write is the token-spending act
	// the floor exists to protect.
	_, g = newGate()
	defered, err := g.Add(ctx, reference(true))
	require.NoError(t, err)
	require.Equal(t, memory.StatusPendingWrite, defered.Status,
		"the rate-limit floor defers the write rather than spending the budget to commit it")
	require.NotZero(t, defered.Proposal, "a deferred write has to be parked as a proposal, not dropped")

	// And recall still works over the same contended store: the floor is about writes.
	s, g := newGate()
	_, err = g.Add(ctx, reference(true))
	require.NoError(t, err)
	hits, err := s.Search(ctx, memory.SearchQuery{Query: "memory search index FTS5 BM25 ranking", Limit: 5})
	require.NoError(t, err)
	require.NotEmpty(t, hits, "a search is cheap and stays allowed even at the rate-limit floor")
}

// TestEvalDistillationIsGatedAndPendingOnly enforces section 12 Phase 6's Definition
// of Done for distillation: gated off it ships nothing, on it proposes pending drafts
// only and never auto-injects, and it stands down below the floor. The tri-state and
// the parse/strip are pure and checked directly; the propose path drives the real
// gate the way ProposeDistillates drives it, so the guarantee is measured on the
// production write path rather than asserted about it.
func TestEvalDistillationIsGatedAndPendingOnly(t *testing.T) {
	ctx := context.Background()

	fresh := memory.Budget{Used: 0, Window: 1000, FloorPct: 10}
	broke := memory.Budget{Used: 970, Window: 1000, FloorPct: 10}

	// Off by default: no amount of remaining budget turns an unasked-for path on.
	require.False(t, memory.ShouldDistill(false, fresh), "distillation is off unless explicitly turned on")
	// On but below the floor: even an opted-in distillation stands down rather than risk the limit.
	require.False(t, memory.ShouldDistill(true, broke), "distillation is skipped below the rate-limit floor")
	// On and clear of the floor: the one configuration where it is permitted.
	require.True(t, memory.ShouldDistill(true, fresh), "an opted-in distillation runs when the budget clears the floor")

	// The side-channel block the summarizer is asked to append parses into candidates
	// and is removable from the summary so it is never replayed as prose.
	raw := "Here is the conversation summary.\n\n" +
		"<proposed_memories>\ndecision :: We will use SQLite FTS5 for the memory index\n" +
		"constraint :: The memory path must never spend a model token on the hot path\n" +
		"</proposed_memories>"
	cands := memory.ParseDistillates(raw, 8)
	require.Len(t, cands, 2, "both proposed candidates parse back out of the summary")
	require.Equal(t, memory.TypeDecision, cands[0].Type)
	require.Equal(t, memory.TypeConstraint, cands[1].Type)
	stripped := memory.StripDistillates(raw)
	require.NotContains(t, stripped, "proposed_memories",
		"the candidate block is stripped from the stored summary so it is not replayed forever")
	require.Contains(t, stripped, "conversation summary", "the summary itself survives the strip")

	// A distilled draft is inferred, and an inferred write may only ever land pending —
	// the invariant that makes "propose, never auto-commit" true regardless of the dial.
	s, _ := newCorpusStore(t, corpusSettings())
	policy := memory.DefaultPolicy()
	policy.AskMode = memory.AskModeAuto
	g := memory.NewGate(s, nil, policy)
	for _, c := range cands {
		out, err := g.Add(ctx, memory.WriteRequest{
			SessionID:       "eval-distill",
			Primary:         true,
			SkipBudgetCheck: true,
			Entry: memory.Entry{
				Type:    c.Type,
				Summary: c.Summary,
				Body:    c.Body,
				Owner:   memory.OwnerAgent,
				Scope:   memory.ScopeGlobal,
			},
		})
		require.NoError(t, err)
		require.Equal(t, memory.StatusPendingWrite, out.Status,
			"a distilled draft is proposed as pending, never committed, even under the auto dial")
	}
	block, err := s.TierABlock(ctx)
	require.NoError(t, err)
	require.NotContains(t, block, "SQLite FTS5",
		"a pending distillate never reaches the always-injected window on its own")
}

// TestEvalCompactionSurvivalCapsBlock is section 12 Phase 6's survival clause: the
// durable window is re-stamped into a compaction summary so decisions survive it,
// with the hard requirement that the spliced block stays under max_inject_bytes after
// compaction so survival cannot re-inflate the very window the compaction shrank.
func TestEvalCompactionSurvivalCapsBlock(t *testing.T) {
	ctx := context.Background()

	// A durable window far larger than the budget is capped to it by the splice, and the
	// capped block is exactly what the summary ends up carrying.
	big := strings.Repeat("durable decision that must survive compaction\n", 200)
	require.Greater(t, len(big), 400, "the test needs an over-budget block to prove the cap bites")
	capped := memory.CapBlock(big, 400)
	require.LessOrEqual(t, len(capped), 400, "a capped block never exceeds the inject budget")
	require.True(t, utf8.Valid([]byte(capped)), "a capped block is always valid UTF-8")

	spliced := memory.SpliceIntoSummary("SUMMARY OF THE CONVERSATION", big, 400)
	require.Equal(t, "SUMMARY OF THE CONVERSATION\n\n"+capped, spliced,
		"the summary carries the durable window verbatim-capped so it survives compaction")
	require.True(t, strings.HasSuffix(spliced, capped), "the durable window is present in the spliced summary")

	// A block already inside the budget is passed through untouched, and one built over
	// the corpus respects the ceiling the store was opened with.
	settings := memory.DefaultSettings()
	settings.MaxInjectBytes = 1600
	settings.ThreadHintMaxLines = 0
	s, _ := newCorpusStore(t, settings)
	block, err := s.TierABlock(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, strings.TrimSpace(block))
	require.LessOrEqual(t, len(block), settings.MaxInjectBytes,
		"the durable window is byte-capped to the inject budget")
	require.Equal(t, block, memory.CapBlock(block, settings.MaxInjectBytes),
		"an in-budget window is not trimmed further")

	// CapBlock trims to the byte budget without ever splitting a multibyte rune, which
	// would otherwise hand the next session corrupted text as "memory."
	require.Equal(t, "aaaa", memory.CapBlock("aaaaé", 5), "trimming stops before splitting a multibyte rune")
	require.True(t, utf8.Valid([]byte(memory.CapBlock("aébçdééf", 5))), "a trimmed block is always valid UTF-8")
}
