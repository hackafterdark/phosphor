package tools

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/exp/golden"
	"github.com/hackafterdark/phosphor/pkg/otel"
	"github.com/stretchr/testify/require"
)

func TestPhosphorTraceDescription(t *testing.T) {
	t.Parallel()

	golden.RequireEqual(t, phosphorTraceDescription())
}

func TestFormatTrace(t *testing.T) {
	t.Parallel()

	summaries := []otel.SpanSummary{
		{
			Name: "execute_tool bash", SpanID: "3", ParentID: "2", TraceID: "t", DurationMs: 4.2,
			Attributes: map[string]string{"gen_ai.tool.name": "bash"},
		},
		{
			Name: "chat qwen3", SpanID: "2", ParentID: "1", TraceID: "t", DurationMs: 900.5,
			Status:     "Error: token overflow",
			Attributes: map[string]string{"gen_ai.provider.name": "openai", "gen_ai.request.model": "qwen3", "gen_ai.step.index": "2", "gen_ai.response.finish_reason": "tool_calls", "gen_ai.usage.input_tokens": "120", "gen_ai.usage.output_tokens": "34", "unused": "x"},
		},
		{
			Name: "invoke_agent Phosphor", SpanID: "1", TraceID: "t", DurationMs: 1000.0,
			Attributes: map[string]string{"gen_ai.operation.name": "invoke_agent"},
		},
	}

	out := formatTrace(summaries)
	lines := strings.Split(out, "\n")
	require.Len(t, lines, 3)
	require.True(t, strings.HasPrefix(lines[0], "invoke_agent Phosphor (1000.0ms)"), lines[0])
	require.True(t, strings.HasPrefix(lines[1], "  chat qwen3 (900.5ms) status=Error: token overflow"), lines[1])
	require.Contains(t, lines[1], "gen_ai.step.index=2")
	require.Contains(t, lines[1], "gen_ai.response.finish_reason=tool_calls")
	require.Contains(t, lines[1], "gen_ai.usage.input_tokens=120")
	require.Contains(t, lines[1], "gen_ai.usage.output_tokens=34")
	require.True(t, strings.HasPrefix(lines[2], "    execute_tool bash (4.2ms) gen_ai.tool.name=bash"), lines[2])
	require.NotContains(t, out, "unused", "non-preferred attributes are dropped")
}

func TestRunPhosphorTraceDisabled(t *testing.T) {
	t.Parallel()

	out := runPhosphorTrace(PhosphorTraceParams{})
	require.Contains(t, out, "memory_buffer", "the disabled path points at the config key")
}
