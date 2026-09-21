package hooks

import (
	"testing"

	"github.com/hackafterdark/phosphor/pkg/config"
	"github.com/stretchr/testify/require"
)

func TestNewRunner_SkipsReDoSHookMatcher(t *testing.T) {
	t.Parallel()

	hooks := []config.HookConfig{
		{Matcher: `(a+)+`, Command: "exit 0"},
		{Matcher: `^bash$`, Command: "exit 0"},
	}
	runner := NewRunner(hooks, t.TempDir(), t.TempDir())

	kept := runner.Hooks()
	require.Len(t, kept, 1, "the ReDoS-shaped matcher must be skipped, not compiled")
	require.Equal(t, `^bash$`, kept[0].Matcher)

	agg, err := runner.Run(t.Context(), "PreToolUse", "session", "grep", "{}")
	require.NoError(t, err)
	require.Equal(t, DecisionNone, agg.Decision)
	require.Equal(t, 0, agg.HookCount)
}

func TestNewRunner_KeepsSafeHookMatchers(t *testing.T) {
	t.Parallel()

	hooks := []config.HookConfig{
		{Matcher: `^(bash|edit)$`, Command: "exit 0"},
		{Command: "exit 0"},
	}
	runner := NewRunner(hooks, t.TempDir(), t.TempDir())
	require.Len(t, runner.Hooks(), 2)
}
