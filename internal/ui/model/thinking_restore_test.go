package model

import (
	"context"
	"testing"

	"github.com/hackafterdark/phosphor/internal/ui/chat"
	"github.com/hackafterdark/phosphor/internal/ui/common"
	"github.com/hackafterdark/phosphor/pkg/config"
	"github.com/hackafterdark/phosphor/pkg/message"
	"github.com/hackafterdark/phosphor/pkg/session"
	"github.com/stretchr/testify/require"
)

// thinkingRestoreWorkspace serves stored messages to the restoration path.
type thinkingRestoreWorkspace struct {
	toolOutputTestWorkspace
	msgs []message.Message
}

func (w *thinkingRestoreWorkspace) ListMessages(context.Context, string) ([]message.Message, error) {
	return w.msgs, nil
}

// TestRestoreOmittedAssistantItemsMultiple walks the thinking toggle-on path
// with two reasoning-only assistant messages omitted at load and asserts
// every one is restored directly before its tool items, verifying the
// restore loop accounts for the index shifts its own insertions cause.
func TestRestoreOmittedAssistantItemsMultiple(t *testing.T) {
	t.Parallel()

	mk := func(id, toolID string) (message.Message, message.Message) {
		assistant := message.Message{
			ID:    id,
			Role:  message.Assistant,
			Parts: []message.ContentPart{message.ReasoningContent{Thinking: "pondering " + id}},
		}
		tool := message.Message{
			ID:    id + "-tools",
			Role:  message.Assistant,
			Parts: []message.ContentPart{message.ToolCall{ID: toolID, Name: "probe", Finished: true}},
		}
		return assistant, tool
	}
	a1, t1 := mk("a1", "tc1")
	a2, t2 := mk("a2", "tc2")

	ws := &thinkingRestoreWorkspace{
		toolOutputTestWorkspace: toolOutputTestWorkspace{cfg: &config.Config{Options: &config.Options{}}},
		msgs:                    []message.Message{a1, t1, a2, t2},
	}
	com := common.DefaultCommon(nil)
	com.Workspace = ws
	ui := &UI{
		com:     com,
		chat:    NewChat(com),
		session: &session.Session{ID: "s1"},
	}

	// Load-time extraction with thinking hidden omits both assistant
	// messages (reasoning-only), leaving just the two tool items.
	ui.chat.AppendMessages(
		chat.NewToolMessageItem(ui.com.Styles, a1.ID, t1.ToolCalls()[0], nil, false),
		chat.NewToolMessageItem(ui.com.Styles, a2.ID, t2.ToolCalls()[0], nil, false),
	)
	require.Nil(t, ui.chat.MessageItem(a1.ID))
	require.Nil(t, ui.chat.MessageItem(a2.ID))

	ui.restoreOmittedAssistantItems()

	// Both omitted assistant items must now exist...
	require.NotNil(t, ui.chat.MessageItem(a1.ID))
	require.NotNil(t, ui.chat.MessageItem(a2.ID))

	// ...each directly before its own tool item, preserving transcript
	// order (tool items identify by tool-call ID).
	got := make([]string, 0, ui.chat.Len())
	for i := range ui.chat.Len() {
		item, ok := ui.chat.ItemAt(i).(chat.MessageItem)
		require.True(t, ok)
		got = append(got, item.ID())
	}
	require.Equal(t, []string{a1.ID, "tc1", a2.ID, "tc2"}, got)
}
