package chat

import (
	"testing"

	"github.com/hackafterdark/phosphor/pkg/config"
	"github.com/stretchr/testify/require"
)

// A collapsed body is suppressed only while unexpanded; expansion (click or
// space) always wins over the display mode, so users can still inspect a
// hidden result.
func TestToolOutputMaxLines_ExpandedBeatsCollapsed(t *testing.T) {
	t.Parallel()

	require.Equal(t, -1, toolOutputMaxLines(config.ToolOutputCollapsed, false, 42, 10))
	require.Equal(t, 42, toolOutputMaxLines(config.ToolOutputCollapsed, true, 42, 10))
}
