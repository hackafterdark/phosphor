package tools

import (
	"testing"

	"github.com/hackafterdark/phosphor/internal/memory"
	"github.com/hackafterdark/phosphor/pkg/config"
	"github.com/stretchr/testify/require"
)

func TestSettingsResolvesTheShippedDefaults(t *testing.T) {
	t.Parallel()
	s := Settings(nil)
	require.Equal(t, 8192, s.MaxInjectBytes)
	require.Equal(t, memory.DefaultSettings().WriteRetries, s.WriteRetries,
		"an unconfigured build has to tolerate a parallel instance holding the index lock")
}

func TestSettingsTakesTheWriteRetryBudgetFromConfig(t *testing.T) {
	t.Parallel()
	cfg := config.NewTestStore(&config.Config{
		Memory: &config.Memory{WriteRetries: 3},
	})
	require.Equal(t, 3, Settings(cfg).WriteRetries,
		"a shared or slow disk has to be able to shorten the contended-write budget")
}

func TestSettingsClampsAConfiguredInjectBudgetToTheCeiling(t *testing.T) {
	t.Parallel()
	cfg := config.NewTestStore(&config.Config{
		Memory: &config.Memory{MaxInjectBytes: 128 * 1024},
	})
	require.Equal(t, memory.MaxInjectBytesCeiling, Settings(cfg).MaxInjectBytes,
		"the render-time CapBlock reads this struct without passing through Open, so this path must clamp too")

	under := config.NewTestStore(&config.Config{
		Memory: &config.Memory{MaxInjectBytes: 4096},
	})
	require.Equal(t, 4096, Settings(under).MaxInjectBytes,
		"a configured budget the ceiling has no business touching has to pass through untouched")
}
