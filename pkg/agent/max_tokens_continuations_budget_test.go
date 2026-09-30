package agent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestMaxTokensContinuationsCarryThroughRecursion pins the reason the count
// travels on the context: sessionAgent.Run recovers from a step that ran out
// of its token/thinking budget by recursing into itself, so the continuation
// budget has to survive the nesting or the cap resets per level.
func TestMaxTokensContinuationsCarryThroughRecursion(t *testing.T) {
	t.Parallel()

	base := context.Background()
	require.Equal(t, 0, maxTokensContinuationsFromContext(base), "a turn that never recovered has spent nothing")

	nested := base
	for range 4 {
		nested = withMaxTokensContinuations(nested, maxTokensContinuationsFromContext(nested)+1)
	}
	require.Equal(t, 4, maxTokensContinuationsFromContext(nested), "each recursive level must see the running total")

	// A sibling branch off the same root must not inherit another branch's spend.
	sibling := withMaxTokensContinuations(base, 9)
	require.Equal(t, 9, maxTokensContinuationsFromContext(sibling))
	require.Equal(t, 4, maxTokensContinuationsFromContext(nested))
}

func TestEffectiveMaxTokensContinuations(t *testing.T) {
	t.Parallel()

	require.Equal(t, defaultMaxTokensContinuations, effectiveMaxTokensContinuations(0), "an unset cap must fall back to the default")
	require.Equal(t, 1, effectiveMaxTokensContinuations(1), "an explicit positive cap is honoured exactly")
	require.Equal(t, 7, effectiveMaxTokensContinuations(7))
	require.Equal(t, -1, effectiveMaxTokensContinuations(-1), "a negative value passes through for the caller to treat as unlimited")
}
