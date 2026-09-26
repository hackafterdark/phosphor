package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNormalizeHookEventCanonicalizesTurnEvents(t *testing.T) {
	t.Parallel()

	require.Equal(t, "PreToolUse", normalizeHookEvent("pre_tool_use"))
	require.Equal(t, "Stop", normalizeHookEvent("stop"))
	require.Equal(t, "Stop", normalizeHookEvent("STOP"))
	require.Equal(t, "SessionEnd", normalizeHookEvent("session_end"))
	require.Equal(t, "SessionEnd", normalizeHookEvent("SessionEnd"))
	require.Equal(t, "something", normalizeHookEvent("something"))
}

func TestValidateHooksCanonicalizesTurnEventKeys(t *testing.T) {
	t.Parallel()

	c := &Config{Hooks: map[string][]HookConfig{
		"stop":        {{Command: `echo ok`}},
		"session_end": {{Command: `echo ok`}},
	}}
	require.NoError(t, c.ValidateHooks())
	require.Contains(t, c.Hooks, "Stop")
	require.Contains(t, c.Hooks, "SessionEnd")
	require.NotContains(t, c.Hooks, "stop")
	require.NotContains(t, c.Hooks, "session_end")
}
