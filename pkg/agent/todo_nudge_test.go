package agent

import (
	"context"
	"testing"

	"charm.land/fantasy"
	"github.com/hackafterdark/phosphor/pkg/agent/tools"
	"github.com/hackafterdark/phosphor/pkg/session"
	"github.com/stretchr/testify/require"
)

type stubTool struct{ name string }

func (s *stubTool) Info() fantasy.ToolInfo {
	return fantasy.ToolInfo{Name: s.name, Description: "stub"}
}

func (s *stubTool) ProviderOptions() fantasy.ProviderOptions { return nil }

func (s *stubTool) SetProviderOptions(fantasy.ProviderOptions) {}

func (s *stubTool) Run(context.Context, fantasy.ToolCall) (fantasy.ToolResponse, error) {
	return fantasy.NewTextResponse("ok"), nil
}

// TestTodoNudgeFiresAfterThreshold verifies the drift nudge appends once to
// the tool result at the threshold and that a todos call resets the counter.
func TestTodoNudgeFiresAfterThreshold(t *testing.T) {
	t.Parallel()
	env := testEnv(t)
	sess, err := env.sessions.Create(t.Context(), "nudge")
	require.NoError(t, err)
	sess.Todos = []session.Todo{
		{Content: "Ship it", Status: session.TodoStatusInProgress, ActiveForm: "Shipping it"},
	}
	_, err = env.sessions.Save(t.Context(), sess)
	require.NoError(t, err)

	ctx := context.WithValue(t.Context(), tools.SessionIDContextKey, sess.ID)
	wrapped := wrapToolsWithTodoNudge([]fantasy.AgentTool{&stubTool{name: "edit"}}, env.sessions)
	tool := wrapped[0]

	var last fantasy.ToolResponse
	for range todoNudgeThreshold - 1 {
		last, err = tool.Run(ctx, fantasy.ToolCall{ID: "c", Name: "edit"})
		require.NoError(t, err)
		require.NotContains(t, last.Content, "[Reminder:")
	}
	last, err = tool.Run(ctx, fantasy.ToolCall{ID: "c", Name: "edit"})
	require.NoError(t, err)
	require.Contains(t, last.Content, `[Reminder: Active todo in progress: "Shipping it"`)

	// A todos call resets the counter; the nudge stays quiet again until
	// the threshold is crossed once more.
	_, err = tool.Run(ctx, fantasy.ToolCall{ID: "c2", Name: tools.TodosToolName})
	require.NoError(t, err)
	for range todoNudgeThreshold - 1 {
		last, err = tool.Run(ctx, fantasy.ToolCall{ID: "c3", Name: "edit"})
		require.NoError(t, err)
		require.NotContains(t, last.Content, "[Reminder:")
	}
}

// TestTodoNudgeSilentWithoutOpenTodos verifies sessions with no open todos
// never receive the nudge.
func TestTodoNudgeSilentWithoutOpenTodos(t *testing.T) {
	t.Parallel()
	env := testEnv(t)
	sess, err := env.sessions.Create(t.Context(), "nudge-empty")
	require.NoError(t, err)

	ctx := context.WithValue(t.Context(), tools.SessionIDContextKey, sess.ID)
	wrapped := wrapToolsWithTodoNudge([]fantasy.AgentTool{&stubTool{name: "bash"}}, env.sessions)
	for range todoNudgeThreshold * 2 {
		last, err := wrapped[0].Run(ctx, fantasy.ToolCall{ID: "c", Name: "bash"})
		require.NoError(t, err)
		require.NotContains(t, last.Content, "[Reminder:")
	}
}
