package dialog

import (
	"fmt"
	"testing"

	"github.com/hackafterdark/phosphor/internal/config"
	"github.com/hackafterdark/phosphor/internal/goal"
	"github.com/hackafterdark/phosphor/internal/ui/common"
	"github.com/hackafterdark/phosphor/internal/ui/styles"
	"github.com/hackafterdark/phosphor/internal/workspace"
	"github.com/stretchr/testify/require"
)

type mockWorkspace struct {
	workspace.Workspace
	cfg *config.Config
}

func (m *mockWorkspace) Config() *config.Config {
	if m.cfg == nil {
		m.cfg = &config.Config{}
	}
	return m.cfg
}

func TestCommandItem_DisabledState(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()
	item := NewCommandItem(&sty, "test_cmd", "Test Command", "ctrl+t", nil)
	require.False(t, item.disabled)

	item = item.WithDisabled(true)
	require.True(t, item.disabled)

	// Test render when disabled
	rendered := item.Render(40)
	require.NotEmpty(t, rendered)
}

func TestCommands_PreserveSelection(t *testing.T) {
	sty := styles.CharmtonePantera()
	ws := &mockWorkspace{}
	com := &common.Common{
		Styles:    &sty,
		Workspace: ws,
	}
	c, err := NewCommands(com, "session-123", true, true, true, goal.GoalStatus(""), nil, nil)
	require.NoError(t, err)

	// Test case 1: No query filter, preserve selection index
	c.list.Focus()
	c.list.SetSelected(1)
	require.Equal(t, 1, c.list.Selected())

	// Simulate setting command items again (which happens when Docker MCP is checked or width changes)
	c.setCommandItems(c.selected)

	// Verify that the selection is still index 1
	require.Equal(t, 1, c.list.Selected())

	// Test case 2: With query filter, preserve query and selection
	c.list.SetFilter("clear")
	// Make sure at least one item matches and select it
	filtered := c.list.FilteredItems()
	require.NotEmpty(t, filtered)

	// Let's select the first filtered item (index 0)
	c.list.SetSelected(0)
	selectedItemBefore := c.list.SelectedItem().(*CommandItem)
	selectedIDBefore := selectedItemBefore.ID()

	// Refresh items again
	c.setCommandItems(c.selected)

	// Verify filter query is preserved
	require.Equal(t, "clear", c.list.Query())
	// Verify selected item remains the same
	selectedItemAfter := c.list.SelectedItem().(*CommandItem)
	require.Equal(t, selectedIDBefore, selectedItemAfter.ID())
}

func TestCommands_RenderInitial(t *testing.T) {
	sty := styles.CharmtonePantera()
	ws := &mockWorkspace{}
	com := &common.Common{
		Styles:    &sty,
		Workspace: ws,
	}
	c, err := NewCommands(com, "session-123", true, true, true, goal.GoalStatus(""), nil, nil)
	require.NoError(t, err)

	// Set size of the list
	c.list.SetSize(80, 20)

	fmt.Printf("DEBUG RENDER: len(FilteredItems) = %d\n", len(c.list.FilteredItems()))
	fmt.Printf("DEBUG RENDER: c.list.Len() = %d\n", c.list.Len())
	fmt.Printf("DEBUG RENDER: Selected = %d\n", c.list.Selected())

	// Render the list
	rendered := c.list.Render()
	fmt.Printf("DEBUG RENDER: rendered len = %d\n", len(rendered))
	fmt.Printf("DEBUG RENDER: rendered = %q\n", rendered)

	require.NotEmpty(t, rendered)
}
