package model

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestCompactLayoutToggleAppliesLive asserts the compact layout takes effect
// the moment the compact-mode flag flips, with no restart or resize needed.
func TestCompactLayoutToggleAppliesLive(t *testing.T) {
	t.Parallel()

	u := newTestUI()
	u.updateLayoutAndSize()
	require.False(t, u.isCompact, "large terminal starts in full layout")

	// Simulate the Display Preferences toggle: SetCompactMode flips the
	// flag on the live config, the handler flips forceCompactMode, and
	// updateLayoutAndSize re-derives the layout.
	u.forceCompactMode = true
	u.updateLayoutAndSize()
	require.True(t, u.isCompact, "toggling compact mode on must apply immediately")

	u.forceCompactMode = false
	u.updateLayoutAndSize()
	require.False(t, u.isCompact, "toggling compact mode off must apply immediately")
}
