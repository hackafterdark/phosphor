package memory

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// The phosphor.json kill switch must leave the entry exactly as written: no
// tags invented, no error, nothing an observer can tell apart from a build
// that never had the enrichment. Valid under both build modes, which is what
// makes it ungated.
func TestTagAutoFillHonoursTheKillSwitch(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	off, yes := false, true
	settings := DefaultSettings()
	settings.EnableProse = &off
	s := openTestStore(t, settings)
	g := NewGate(s, nil, Policy{AskMode: AskModeAuto, Adaptive: false})

	e := Entry{
		Type: TypeFact, Thread: "kw", ID: NewID("kw", "kept-clean"),
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

	// The join table is where tags would live, so emptiness must be asserted there:
	// the row struct carries no tag column and would pass vacuously.
	tags, err := s.TagsOf(ctx, e.ID)
	require.NoError(t, err)
	require.Empty(t, tags)
}
