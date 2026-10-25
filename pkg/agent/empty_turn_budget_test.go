package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/hackafterdark/phosphor/pkg/message"
)

// TestEmptyTurnContinuationsCarryThroughRecursion pins the reason the count
// travels on the context: sessionAgent.Run recovers from an empty dead-end
// turn by recursing into itself, so the continuation budget has to survive
// the nesting or the cap resets per level.
func TestEmptyTurnContinuationsCarryThroughRecursion(t *testing.T) {
	t.Parallel()

	base := context.Background()
	require.Equal(t, 0, emptyTurnContinuationsFromContext(base), "a turn that never recovered has spent nothing")

	nested := base
	for range 4 {
		nested = withEmptyTurnContinuations(nested, emptyTurnContinuationsFromContext(nested)+1)
	}
	require.Equal(t, 4, emptyTurnContinuationsFromContext(nested), "each recursive level must see the running total")

	// A sibling branch off the same root must not inherit another branch's spend.
	sibling := withEmptyTurnContinuations(base, 9)
	require.Equal(t, 9, emptyTurnContinuationsFromContext(sibling))
	require.Equal(t, 4, emptyTurnContinuationsFromContext(nested))
}

func TestIsEmptyDeadEndTurn(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		msg  *message.Message
		want bool
	}{
		{
			"no tool calls and blank text",
			&message.Message{Role: message.Assistant, Parts: []message.ContentPart{
				message.TextContent{Text: ""},
			}},
			true,
		},
		{
			// The exact confirmed live shape: reasoning plus a bare newline.
			"no tool calls and whitespace-only text",
			&message.Message{Role: message.Assistant, Parts: []message.ContentPart{
				message.ReasoningContent{Thinking: "Next, I'll read the target area for editing."},
				message.TextContent{Text: "\n"},
			}},
			true,
		},
		{
			"no tool calls and real text",
			&message.Message{Role: message.Assistant, Parts: []message.ContentPart{
				message.TextContent{Text: "The temp files were already deleted."},
			}},
			false,
		},
		{
			"tool call present with blank text",
			&message.Message{Role: message.Assistant, Parts: []message.ContentPart{
				message.TextContent{Text: ""},
				message.ToolCall{ID: "1", Name: "bash", Input: "{}"},
			}},
			false,
		},
		{
			"tool call present with real text",
			&message.Message{Role: message.Assistant, Parts: []message.ContentPart{
				message.TextContent{Text: "Reading the target area now."},
				message.ToolCall{ID: "1", Name: "bash", Input: "{}"},
			}},
			false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, c.want, isEmptyDeadEndTurn(c.msg))
		})
	}
}
func TestIsNarrationOnlyDeadEndTurn(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		msg  *message.Message
		want bool
	}{
		{
			// The confirmed live agentic_fetch shape: intent text returned
			// as the final answer instead of findings.
			"single-line intent, no tool call",
			&message.Message{Role: message.Assistant, Parts: []message.ContentPart{
				message.TextContent{Text: "I'll route queries through web_fetch."},
			}},
			true,
		},
		{
			"let-me variant",
			&message.Message{Role: message.Assistant, Parts: []message.ContentPart{
				message.TextContent{Text: "Let me check the config file first."},
			}},
			true,
		},
		{
			"tool call present",
			&message.Message{Role: message.Assistant, Parts: []message.ContentPart{
				message.TextContent{Text: "I'll read the file now."},
				message.ToolCall{ID: "1", Name: "view", Input: "{}"},
			}},
			false,
		},
		{
			"multi-line reply is a real answer",
			&message.Message{Role: message.Assistant, Parts: []message.ContentPart{
				message.TextContent{Text: "I'll note that for later.\n\nThe findings are:\n- alpha\n- beta"},
			}},
			false,
		},
		{
			"genuine final answer without intent prefix",
			&message.Message{Role: message.Assistant, Parts: []message.ContentPart{
				message.TextContent{Text: "The temp files were already deleted."},
			}},
			false,
		},
		{
			"empty text belongs to the empty-turn check",
			&message.Message{Role: message.Assistant, Parts: []message.ContentPart{
				message.TextContent{Text: "  "},
			}},
			false,
		},
		{
			"long reply is never narration-only",
			&message.Message{Role: message.Assistant, Parts: []message.ContentPart{
				message.TextContent{Text: "I'll walk through every detail of the fix here: " +
					strings.Repeat("x", maxNarrationOnlyTurnChars)},
			}},
			false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, c.want, isNarrationOnlyDeadEndTurn(c.msg))
		})
	}
}
