package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/hackafterdark/phosphor/internal/home"
	"github.com/stretchr/testify/require"
)

func TestSetupGrokSubscription(t *testing.T) {
	// Force the fallback path so the assertion is deterministic regardless
	// of whether a Grok CLI is installed on the host.
	home.DirOverride = t.TempDir()
	t.Cleanup(func() { home.DirOverride = "" })

	c := &ProviderConfig{
		ID:           "xai",
		BaseURL:      "https://api.x.ai/v1",
		ExtraHeaders: map[string]string{},
	}

	c.SetupGrokSubscription()

	// Subscription traffic must route to the chat proxy, not the
	// API-spend-metered api.x.ai surface.
	require.Equal(t, "https://cli-chat-proxy.grok.com/v1", c.BaseURL)
	// The entitlement header is load-bearing for the proxy.
	require.Equal(t, "xai-grok-cli", c.ExtraHeaders["x-xai-token-auth"])
	require.Equal(t, "phosphor", c.ExtraHeaders["x-grok-client-identifier"])
	// The client version must satisfy the proxy's version gate even with
	// no Grok CLI installed.
	require.Equal(t, defaultGrokClientVersion, c.ExtraHeaders["x-grok-client-version"])
}

func TestSetupGrokSubscriptionNilHeaders(t *testing.T) {
	c := &ProviderConfig{ID: "xai"}

	c.SetupGrokSubscription()

	require.NotNil(t, c.ExtraHeaders)
	require.Equal(t, "xai-grok-cli", c.ExtraHeaders["x-xai-token-auth"])
}

func TestGrokClientVersionFallsBack(t *testing.T) {
	// Point home at an empty dir so no version.json is found.
	home.DirOverride = t.TempDir()
	t.Cleanup(func() { home.DirOverride = "" })

	require.Equal(t, defaultGrokClientVersion, grokClientVersion())
}

func TestGrokClientVersionIgnoresOutdatedCLI(t *testing.T) {
	// An installed CLI below the proxy's floor must not be reported, or the
	// proxy returns HTTP 426; we fall back to the headroom default instead.
	home.DirOverride = t.TempDir()
	t.Cleanup(func() { home.DirOverride = "" })

	grokDir := filepath.Join(home.DirOverride, ".grok")
	require.NoError(t, os.MkdirAll(grokDir, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(grokDir, "version.json"),
		[]byte(`{"version":"1.0.4"}`),
		0o644,
	))

	require.Equal(t, defaultGrokClientVersion, grokClientVersion())
}

func TestGrokClientVersionRejectsPhosphorStyleVersion(t *testing.T) {
	// A garbage or Phosphor-style pseudo-version must never be reported.
	home.DirOverride = t.TempDir()
	t.Cleanup(func() { home.DirOverride = "" })

	grokDir := filepath.Join(home.DirOverride, ".grok")
	require.NoError(t, os.MkdirAll(grokDir, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(grokDir, "version.json"),
		[]byte(`{"version":"v0.8.5-0.20261124214254-fa1c21899cd5+dirty"}`),
		0o644,
	))

	require.Equal(t, defaultGrokClientVersion, grokClientVersion())
}

func TestGrokClientVersionReadsInstalledCLI(t *testing.T) {
	home.DirOverride = t.TempDir()
	t.Cleanup(func() { home.DirOverride = "" })

	grokDir := filepath.Join(home.DirOverride, ".grok")
	require.NoError(t, os.MkdirAll(grokDir, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(grokDir, "version.json"),
		[]byte(`{"version":"1.0.20"}`),
		0o644,
	))

	require.Equal(t, "1.0.20", grokClientVersion())
}
