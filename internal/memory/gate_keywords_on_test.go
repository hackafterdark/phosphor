//go:build phosphor_prose

package memory

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Phase 5 DoD, gated build: keywords auto-fill untagged entries at write time,
// and the fill comes from the linked multilingual runtime rather than the
// deterministic stand-in.
func TestGateAutoFillsTagsFromKeywords(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	require.True(t, ProseLinked())
	s := openTestStore(t, DefaultSettings())
	yes := true
	g := NewGate(s, nil, Policy{AskMode: AskModeAuto, Adaptive: false})

	e := Entry{
		Type: TypeDecision, Thread: "kw", ID: NewID("kw", "tagless"),
		Summary: "the render cache busts when the window shrinks mid frame",
		Body:    "the render cache busts when the window shrinks mid frame; invalidate it on resize",
		Status:  StatusActive, Asserted: &yes,
	}
	out, err := g.Add(ctx, WriteRequest{SessionID: "kw", Primary: true, Entry: e})
	require.NoError(t, err)
	require.Equal(t, StatusCommitted, out.Status)

	got, err := s.Get(ctx, e.ID)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, StatusActive, got[0].Status)

	// Tags live in the join table, so the assertion must go through the tag view:
	// asserting on the row struct would pass vacuously and prove nothing.
	tags, err := s.TagsOf(ctx, e.ID)
	require.NoError(t, err)
	require.Contains(t, tags, "render")
}

func TestKeywordsUseTheLinkedProseRuntime(t *testing.T) {
	t.Parallel()
	kw := Keywords("Go is an open-source programming language made at Google.", 6)
	require.NotEmpty(t, kw)
	for _, w := range kw {
		require.Equal(t, strings.ToLower(w), w)
		require.NotEqual(t, "is", w)
	}
}

// The whole reason this fork was chosen over the archived original: the
// keyword path must work without being told the language.
func TestKeywordsAreMultilingual(t *testing.T) {
	t.Parallel()
	kw := Keywords("El sistema almacena datos estructurados de muchas aplicaciones.", 6)
	require.NotEmpty(t, kw)
	require.Contains(t, kw, "almacena")
}
