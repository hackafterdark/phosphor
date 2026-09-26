//go:build !phosphor_prose

package memory

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// Without the build gate the keyword path is the deterministic stand-in:
// no model, no dependency, byte-identical to what the tree did before the
// prose seam existed. This is the §13 prune, enforced by a test.
func TestKeywordsAreTheStandInWithoutTheBuildGate(t *testing.T) {
	t.Parallel()
	require.False(t, ProseLinked())
	text := "we decided to ship the render cache because the window shrinks"
	require.Equal(t, Tokenize(text, 5), Keywords(text, 5))
}

// A config request cannot conjure a runtime the binary never linked: asking
// for enrichment in a default build must be a clean no-op, not a surprise
// behaviour change for whoever flips the setting on.
func TestEnableTrueCannotForceAnUnlinkedBuild(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	on, yes := true, true
	settings := DefaultSettings()
	settings.EnableProse = &on
	s := openTestStore(t, settings)
	g := NewGate(s, nil, Policy{AskMode: AskModeAuto, Adaptive: false})

	e := Entry{
		Type: TypeFact, Thread: "kw", ID: NewID("kw", "unlinked"),
		Summary: "the render cache busts when the window shrinks mid frame",
		Body:    "the render cache busts when the window shrinks mid frame",
		Status:  StatusActive, Asserted: &yes,
	}
	out, err := g.Add(ctx, WriteRequest{SessionID: "kw", Primary: true, Entry: e})
	require.NoError(t, err)
	require.Equal(t, StatusCommitted, out.Status)

	got, err := s.Get(ctx, e.ID)
	require.NoError(t, err)
	require.Len(t, got, 1)

	tags, err := s.TagsOf(ctx, e.ID)
	require.NoError(t, err)
	require.Empty(t, tags)
}
