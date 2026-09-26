package memory

import (
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
)

func TestSerializeParseRoundTrip(t *testing.T) {
	t.Parallel()

	yes := true
	in := Entry{
		ID:       "thread/decided-x",
		Type:     TypeDecision,
		Thread:   "release-flow",
		Summary:  "Ship behind a feature flag",
		Tags:     []string{"release", "flag"},
		Body:     "The rollout is gated behind a flag until the dashboard lands.",
		Notes:    "Human added: ping platform before enabling.",
		Status:   StatusActive,
		Trust:    0.7,
		Owner:    OwnerAgent,
		Asserted: &yes,
	}

	raw, err := Serialize(in)
	require.NoError(t, err)

	out, err := ParseEntryBytes("vault/x.md", raw)
	require.NoError(t, err)

	require.Equal(t, in.ID, out.ID)
	require.Equal(t, TypeDecision, out.Type)
	require.Equal(t, in.Thread, out.Thread)
	require.Equal(t, in.Summary, out.Summary)
	require.Equal(t, in.Tags, out.Tags)
	require.Equal(t, in.Body, out.Body)
	require.Equal(t, in.Notes, out.Notes)
	require.Equal(t, StatusActive, out.Status)
	require.InDelta(t, 0.7, out.Trust, 1e-9)
	require.NotNil(t, out.Asserted)
	require.True(t, *out.Asserted)
	require.False(t, out.Inferred())
}

func TestParseEntryRejectsBadFrontmatter(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		raw  string
	}{
		{"missing fence", "id: x\ntype: fact\n\nbody only, no fence"},
		{"unterminated block", "---\nid: x\ntype: fact\n"},
		{"no id", "---\nid:\ntype: fact\n---\nbody"},
		{
			"merge conflict markers",
			"---\nid: x\ntype: fact\n<<<<<<< HEAD\nphosphor.trust: 0.9\n=======\nphosphor.trust: 0.2\n>>>>>>> other\n---\nbody",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseEntryBytes("vault/x.md", []byte(tc.raw))
			require.Error(t, err)
			var perr *ParseError
			require.ErrorAs(t, err, &perr, "must surface a *ParseError so the caller can quarantine")
		})
	}
}

func TestSplitNotesKeepsBothVoices(t *testing.T) {
	t.Parallel()

	body, notes := splitNotes("The agent body.\n\n## Notes\nHuman note line.")
	require.Equal(t, "The agent body.", body)
	require.Equal(t, "Human note line.", notes)

	// A body with no Notes region keeps everything as the body.
	b2, n2 := splitNotes("Just the body.")
	require.Equal(t, "Just the body.", b2)
	require.Equal(t, "", n2)
}

func TestNewIDAndSlugAreStable(t *testing.T) {
	t.Parallel()

	a := NewID("release-flow", "Ship behind a feature flag")
	b := NewID("release-flow", "Ship behind a feature flag")
	require.Equal(t, a, b, "identity must be deterministic so re-writes are idempotent")

	slug := Slug(a)
	require.NotEmpty(t, slug)
	for _, bad := range []string{" ", "/", "\\", ":", "*", "?", "\"", "<", ">", "|"} {
		require.NotContains(t, slug, bad, "slug must be filesystem-safe")
	}
}

func TestSlugAndEntryPathsBoundLongValues(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("x", 300)
	slug := Slug(long)
	require.Equal(t, slug, Slug(long), "truncation must be deterministic")
	require.LessOrEqual(t, len(slug), maxFileNameLength, "slug must stay inside the file-name budget")
	require.NotEqual(t, slug, Slug(long[:299]+"y"), "long values sharing a prefix must not fuse")

	id := NewID(long, long+" tail")
	require.LessOrEqual(t, len(id), maxFileNameLength, "generated ids must stay inside the file-name budget")
	path := EntryPath("vault", id)
	name := filepath.Base(path)
	require.LessOrEqual(t, len(strings.TrimSuffix(name, ".md")), maxFileNameLength)

	other := NewID(long, long+" other tail")
	require.NotEqual(t, path, EntryPath("vault", other))
}

func TestTokenOverlapAndNormalize(t *testing.T) {
	t.Parallel()

	require.InDelta(t, 1.0, TokenOverlap("alpha beta gamma", "alpha beta gamma"), 1e-9)
	require.Equal(t, 0.0, TokenOverlap("alpha beta", "completely different"))

	require.True(t, ValidType("decision"))
	require.False(t, ValidType("nonsense"))
	require.Equal(t, TypeFact, NormalType("nonsense"), "an unknown type degrades to the least consequential kind")
	require.Equal(t, TypeDecision, NormalType("  Decision "))
}

func TestSerializeCarriesTitleVerbatim(t *testing.T) {
	t.Parallel()

	in := Entry{
		ID:      "release-flow/titled-entry",
		Type:    TypeDecision,
		Thread:  "release-flow",
		Summary: "Ship behind a feature flag",
		Title:   "Ship behind a flag",
		Body:    "The rollout is gated behind a flag.",
		Status:  StatusActive,
	}

	raw, err := Serialize(in)
	require.NoError(t, err)
	require.Contains(t, string(raw), "title: Ship behind a flag\n")

	out, err := ParseEntryBytes("vault/titled.md", raw)
	require.NoError(t, err)
	require.Equal(t, in.Title, out.Title)

	// An absent title serializes to nothing at all, so files a human curates
	// stay free of a generated label they never asked for.
	untitled := in
	untitled.Title = ""
	bare, err := Serialize(untitled)
	require.NoError(t, err)
	require.NotContains(t, string(bare), "title:")
}

func TestDeriveTitleCutsAtWordBoundaries(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		summary string
		want    string
	}{
		{"short summary passes whole", "Ship behind a feature flag", "Ship behind a feature flag"},
		{"trailing punctuation trimmed", "Ship behind a feature flag.", "Ship behind a feature flag"},
		{
			"word cap keeps the leading clause",
			"one two three four five six seven eight nine ten eleven twelve",
			"one two three four five six seven eight",
		},
		{
			"byte cap never cuts mid word",
			"Prose backends supply deterministic keyword extraction for multilingual memory corpora",
			"Prose backends supply deterministic keyword",
		},
		{"blank summary yields no title", "   ", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, deriveTitle(tc.summary))
		})
	}

	long := deriveTitle("ExtraordinarilyUnlikelySupercalifragilisticistically expounds everything else here now")
	require.NotEmpty(t, long)
	require.LessOrEqual(t, len(long), 48, "the label stays short enough for a graph node")
	require.True(t, utf8.Valid([]byte(long)), "a rune is never split by the byte cap")
}
