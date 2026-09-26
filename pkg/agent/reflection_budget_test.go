package agent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestReflectionTurnsCarryThroughRecursion pins the reason the count travels on
// the context: sessionAgent.Run re-runs a turn by recursing into itself, so the
// self-critique budget has to survive the nesting or the cap resets per level.
func TestReflectionTurnsCarryThroughRecursion(t *testing.T) {
	t.Parallel()

	base := context.Background()
	require.Equal(t, 0, reflectionTurnsFromContext(base), "a turn that never reflected has spent nothing")

	nested := base
	for range 4 {
		nested = withReflectionTurns(nested, reflectionTurnsFromContext(nested)+1)
	}
	require.Equal(t, 4, reflectionTurnsFromContext(nested), "each recursive level must see the running total")

	// A sibling branch off the same root must not inherit another branch's spend.
	sibling := withReflectionTurns(base, 9)
	require.Equal(t, 9, reflectionTurnsFromContext(sibling))
	require.Equal(t, 4, reflectionTurnsFromContext(nested))
}

func TestEffectiveMaxReflectionTurns(t *testing.T) {
	t.Parallel()

	require.Equal(t, defaultMaxReflectionTurns, effectiveMaxReflectionTurns(0), "an unset cap must not leave the recursion uncapped")
	require.Equal(t, defaultMaxReflectionTurns, effectiveMaxReflectionTurns(-1))
	require.Equal(t, 1, effectiveMaxReflectionTurns(1), "an explicit cap is honoured exactly")
	require.Equal(t, 7, effectiveMaxReflectionTurns(7))
}
