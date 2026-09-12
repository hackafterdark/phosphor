package model

import (
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/hackafterdark/phosphor/internal/workspaceindex"
	"github.com/stretchr/testify/require"
)

// The hint is drawn with the Reasoning style, which left-pads two columns. We
// reproduce that here so the test exercises the same measurement the widget
// uses; the counts use a plain style like Provider (no padding).
var (
	testCountsStyle = lipgloss.NewStyle()
	testHintStyle   = lipgloss.NewStyle().PaddingLeft(2)
)

// TestIndexStatusLinesResponsive guards the responsive layout of the sidebar
// Workspace Search status: a wide sidebar collapses the counts onto one line and
// the freshness hint onto another, while a narrow one splits each onto its own
// short line so trailing values are never truncated.
func TestIndexStatusLinesResponsive(t *testing.T) {
	t.Parallel()

	fresh := &workspaceindex.IndexProgress{
		FilesIndexed:   1,
		SymbolsIndexed: 2,
		DocsIndexed:    3,
		LastBuilt:      time.Now(),
	}

	t.Run("wide sidebar collapses counts onto one line", func(t *testing.T) {
		t.Parallel()

		counts, hint := indexStatusLines(fresh, true, 60, testCountsStyle, testHintStyle)
		require.Len(t, counts, 1, "expected counts collapsed onto a single line")
		require.Contains(t, counts[0], "Files")
		require.Contains(t, counts[0], "Symbols")
		require.Contains(t, counts[0], "Docs")
		require.Len(t, hint, 1, "expected a single hint line when wide")
	})

	t.Run("narrow sidebar splits counts and never truncates", func(t *testing.T) {
		t.Parallel()

		const width = 12
		counts, _ := indexStatusLines(fresh, true, width, testCountsStyle, testHintStyle)
		require.Len(t, counts, 3, "expected counts to split one per line when narrow")
		for _, l := range counts {
			require.LessOrEqual(t, lipgloss.Width(testCountsStyle.Render(l)), width, "count line exceeds width: %q", l)
		}
		require.Contains(t, strings.Join(counts, "\n"), "Docs 3")
	})

	// Regression: "  auto-update on · built just now" fit unstyled but overflowed
	// because the Reasoning style pads two columns. The hint must split so the
	// rendered (padded) lines always fit.
	t.Run("hint splits rather than clip from style padding", func(t *testing.T) {
		t.Parallel()

		const width = 24
		_, hint := indexStatusLines(fresh, true, width, testCountsStyle, testHintStyle)
		require.Len(t, hint, 2, "hint should split rather than overflow when padding is counted")
		for _, l := range hint {
			require.LessOrEqual(t, lipgloss.Width(testHintStyle.Render(l)), width, "hint line exceeds width: %q", l)
			require.NotContains(t, l, "  ", "hint must not carry a manual indent (style pads it)")
		}
		require.Contains(t, strings.Join(hint, "\n"), "auto-update on")
	})
}
