package memory

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestShouldNudge(t *testing.T) {
	t.Run("decision-shaped primary turn with no write nudges", func(t *testing.T) {
		note, ok := ShouldNudge(TurnInfo{
			SessionID: "s",
			Text:      "We decided to store entries as markdown files.",
			Primary:   true,
		})
		require.True(t, ok)
		require.Equal(t, NudgeText, note)
	})

	t.Run("subagent stays silent", func(t *testing.T) {
		_, ok := ShouldNudge(TurnInfo{
			SessionID: "s",
			Text:      "We decided to store entries as markdown files.",
			Primary:   false,
		})
		require.False(t, ok)
	})

	t.Run("already wrote memory stays silent", func(t *testing.T) {
		_, ok := ShouldNudge(TurnInfo{
			SessionID:   "s",
			Text:        "We decided to store entries as markdown files.",
			Primary:     true,
			WroteMemory: true,
		})
		require.False(t, ok)
	})

	t.Run("suppressed turn stays silent", func(t *testing.T) {
		_, ok := ShouldNudge(TurnInfo{
			SessionID:  "s",
			Text:       "We decided to store entries as markdown files.",
			Primary:    true,
			Suppressed: true,
		})
		require.False(t, ok)
	})

	t.Run("non-decision text stays silent", func(t *testing.T) {
		_, ok := ShouldNudge(TurnInfo{
			SessionID: "s",
			Text:      "Just listing the files in the repo.",
			Primary:   true,
		})
		require.False(t, ok)
	})
}

func TestWroteMemory(t *testing.T) {
	t.Parallel()
	require.True(t, WroteMemory([]string{"edit", "memory"}))
	require.True(t, WroteMemory([]string{"Memory"}))
	require.False(t, WroteMemory([]string{"edit", "bash"}))
	require.False(t, WroteMemory(nil))
}

func TestSuppressed(t *testing.T) {
	t.Parallel()
	require.True(t, Suppressed("Done. //ignore-memory-this-turn"))
	require.False(t, Suppressed("A plain answer with no opt-out."))
}

func TestLooksDecisionShaped(t *testing.T) {
	t.Parallel()
	require.True(t, LooksDecisionShaped("The plan is to use SQLite for the index."))
	require.False(t, LooksDecisionShaped("   "))
	require.False(t, LooksDecisionShaped("Renamed a variable in the helper."))
}
