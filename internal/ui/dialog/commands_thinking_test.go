package dialog

import (
	"testing"

	"github.com/hackafterdark/phosphor/internal/ui/common"
	"github.com/hackafterdark/phosphor/internal/ui/styles"
	"github.com/hackafterdark/phosphor/pkg/goal"
	"github.com/stretchr/testify/require"
)

func TestCommands_ThinkingToggleRemovedInFavorOfPreferences(t *testing.T) {
	sty := styles.CharmtonePantera()
	com := &common.Common{Styles: &sty, Workspace: &mockWorkspace{}}
	c, err := NewCommands(com, "s", true, true, true, false, goal.GoalStatus(""), nil, nil)
	require.NoError(t, err)

	ids := map[string]bool{}
	for _, item := range c.list.FilteredItems() {
		if ci, ok := item.(*CommandItem); ok {
			ids[ci.ID()] = true
		}
	}
	require.False(t, ids["toggle_thinking"], "thinking toggle belongs in Display Preferences now")
	require.True(t, ids["display_preferences"])
}
