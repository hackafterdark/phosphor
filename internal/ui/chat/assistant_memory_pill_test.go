package chat

import (
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/hackafterdark/phosphor/internal/memory"
	memoryui "github.com/hackafterdark/phosphor/internal/memory/ui"
	"github.com/hackafterdark/phosphor/internal/ui/styles"
	"github.com/hackafterdark/phosphor/pkg/message"
	"github.com/stretchr/testify/require"
)

func pillSources() []memoryui.Source {
	return []memoryui.Source{
		{ID: "mem-one", Type: "decision", Status: "active", Thread: "memory-system", Summary: "First claim.", Origin: "session#a@1:2", Why: "score 9", Trust: 0.8, Role: memoryui.RoleRecalled},
		{ID: "mem-two", Type: "constraint", Status: "active", Summary: "Second claim.", Trust: 0.5, Role: memoryui.RoleActed},
	}
}

func memoryTurnMessage(results map[string]message.ToolResult) (*message.Message, map[string]message.ToolResult) {
	msg := &message.Message{ID: "m-mem", Role: message.Assistant}
	msg.SetContent("Answer.")
	msg.AddToolCall(message.ToolCall{ID: "tc-1", Name: memory.SearchToolName, Finished: true})
	if results == nil {
		results = map[string]message.ToolResult{
			"tc-1": {ToolCallID: "tc-1", Name: memory.SearchToolName, Content: memoryui.Card(2, pillSources())},
		}
	}
	return msg, results
}

func TestAssistantMemoryPillCollapsedShowsOnlyTheCount(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()
	msg, _ := memoryTurnMessage(nil)
	item := NewAssistantMessageItem(&sty, msg).(*AssistantMessageItem)
	item.SetMemorySources(pillSources())

	rendered := ansi.Strip(item.RawRender(100))
	require.Contains(t, rendered, "+2 memories")
	require.Contains(t, rendered, "click or space to expand")
	require.NotContains(t, rendered, "mem-one", "collapsed is the cheap state: a count, not a recap")
}

func TestAssistantMemoryPillExpandsThroughTheSamePathAsThinking(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()
	msg, _ := memoryTurnMessage(nil)
	item := NewAssistantMessageItem(&sty, msg).(*AssistantMessageItem)
	item.SetMemorySources(pillSources())

	exp, ok := any(item).(Expandable)
	require.True(t, ok)

	require.True(t, exp.ToggleExpanded(), "a pill alone is enough to be expandable")
	rendered := ansi.Strip(item.RawRender(100))
	require.Contains(t, rendered, "mem-one")
	require.Contains(t, rendered, "thread: memory-system")
	require.Contains(t, rendered, "source: session#a@1:2")
	require.Contains(t, rendered, "trust 0.80")

	require.False(t, exp.ToggleExpanded())
	rendered = ansi.Strip(item.RawRender(100))
	require.NotContains(t, rendered, "mem-one")
}

func TestAssistantMemoryPillIsClickable(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()
	msg, _ := memoryTurnMessage(nil)
	item := NewAssistantMessageItem(&sty, msg).(*AssistantMessageItem)
	item.SetMemorySources(pillSources())
	item.RawRender(100) // renders once so the pill's row is known

	require.False(t, item.HandleMouseClick(ansi.MouseLeft, 0, 0), "the answer itself must stay selectable")
	pillRow := item.memoryRow
	require.Greater(t, pillRow, 0)
	require.True(t, item.HandleMouseClick(ansi.MouseLeft, 0, pillRow))
}

func TestAssistantMemoryPillNoSourcesDrawsNothing(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()
	msg, _ := memoryTurnMessage(nil)
	item := NewAssistantMessageItem(&sty, msg).(*AssistantMessageItem)

	rendered := ansi.Strip(item.RawRender(100))
	require.NotContains(t, rendered, "memories")

	exp, _ := any(item).(Expandable)
	require.False(t, exp.ToggleExpanded(), "no pill and no thinking means nothing to expand")
}

func TestExtractMessageItemsWiresThePillFromToolResults(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()
	msg, results := memoryTurnMessage(nil)

	items := ExtractMessageItems(&sty, msg, results)
	require.NotEmpty(t, items)

	var assistant *AssistantMessageItem
	for _, item := range items {
		if a, ok := item.(*AssistantMessageItem); ok {
			assistant = a
		}
	}
	require.NotNil(t, assistant)
	rendered := ansi.Strip(assistant.RawRender(100))
	require.Contains(t, rendered, "+2 memories", "the extractor has both the calls and the results, so the pill needs no live tracking")
}

func TestExtractMessageItemsNoPillForForeignResults(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()
	msg, results := memoryTurnMessage(map[string]message.ToolResult{
		"tc-1": {ToolCallID: "tc-1", Name: "bash", Content: "Recalled 1 memory:\n1. [not-a] (card)\n"},
	})

	items := ExtractMessageItems(&sty, msg, results)
	for _, item := range items {
		if a, ok := item.(*AssistantMessageItem); ok {
			require.NotContains(t, ansi.Strip(a.RawRender(100)), "memories")
		}
	}
}
