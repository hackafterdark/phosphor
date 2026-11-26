package model

import (
	"testing"

	"github.com/hackafterdark/phosphor/internal/ui/chat"
	"github.com/hackafterdark/phosphor/internal/ui/common"
	"github.com/hackafterdark/phosphor/pkg/message"
	"github.com/hackafterdark/phosphor/pkg/session"
	"github.com/stretchr/testify/require"
)

// TestRestoreOmittedAssistantItemsMultiple loads two assistant messages that
// each mix reasoning with a tool call through the real load-time extraction
// with thinking hidden — the path that omits the assistant item while its
// tool item is still created — and asserts the thinking toggle-on restore
// re-inserts every omitted assistant directly before its own tool items,
// verifying the restore loop accounts for the index shifts its own
// insertions cause.
func TestRestoreOmittedAssistantItemsMultiple(t *testing.T) {
	t.Parallel()

	msgs := []message.Message{
		{
			ID:   "a1",
			Role: message.Assistant,
			Parts: []message.ContentPart{
				message.ReasoningContent{Thinking: "pondering a1", FinishedAt: 1},
				message.ToolCall{ID: "tc1", Name: "probe", Finished: true},
				message.Finish{Reason: message.FinishReasonToolUse, Time: 2},
			},
		},
		{
			ID:   "a2",
			Role: message.Assistant,
			Parts: []message.ContentPart{
				message.ReasoningContent{Thinking: "pondering a2", FinishedAt: 1},
				message.ToolCall{ID: "tc2", Name: "probe", Finished: true},
				message.Finish{Reason: message.FinishReasonToolUse, Time: 2},
			},
		},
	}

	com := common.DefaultCommon(nil)
	ui := &UI{
		com:                com,
		chat:               NewChat(com),
		session:            &session.Session{ID: "s1"},
		allSessionMessages: msgs,
	}

	// Load-time extraction with thinking hidden omits both assistant items
	// (reasoning is their only content besides the tool call), leaving just
	// the tool items parented to the omitted assistant messages.
	// allSessionMessages is what the restoration path reads from, and it is
	// seeded above exactly as session load would seed it.
	ptrs := make([]*message.Message, len(msgs))
	for i := range msgs {
		ptrs[i] = &msgs[i]
	}
	toolResults := chat.BuildToolResultMap(ptrs)
	for i := range msgs {
		items := chat.ExtractMessageItems(ui.com.Styles, &msgs[i], toolResults, false, nil)
		ui.chat.AppendMessages(items...)
	}
	require.Equal(t, 2, ui.chat.Len(), "thinking-hidden extraction must omit both assistant items")
	require.Nil(t, ui.chat.MessageItem("a1"))
	require.Nil(t, ui.chat.MessageItem("a2"))

	ui.restoreOmittedAssistantItems()

	// Both omitted assistant items must now exist...
	require.NotNil(t, ui.chat.MessageItem("a1"))
	require.NotNil(t, ui.chat.MessageItem("a2"))

	// ...each directly before its own tool item, preserving transcript
	// order (tool items identify by tool-call ID).
	got := make([]string, 0, ui.chat.Len())
	for i := range ui.chat.Len() {
		item, ok := ui.chat.ItemAt(i).(chat.MessageItem)
		require.True(t, ok)
		got = append(got, item.ID())
	}
	require.Equal(t, []string{"a1", "tc1", "a2", "tc2"}, got)
}
