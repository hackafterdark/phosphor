package agent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestUnrecognizedToolCallContinuationsCarryThroughRecursion pins the reason
// the count travels on the context: sessionAgent.Run recovers from an
// unrecognized qwen3_xml tool-call block by recursing into itself, so the
// continuation budget has to survive the nesting or the cap resets per level.
func TestUnrecognizedToolCallContinuationsCarryThroughRecursion(t *testing.T) {
	t.Parallel()

	base := context.Background()
	require.Equal(t, 0, unrecognizedToolCallContinuationsFromContext(base), "a turn that never recovered has spent nothing")

	nested := base
	for range 4 {
		nested = withUnrecognizedToolCallContinuations(nested, unrecognizedToolCallContinuationsFromContext(nested)+1)
	}
	require.Equal(t, 4, unrecognizedToolCallContinuationsFromContext(nested), "each recursive level must see the running total")

	// A sibling branch off the same root must not inherit another branch's spend.
	sibling := withUnrecognizedToolCallContinuations(base, 9)
	require.Equal(t, 9, unrecognizedToolCallContinuationsFromContext(sibling))
	require.Equal(t, 4, unrecognizedToolCallContinuationsFromContext(nested))
}

func TestLooksLikeUnrecognizedToolCall(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		text string
		want bool
	}{
		{
			"the confirmed live shape",
			"<tool_call>\n<function=read>\n<parameter=file_path>\nF:\\x\\reef.js\n</parameter>\n</function>\n</tool_call>",
			true,
		},
		{"leading whitespace tolerated", "  \n<tool_call>\n<function=write>", true},
		{"ordinary final answer", "I've finished updating reef.js with the fix.", false},
		{"mentions tool_call in prose, not at the start", "Note: a <tool_call> tag appeared in the output earlier.", false},
		{"tool_call tag without function tag", "<tool_call>some other shape</tool_call>", false},
		{"empty text", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, c.want, looksLikeUnrecognizedToolCall(c.text))
		})
	}
}
