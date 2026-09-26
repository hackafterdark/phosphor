package ui

import (
	"strings"
	"testing"

	"github.com/hackafterdark/phosphor/internal/memory"
	"github.com/stretchr/testify/require"
)

func TestFromHitCarriesTheFieldsThatMakeACitation(t *testing.T) {
	t.Parallel()

	hit := memory.Hit{
		Entry: memory.Entry{
			ID:      "2026-09-24-decision-x",
			Type:    memory.NormalType("decision"),
			Status:  memory.StatusActive,
			Thread:  "memory-system",
			Tags:    []string{"plan", "gate"},
			Summary: "Pick option D.",
			Body:    "The long form of the claim.",
			Source:  "session#abc@3:9",
			Trust:   0.8,
			Pinned:  true,
			Updated: "2026-09-24T09:00:00Z",
			Scope:   memory.ScopeProject,
		},
		Snippet: "a snippet from the index",
		Why:     "score 12.5",
	}

	s := FromHit(hit, RoleRecalled)

	require.Equal(t, "2026-09-24-decision-x", s.ID)
	require.Equal(t, "decision", s.Type)
	require.Equal(t, "active", s.Status)
	require.Equal(t, "memory-system", s.Thread)
	require.Equal(t, []string{"plan", "gate"}, s.Tags)
	require.Equal(t, "Pick option D.", s.Summary)
	require.Equal(t, "a snippet from the index", s.Detail, "the snippet is what the reader saw, not the whole body")
	require.Equal(t, "session#abc@3:9", s.Origin)
	require.Equal(t, "score 12.5", s.Why)
	require.Equal(t, 0.8, s.Trust)
	require.True(t, s.Pinned)
	require.Equal(t, "2026-09-24T09:00:00Z", s.Updated)
	require.Equal(t, "project", s.Scope)
	require.Equal(t, RoleRecalled, s.Role)
}

func TestFromHitFallsBackToTheBodyAndTheCreatedStamp(t *testing.T) {
	t.Parallel()

	hit := memory.Hit{Entry: memory.Entry{
		ID:      "x",
		Body:    "Only a body exists.",
		Created: "2026-09-20T09:00:00Z",
	}}

	s := FromHit(hit, RoleInjected)

	require.Equal(t, "Only a body exists.", s.Detail, "with no snippet the body is the only thing to show")
	require.Equal(t, "2026-09-20T09:00:00Z", s.Updated, "an entry never written after creation still has an age")
}

func TestOneLineCollapsesRunsOfWhitespaceAndCapsTheLength(t *testing.T) {
	t.Parallel()

	require.Equal(t, "a b c", oneLine("  a\n\tb \r\n c  "))

	long := strings.Repeat("word ", 200)
	got := oneLine(long)
	require.LessOrEqual(t, len([]rune(got)), maxFieldRunes+1, "a handle is bounded; the body is not the handle")
	require.True(t, strings.HasSuffix(got, "…"), "a cut is disclosed rather than passed off as the whole claim")
}

func TestBadgeDisclosesWhatToDiscount(t *testing.T) {
	t.Parallel()

	badge := Badge(Source{Type: "decision", Status: "active", Trust: 0.5})
	require.True(t, strings.HasPrefix(badge, "["))
	require.True(t, strings.HasSuffix(badge, "]"))
	require.Contains(t, badge, "trust 0.50", "the unreviewed default has to be visible, not hidden behind the prose")

	pinned := Badge(Source{Type: "fact", Status: "active", Thread: "memory-system", Tags: []string{"a", "b"}, Trust: 0.8, Pinned: true, Role: RoleRecalled})
	require.Contains(t, pinned, "pinned")
	require.Contains(t, pinned, "#a #b")
	require.Contains(t, pinned, "memory-system")
	require.Contains(t, pinned, RoleRecalled, "the role is a column like any other")

	empty := Badge(Source{Trust: 1})
	require.Equal(t, "[trust 1.00]", empty, "fields nobody filled in are left out rather than rendered as blanks")
}

func TestCardRoundTripsThroughParse(t *testing.T) {
	t.Parallel()

	sources := []Source{
		{ID: "one", Type: "decision", Status: "active", Thread: "thread-one", Tags: []string{"x", "y"}, Summary: "First claim.", Detail: "Because of the evidence.", Origin: "session#a@1:2", Why: "score 9", Trust: 0.8, Pinned: true, Role: RoleRecalled},
		{ID: "two", Type: "constraint", Status: "active", Summary: "Second claim.", Trust: 0.5, Role: RoleRecalled},
	}

	got, count, ok := Parse(Card(0, sources))

	require.True(t, ok)
	require.Equal(t, 2, count)
	require.Equal(t, sources[0].ID, got[0].ID, "the card has to carry the id, because the card itself tells the reader to fetch by it")
	require.Equal(t, sources[0].Type, got[0].Type)
	require.Equal(t, sources[0].Status, got[0].Status)
	require.Equal(t, sources[0].Thread, got[0].Thread)
	require.Equal(t, sources[0].Tags, got[0].Tags)
	require.Equal(t, sources[0].Summary, got[0].Summary)
	require.Equal(t, sources[0].Detail, got[0].Detail)
	require.Equal(t, sources[0].Origin, got[0].Origin)
	require.Equal(t, sources[0].Why, got[0].Why)
	require.Equal(t, sources[0].Trust, got[0].Trust)
	require.Equal(t, sources[0].Pinned, got[0].Pinned)
	require.Equal(t, sources[1].ID, got[1].ID)
	require.Equal(t, sources[1].Summary, got[1].Summary)
	require.False(t, got[1].Pinned)
	require.Equal(t, 0.5, got[1].Trust)
}

func TestParseToleratesBytesItDoesNotRecognise(t *testing.T) {
	t.Parallel()

	// The bytes a pill re-reads were written by whatever build was current when the turn
	// happened, so a card from an older build or a truncated one still has to give what
	// it can rather than nothing at all.
	truncated := "Recalled 2 memories:\n\n1. [decision · active · t · trust 0.80] (one)\n   First claim.\n"
	got, count, ok := Parse(truncated)
	require.True(t, ok)
	require.Equal(t, 2, count, "the header outnumbers the surviving rows, and the header is the honest count")
	require.Len(t, got, 1)
	require.Equal(t, "one", got[0].ID)
	require.Equal(t, "First claim.", got[0].Summary)

	_, _, ok = Parse("Some other tool said:\n  nothing of the sort happened.\n")
	require.False(t, ok, "text that is not a card is not rendered as one")

	_, _, ok = Parse("")
	require.False(t, ok)
}

func TestParseReadsTheCardsBothVerbsUse(t *testing.T) {
	t.Parallel()

	out := Header("Touched ", 1) + "\n" + Rows([]Source{{ID: "gate-surfaced", Type: "plan", Status: "active", Summary: "Near duplicate.", Trust: 0.5, Role: RoleActed}})

	got, count, ok := Parse(out)
	require.True(t, ok)
	require.Equal(t, 1, count)
	require.Equal(t, "gate-surfaced", got[0].ID)
	require.Equal(t, "Near duplicate.", got[0].Summary)
}

func TestCountIsTheCollapsedPillLabel(t *testing.T) {
	t.Parallel()

	require.Equal(t, "", Count(nil), "no memories, no pill")
	require.Equal(t, "+1 memory", Count([]Source{{ID: "a"}}))
	require.Equal(t, "+3 memories", Count([]Source{{ID: "a"}, {ID: "b"}, {ID: "c"}}))
	require.Equal(t, "+2 memories", Count([]Source{{ID: "a"}, {ID: "b"}, {ID: "a"}}), "one entry recalled twice is one memory")
	require.Equal(t, "", Count([]Source{{}, {}}), "rows with no id are not a count of anything")
}

func TestExpandedDisclosesInOrderOfDecreasingConfidence(t *testing.T) {
	t.Parallel()

	text := Source{ID: "one", Summary: "A claim.", Thread: "t", Origin: "session#a@1:2", Trust: 0.8, Pinned: true, Why: "score 9"}.Expanded()
	lines := strings.Split(text, "\n")

	require.Equal(t, "one", lines[0], "it opens with what it is")
	require.Contains(t, lines[1], "A claim.")
	require.Contains(t, lines[2], "thread: t")
	require.Contains(t, lines[3], "source: session#a@1:2")
	require.Contains(t, lines[4], "trust 0.80")
	require.Contains(t, lines[4], "pinned")
	require.Contains(t, lines[5], "why: score 9")

	require.Equal(t, "one · t · session#a@1:2 · trust 0.80 · score 9", Source{ID: "one", Thread: "t", Origin: "session#a@1:2", Trust: 0.8, Why: "score 9"}.Citation())
}

func TestParseFootprintReadsTheWritersBlock(t *testing.T) {
	t.Parallel()

	out := "Committed decision \"x\".\nWhy: it is a constraint.\nInjected window: 512/2048 bytes. Corpus: 12 active, 2 pending, 3 retired."
	f, ok := ParseFootprint(out)

	require.True(t, ok)
	require.Equal(t, 512, f.Injected)
	require.Equal(t, 2048, f.InjectLimit)
	require.Equal(t, 12, f.Active)
	require.Equal(t, 2, f.Pending)
	require.Equal(t, 3, f.Retired)

	_, ok = ParseFootprint("no numbers in here")
	require.False(t, ok)
}
