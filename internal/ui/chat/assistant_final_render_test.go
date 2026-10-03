package chat

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/hackafterdark/phosphor/internal/ui/common"
	"github.com/hackafterdark/phosphor/internal/ui/styles"
	"github.com/hackafterdark/phosphor/pkg/message"
	"github.com/stretchr/testify/require"
)

// finalRenderDoc is the assistant body used by the final-frame tests:
// a heading, a second-level heading, a bullet list, and plain
// paragraphs — the shapes that showed the misalignment.
func finalRenderDoc() string {
	return strings.Join([]string{
		"## Patch Release — Stability",
		"",
		"### Agent loop stability",
		"- **Empty-turn recovery** — when a step ends normally but produces nothing actionable (no tool call, no text), the agent now automatically continues the turn with a small bounded budget, so dropped chains of thought no longer stall a response.",
		"",
		"Both recoveries are tracked as internal continuations, keeping per-turn dispatch accounting correct.",
		"",
		"### TUI performance",
		"- **Faster prompt input** — sidebar panel data (workspace-search progress, memory sources/tallies) is now fetched off the render path via background ticks, so a slow store read no longer delays keystrokes.",
	}, "\n")
}

// newFinalRenderMsg builds the streaming (not yet finished) assistant
// message; the tests append the finish part to mimic the turn
// completing in place.
func newFinalRenderMsg(id, content string) *message.Message {
	return &message.Message{
		ID:    id,
		Role:  message.Assistant,
		Parts: []message.ContentPart{message.TextContent{Text: content}},
	}
}

func finishInPlace(msg *message.Message) {
	msg.Parts = append(msg.Parts, message.Finish{
		Reason: message.FinishReasonEndTurn,
		Time:   testFinishTime,
	})
}

// TestAssistantFinalRenderIsFullRender pins the fix for the misaligned
// final frame of a streamed assistant message. Mid-stream the item
// serves a glued render (cached stable prefix + fresh trailing
// fragments), and two concatenated fragment renders wrap each block
// against its own fragment instead of the whole document — headings
// then start at mismatched columns and list items split across lines.
// Once the message is finished the final frame must be a single full
// render so the reader sees the clean layout.
func TestAssistantFinalRenderIsFullRender(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()
	content := finalRenderDoc()

	msg := newFinalRenderMsg("final-render", content)
	item := NewAssistantMessageItem(&sty, msg).(*AssistantMessageItem)

	const width = 120
	// First pass: mid-stream glued render fills the section cache.
	item.RawRender(width)

	// The turn finishes in place (the agent appends the finish part to
	// the same message). The next frame must equal a single full
	// glamour render of the whole document.
	finishInPlace(msg)
	got := item.RawRender(width)

	renderer := common.MarkdownRenderer(&sty, cappedMessageWidth(width))
	mu := common.LockMarkdownRenderer(renderer)
	mu.Lock()
	want, err := renderer.Render(content)
	mu.Unlock()
	require.NoError(t, err)

	require.Equal(t, strings.TrimSuffix(want, "\n"), strings.TrimSuffix(got, "\n"))
}

// TestAssistantFinalRenderLinesFitContentWidth pins the second half of
// the misalignment fix: with the glued fragment render, some rows came
// out a few columns wider than the wrap width, so the terminal hard-
// wrapped them and every following row started at a mismatched column.
// The final frame must have no row wider than the content width it was
// rendered at.
func TestAssistantFinalRenderLinesFitContentWidth(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()
	msg := newFinalRenderMsg("final-width", finalRenderDoc())
	item := NewAssistantMessageItem(&sty, msg).(*AssistantMessageItem)

	const width = 120
	item.RawRender(width) // mid-stream glued render fills the caches
	finishInPlace(msg)
	out := item.RawRender(width)

	limit := cappedMessageWidth(width)
	lines := strings.Split(stripANSI(out), "\n")
	require.NotEmpty(t, lines)
	for i, line := range lines {
		require.LessOrEqualf(t, ansi.StringWidth(line), limit,
			"line %d overflows the content width and will be re-wrapped by the terminal", i+1)
	}
}

// TestTrimGlamourMarginsTrimsPaddedBlanks covers the margin trim used
// when gluing fragment renders. Glamour wraps every visible run in CSI
// sequences, so a blank separator line is not the empty string once
// ANSI is accounted for; the trim has to compare visible glyphs or the
// glued output grows extra blank rows at every seam.
func TestTrimGlamourMarginsTrimsPaddedBlanks(t *testing.T) {
	t.Parallel()

	const styled = "\x1b[1m\x1b[0m\n\x1b[38;2;1;2;3m  \x1b[0m\n\x1b[1mhello\x1b[0m\n\x1b[0m\n"
	require.Equal(t, "\x1b[1mhello\x1b[0m", trimGlamourMargins(styled))
	require.Equal(t, "", trimGlamourMargins("\x1b[0m\n\n"))
	require.Equal(t, "", trimGlamourMargins(""))
}
