package chat

import (
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/hackafterdark/phosphor/internal/ui/styles"
	"github.com/hackafterdark/phosphor/pkg/message"
	"github.com/stretchr/testify/require"
)

// toolCallThinkingMessage builds an assistant message that reasoned and then
// handed off to a tool call without writing any text.
func toolCallThinkingMessage(id, thinking string) *message.Message {
	return &message.Message{
		ID:   id,
		Role: message.Assistant,
		Parts: []message.ContentPart{
			message.ReasoningContent{
				Thinking:   thinking,
				StartedAt:  testStartedAt,
				FinishedAt: testFinishedAt,
			},
			message.ToolCall{ID: "tc1", Name: "view", Input: `{"file_path":"x"}`, Finished: true},
		},
	}
}

// TestThinkingHiddenFromAssistantMessage pins the show_thinking=false render:
// the reasoning trace is dropped from the item but the answer stays, and the
// reasoning itself remains on the message so the model context is untouched.
func TestThinkingHiddenFromAssistantMessage(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()
	msg := thinkingMessage("m1", "secret plan of attack", "the visible answer")
	item := NewAssistantMessageItem(&sty, msg).(*AssistantMessageItem)

	visible := ansi.Strip(item.RawRender(80))
	require.Contains(t, visible, "secret plan of attack",
		"thinking must render by default")

	require.True(t, item.SetThinkingVisible(false), "hiding must report a change")
	hidden := ansi.Strip(item.RawRender(80))
	require.NotContains(t, hidden, "secret plan of attack",
		"thinking must not render when hidden")
	require.Contains(t, hidden, "the visible answer",
		"the final response must survive hiding thinking")
	require.Equal(t, "secret plan of attack", msg.ReasoningContent().Thinking,
		"hiding is display-only; the reasoning stays on the message")

	require.False(t, item.SetThinkingVisible(false), "re-hiding must report no change")

	require.True(t, item.SetThinkingVisible(true), "restoring must report a change")
	require.Contains(t, ansi.Strip(item.RawRender(80)), "secret plan of attack",
		"thinking must render again after restoring")
}

// TestThinkingHiddenClearsClickTarget guards the click region: the thinking
// box height doubles as the expansion click target, so hiding thinking must
// zero it out rather than leave a stale value pointing at nothing.
func TestThinkingHiddenClearsClickTarget(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()
	msg := thinkingMessage("m1", "pondering", "answer")
	item := NewAssistantMessageItem(&sty, msg).(*AssistantMessageItem)

	item.RawRender(80)
	require.Positive(t, item.thinkingBoxHeight,
		"the rendered thinking box must be a click target")

	item.SetThinkingVisible(false)
	item.RawRender(80)
	require.Zero(t, item.thinkingBoxHeight,
		"hiding thinking must clear the click target")
	require.False(t, item.HandleMouseClick(ansi.MouseLeft, 0, 0),
		"clicks must not toggle a hidden thinking block")
}

// TestThinkingHiddenSkipsExpansionCycle guards the shared toggle path: with
// thinking hidden, toggling must leave the thinking view mode alone so the
// memory pill (the other half of the path) stays the only thing that can
// expand.
func TestThinkingHiddenSkipsExpansionCycle(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()
	msg := thinkingMessage("m1", "pondering", "answer")
	item := NewAssistantMessageItem(&sty, msg).(*AssistantMessageItem)
	item.SetThinkingVisible(false)

	require.False(t, item.ToggleExpanded(),
		"toggling with thinking hidden must report nothing expanded")
	require.Equal(t, thinkingCollapsed, item.thinkingViewMode,
		"thinking view mode must not advance while hidden")
}

// TestShouldRenderAssistantMessageThinkingHidden pins the gate that keeps an
// empty block from being drawn when thinking is the only thing on a
// tool-calling turn, while a live reasoning turn keeps its spinner.
func TestShouldRenderAssistantMessageThinkingHidden(t *testing.T) {
	t.Parallel()

	toolTurn := toolCallThinkingMessage("m1", "pondering")
	require.True(t, ShouldRenderAssistantMessage(toolTurn, true),
		"a tool turn with thinking renders while thinking is visible")
	require.False(t, ShouldRenderAssistantMessage(toolTurn, false),
		"a tool turn whose only text is thinking must not render when hidden")

	live := &message.Message{
		ID:    "m2",
		Role:  message.Assistant,
		Parts: []message.ContentPart{message.ReasoningContent{Thinking: "pondering", StartedAt: testStartedAt}},
	}
	require.True(t, ShouldRenderAssistantMessage(live, false),
		"a live reasoning turn must still render so its spinner shows")

	contentTurn := thinkingMessage("m3", "pondering", "the answer")
	contentTurn.Parts = append(contentTurn.Parts,
		message.ToolCall{ID: "tc1", Name: "view", Input: `{}`, Finished: true})
	require.True(t, ShouldRenderAssistantMessage(contentTurn, false),
		"a tool turn with an answer must render regardless of thinking visibility")
}

// TestThinkingHiddenLeavesNoBox pins the composition: with thinking hidden,
// an assistant turn whose only text is reasoning draws no thinking box at all
// (only the spinner remains while the turn is unfinished).
func TestThinkingHiddenLeavesNoBox(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()
	msg := thinkingMessage("m1", "pondering away", "")
	item := NewAssistantMessageItem(&sty, msg).(*AssistantMessageItem)
	item.SetThinkingVisible(false)

	out := ansi.Strip(item.RawRender(80))
	require.NotContains(t, out, "pondering away",
		"a hidden-thinking turn must not draw the reasoning text")
}
