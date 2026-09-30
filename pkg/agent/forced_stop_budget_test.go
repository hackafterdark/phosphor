package agent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestForcedStopContinuationsCarryThroughRecursion pins the reason the count
// travels on the context: sessionAgent.Run recovers from a forced mid-generation
// stop by recursing into itself, so the continuation budget has to survive the
// nesting or the cap resets per level.
func TestForcedStopContinuationsCarryThroughRecursion(t *testing.T) {
	t.Parallel()

	base := context.Background()
	require.Equal(t, 0, forcedStopContinuationsFromContext(base), "a turn that never recovered has spent nothing")

	nested := base
	for range 4 {
		nested = withForcedStopContinuations(nested, forcedStopContinuationsFromContext(nested)+1)
	}
	require.Equal(t, 4, forcedStopContinuationsFromContext(nested), "each recursive level must see the running total")

	// A sibling branch off the same root must not inherit another branch's spend.
	sibling := withForcedStopContinuations(base, 9)
	require.Equal(t, 9, forcedStopContinuationsFromContext(sibling))
	require.Equal(t, 4, forcedStopContinuationsFromContext(nested))
}
