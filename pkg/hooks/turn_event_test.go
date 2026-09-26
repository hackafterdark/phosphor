package hooks

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/hackafterdark/phosphor/pkg/config"
	"github.com/stretchr/testify/require"
)

func TestBuildTurnPayload(t *testing.T) {
	t.Parallel()

	payload := BuildTurnPayload(EventStop, "sess-9", "/work", "we decided to ship", []string{"edit", "bash"})

	var got TurnPayload
	require.NoError(t, json.Unmarshal(payload, &got))
	require.Equal(t, EventStop, got.Event)
	require.Equal(t, "sess-9", got.SessionID)
	require.Equal(t, "/work", got.CWD)
	require.Equal(t, "we decided to ship", got.Text)
	require.Equal(t, []string{"edit", "bash"}, got.ToolCalls)
}

func TestBuildTurnPayloadOmitsEmptyToolCalls(t *testing.T) {
	t.Parallel()

	payload := BuildTurnPayload(EventSessionEnd, "sess-9", "/work", "done", nil)
	require.NotContains(t, string(payload), "tool_calls")
}

func TestBuildTurnEnvCarriesSessionAndNoTool(t *testing.T) {
	t.Parallel()

	env := BuildTurnEnv(EventStop, "sess-9", "/work", "/proj")
	require.Contains(t, env, "PHOSPHOR_EVENT=Stop")
	require.Contains(t, env, "PHOSPHOR_SESSION_ID=sess-9")
	require.Contains(t, env, "PHOSPHOR_CWD=/work")
	require.Contains(t, env, "PHOSPHOR_PROJECT_DIR=/proj")

	// The turn-level events have no tool, so no tool variables may leak in.
	for _, entry := range env {
		require.NotContains(t, entry, "PHOSPHOR_TOOL_NAME=")
		require.NotContains(t, entry, "PHOSPHOR_TOOL_INPUT")
	}
}

func TestRunEventWithNoHooks(t *testing.T) {
	t.Parallel()

	r := NewRunner(nil, t.TempDir(), t.TempDir())
	agg, err := r.RunEvent(context.Background(), EventStop, "sess", BuildTurnPayload(EventStop, "sess", "/w", "hi", nil))
	require.NoError(t, err)
	require.Equal(t, DecisionNone, agg.Decision)
	require.Empty(t, agg.Hooks)
}

func TestRunEventCollectsDecisionAndContext(t *testing.T) {
	t.Parallel()

	r := NewRunner([]config.HookConfig{
		{Command: `echo '{"decision":"allow","context":"remember this"}'`},
	}, t.TempDir(), t.TempDir())

	agg, err := r.RunEvent(context.Background(), EventStop, "sess", BuildTurnPayload(EventStop, "sess", "/w", "hi", nil))
	require.NoError(t, err)
	require.Equal(t, DecisionAllow, agg.Decision)
	require.Equal(t, "remember this", agg.Context)
	require.Len(t, agg.Hooks, 1)
	require.False(t, agg.Hooks[0].InputRewrite)
}

func TestRunEventDedupsRepeatedCommands(t *testing.T) {
	t.Parallel()

	cmd := `echo '{"decision":"allow"}'`
	r := NewRunner([]config.HookConfig{{Command: cmd}, {Command: cmd}}, t.TempDir(), t.TempDir())

	agg, err := r.RunEvent(context.Background(), EventSessionEnd, "sess", BuildTurnPayload(EventSessionEnd, "sess", "/w", "hi", nil))
	require.NoError(t, err)
	require.Len(t, agg.Hooks, 1)
}

func TestRunEventHonoursHalt(t *testing.T) {
	t.Parallel()

	r := NewRunner([]config.HookConfig{
		{Command: `echo "stop the run" >&2; exit 49`},
	}, t.TempDir(), t.TempDir())

	agg, err := r.RunEvent(context.Background(), EventStop, "sess", BuildTurnPayload(EventStop, "sess", "/w", "hi", nil))
	require.NoError(t, err)
	require.True(t, agg.Halt)
}
