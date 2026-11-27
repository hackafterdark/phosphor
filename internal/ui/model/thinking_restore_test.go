package model

import (
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/hackafterdark/phosphor/internal/memory"
	memoryui "github.com/hackafterdark/phosphor/internal/memory/ui"
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

// TestRestoreOmittedAssistantItemsAttachesMemoryPill asserts a restored
// assistant item receives the same memory provenance pill that load-time
// extraction wires from the turn's stored tool results, so toggling thinking
// on does not leave the pill missing until the session reloads.
func TestRestoreOmittedAssistantItemsAttachesMemoryPill(t *testing.T) {
	t.Parallel()

	sources := []memoryui.Source{
		{ID: "mem-one", Type: "decision", Status: "active", Summary: "First claim.", Trust: 0.8},
		{ID: "mem-two", Type: "constraint", Status: "active", Summary: "Second claim.", Trust: 0.5},
	}
	msgs := []message.Message{
		{
			ID:   "a1",
			Role: message.Assistant,
			Parts: []message.ContentPart{
				message.ReasoningContent{Thinking: "pondering", FinishedAt: 1},
				message.ToolCall{ID: "tc1", Name: memory.SearchToolName, Finished: true},
				message.Finish{Reason: message.FinishReasonToolUse, Time: 2},
			},
		},
		{
			ID:   "r1",
			Role: message.Tool,
			Parts: []message.ContentPart{
				message.ToolResult{ToolCallID: "tc1", Name: memory.SearchToolName, Content: memoryui.Card(2, sources)},
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

	// Load-time extraction with thinking hidden omits the assistant item;
	// the memory result sits in the loaded message state beside it.
	toolResults := chat.BuildToolResultMap([]*message.Message{&msgs[0], &msgs[1]})
	for i := range msgs {
		ui.chat.AppendMessages(chat.ExtractMessageItems(ui.com.Styles, &msgs[i], toolResults, false, nil)...)
	}
	require.Nil(t, ui.chat.MessageItem("a1"))

	ui.restoreOmittedAssistantItems()

	assistant, ok := ui.chat.MessageItem("a1").(*chat.AssistantMessageItem)
	require.True(t, ok, "the omitted assistant item must be restored")
	rendered := ansi.Strip(assistant.RawRender(100))
	require.Contains(t, rendered, "+2 memories", "the restored item must carry the same memory pill the extractor would have wired")
}
