package model

import (
	"testing"

	"github.com/hackafterdark/phosphor/internal/ui/chat"
	"github.com/hackafterdark/phosphor/pkg/config"
	"github.com/hackafterdark/phosphor/pkg/message"
	"github.com/stretchr/testify/require"
)

// rawRenderer exposes the underlying cache-bypassing render of a tool item
// for assertions.
type rawRenderer interface {
	RawRender(width int) string
}

// TestCollapsedToolItemClickExpands walks the real click path (mouse down ->
// delayed click) for a tool item rendered in collapsed mode and asserts the
// body becomes visible, catching any break in the click-to-expand routing.
func TestCollapsedToolItemClickExpands(t *testing.T) {
	u := newTestUI()
	u.updateLayoutAndSize()

	toolCall := message.ToolCall{
		ID:       "tc-1",
		Name:     "test_tool",
		Input:    `{"k":"v"}`,
		Finished: true,
	}
	result := &message.ToolResult{
		Content: "line one\nline two\nline three",
	}
	item := chat.NewToolMessageItem(u.com.Styles, "msg-1", toolCall, result, false)
	item.SetOutputMode(config.ToolOutputCollapsed, 0)

	u.chat.SetMessages(item)
	u.chat.SetSize(u.layout.main.Dx(), u.layout.main.Dy())
	u.chat.SetSelected(0)

	// Render once in the collapsed state so the item's render cache is
	// populated with the header-only output, as it is in the live TUI.
	rr := item.(rawRenderer)
	require.NotContains(t, rr.RawRender(u.layout.main.Dx()), "line one")

	_, cmd := u.chat.HandleMouseDown(0, 0)
	require.NotNil(t, cmd, "single click must schedule the delayed click")

	handled, _ := u.chat.HandleDelayedClick(DelayedClickMsg{ClickID: 1, ItemIdx: 0, X: 0, Y: 0})
	require.True(t, handled, "delayed click must be handled by the tool item")

	// The delayed click toggled expansion; the cached collapsed body must
	// have been invalidated so the next render reveals the result.
	rendered := rr.RawRender(u.layout.main.Dx())
	require.Contains(t, rendered, "line one", "expanded click did not reveal the collapsed body")
}
