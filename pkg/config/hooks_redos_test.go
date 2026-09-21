package config_test

import (
	"testing"

	"github.com/hackafterdark/phosphor/pkg/config"
	"github.com/stretchr/testify/require"
)

func TestValidateHooks_RejectsReDoSMatcher(t *testing.T) {
	t.Parallel()

	bad := &config.Config{Hooks: map[string][]config.HookConfig{
		"PreToolUse": {{Matcher: `(a|aa)+`, Command: "exit 0"}},
	}}
	err := bad.ValidateHooks()
	require.Error(t, err)
	require.Contains(t, err.Error(), "ReDoS")

	good := &config.Config{Hooks: map[string][]config.HookConfig{
		"PreToolUse": {{Matcher: `^edit$`, Command: "exit 0"}},
	}}
	require.NoError(t, good.ValidateHooks())
}
