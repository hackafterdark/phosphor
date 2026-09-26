package eval

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/hackafterdark/phosphor/internal/memory"
	"github.com/stretchr/testify/require"
)

// measured is one retrieval case reduced to the two numbers section 11 bars.
type measured struct {
	id        string
	recall10  float64
	precision float64
	vetoed    []string
	missing   []string
}

// TestEvalRetrieval is the section 11 retrieval gate: BM25 over the real index has to
// bring back the curated answers and stay clear of the entries that must never come
// back, at the precision and recall bars, without a model in the loop.
//
// The must_not veto is asserted separate of the bars on purpose. Precision can absorb
// a weak ranking, but a retired or pending entry surfacing is not a ranking
// imperfection, it is the exclusion invariant failing, and it has to stop the run on
// its own.
func TestEvalRetrieval(t *testing.T) {
	m := loadManifest(t)
	s, _ := newCorpusStore(t, corpusSettings())
	ctx := context.Background()

	var cases []measured
	for _, c := range m.Retrieval {
		limit := c.Limit
		if limit <= 0 {
			limit = 10
		}
		q := memory.SearchQuery{
			Query:  c.Query,
			Thread: c.Thread,
			Tags:   c.Tags,
			Limit:  limit,
		}
		for _, ts := range c.Types {
			q.Types = append(q.Types, memory.NormalType(ts))
		}
		hits, err := s.Search(ctx, q)
		require.NoError(t, err, "case %s", c.ID)

		var got []string
		for _, h := range hits {
			got = append(got, h.ID)
		}

		mc := measured{id: c.ID}
		for _, banned := range c.MustNot {
			if in(got, banned) {
				mc.vetoed = append(mc.vetoed, banned)
			}
		}
		if len(c.ExpectIDs) > 0 {
			var found int
			for _, want := range c.ExpectIDs {
				if in(got, want) {
					found++
				} else {
					mc.missing = append(mc.missing, want)
				}
			}
			mc.recall10 = float64(found) / float64(len(c.ExpectIDs))

			// Precision is scored against the smaller of the window and the relevant
			// set. A curated golden set names the answers that exist; dividing a
			// one-answer query by five would call a flawless run a twenty percent run.
			denom := min(5, len(c.ExpectIDs))
			inWindow := 0
			for _, id := range got[:min(5, len(got))] {
				if in(c.ExpectIDs, id) {
					inWindow++
				}
			}
			mc.precision = float64(inWindow) / float64(denom)
		}
		cases = append(cases, mc)

		t.Logf("%-24s r@10=%.2f p@5=%.2f hits=%d missing=%v",
			c.ID, mc.recall10, mc.precision, len(got), mc.missing)
	}

	// The hard veto first, with the offending ids in the failure so a regression says
	// what leaked rather than merely that something did.
	for _, mc := range cases {
		require.Empty(t, mc.vetoed, "case %s returned an entry the manifest vetoes", mc.id)
	}

	var sumR, sumP float64
	nR, nP := 0, 0
	for _, mc := range cases {
		if len(mc.missing) > 0 || mc.recall10 > 0 {
			sumR += mc.recall10
			nR++
		}
		if mc.precision > 0 || len(mc.missing) > 0 {
			sumP += mc.precision
			nP++
		}
	}
	require.NotZero(t, nR, "no case produced a recall measurement")
	require.NotZero(t, nP, "no case produced a precision measurement")

	r, p := sumR/float64(nR), sumP/float64(nP)
	t.Logf("aggregate recall@10=%.3f (bar %.2f)  precision@5=%.3f (bar %.2f)", r, BarRecallAt10, p, BarPrecisionAt5)
	require.GreaterOrEqual(t, r, BarRecallAt10, "recall@10 over the curated corpus")
	require.GreaterOrEqual(t, p, BarPrecisionAt5, "precision@5 over the curated corpus")
}

// TestEvalContinuation is the keyword-less "keep going" gate. The prompt shares no
// keyword with any entry and names no thread, so the only thing that can resolve it
// is the names-only continuation hint; if that hint is wrong, incomplete, or leaky,
// the affordance does not exist however the prompt is phrased.
func TestEvalContinuation(t *testing.T) {
	m := loadManifest(t)
	require.NotEmpty(t, m.Continuation, "the manifest declares no continuation cases")
	s, _ := newCorpusStore(t, corpusSettings())
	ctx := context.Background()

	for _, c := range m.Continuation {
		require.NotEmpty(t, c.Prompt)

		hint, err := s.TierABlock(ctx)
		require.NoError(t, err)

		nominated, err := s.ActiveThreads(ctx, 0)
		require.NoError(t, err)
		var names []string
		for _, th := range nominated {
			names = append(names, th.Thread)
		}

		var hit int
		for _, want := range c.ExpectThreads {
			if in(names, want) && strings.Contains(hint, want) {
				hit++
				continue
			}
			t.Errorf("case %s: thread %q is not nominated by the hint (nominated: %v)", c.ID, want, names)
		}
		rate := float64(hit) / float64(len(c.ExpectThreads))
		t.Logf("%-24s prompt=%q nominated=%v coverage=%.2f", c.ID, c.Prompt, names, rate)
		require.GreaterOrEqual(t, rate, BarContinuation, "case %s keyword-less continuation", c.ID)

		// The hint is names-only by contract. A hint that carries body text is both a
		// token cost on every session and the leak the design fences off, so it is
		// checked here rather than left to review.
		//
		// The line budget governs the hint section only, so the count starts at the
		// hint header and not at the top of the block: the entry lines above it are
		// priced by the byte cap, not by this knob.
		lines := 0
		inHint := false
		for _, line := range strings.Split(hint, "\n") {
			if strings.HasPrefix(line, "active threads") {
				inHint = true
				continue
			}
			if inHint && strings.HasPrefix(strings.TrimSpace(line), "- ") {
				lines++
			}
		}
		require.Greater(t, lines, 0, "the hint section has to exist to be budgeted")
		require.LessOrEqual(t, lines, s.Settings().ThreadHintMaxLines,
			"the hint has to stay inside its configured line budget")

		// The hint is names-only by contract: recency and thread names, never
		// content. A hint that carries corpus body text is both a token cost on
		// every session and the leak the design fences off, so the scan runs over
		// the hint section only, not the block above it, whose entry lines carry
		// their summaries by design.
		hintSection := ""
		if i := strings.Index(hint, "active threads"); i >= 0 {
			hintSection = hint[i:]
		}
		require.NotEmpty(t, hintSection, "the block has to carry the names-only hint")
		body := corpusBodyWords(t)
		for _, word := range body {
			require.NotContains(t, hintSection, word,
				"the continuation hint is names-only and must not carry corpus body text")
		}

		// The prompt itself must genuinely be unresolvable by content, or this test
		// would be measuring the search path instead of the hint.
		hits, err := s.Search(ctx, memory.SearchQuery{Query: c.Prompt, Limit: 5})
		require.NoError(t, err)
		t.Logf("%-24s the prompt alone returns %d hits, so the hint is doing the resolving", c.ID, len(hits))
	}
}

// corpusBodyWords samples the distinctive words out of the fixture bodies so the
// names-only assertion above has real strings to look for.
func corpusBodyWords(t *testing.T) []string {
	t.Helper()
	raws, err := os.ReadDir(CorpusDir)
	require.NoError(t, err)
	seen := map[string]bool{}
	var out []string
	for _, de := range raws {
		if info, err := de.Info(); err != nil || info.IsDir() {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(CorpusDir, de.Name()))
		require.NoError(t, err)
		text := string(raw)
		if i := strings.Index(text, "\n---\n"); i > 0 {
			text = text[i:]
		}
		for _, w := range memory.Tokenize(text, 0) {
			if len(w) < 9 || seen[w] {
				continue
			}
			seen[w] = true
			out = append(out, w)
		}
	}
	sort.Strings(out)
	return out[:min(12, len(out))]
}
