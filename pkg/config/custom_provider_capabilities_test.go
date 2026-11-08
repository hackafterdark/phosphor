package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestCustomProviderCapabilitiesRoundTrip pins that capabilities fields
// written by the provider wizard survive the full load pipeline, so
// compaction and attachment handling see the user's choices.
func TestCustomProviderCapabilitiesRoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PHOSPHOR_GLOBAL_CONFIG", dir)
	t.Setenv("PHOSPHOR_GLOBAL_DATA", dir)

	configJSON := `{
		"providers": {
			"ollama": {
				"name": "ollama",
				"type": "openai-compat",
				"base_url": "http://localhost:11434/v1",
				"models": [
					{
						"id": "qwen3-32b",
						"name": "qwen3-32b",
						"context_window": 32768,
						"supports_attachments": true
					}
				]
			}
		}
	}`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "phosphor.json"), []byte(configJSON), 0o600))

	store, err := Load(dir, dir, false)
	require.NoError(t, err)

	cfg := store.Config()
	provider, ok := cfg.Providers.Get("ollama")
	require.True(t, ok, "custom provider must be present")
	require.Len(t, provider.Models, 1)
	require.Equal(t, int64(32768), provider.Models[0].ContextWindow)
	require.True(t, provider.Models[0].SupportsImages)
}
