package shell

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsSecretEnvKey(t *testing.T) {
	secret := []string{
		"MYAPP_API_KEY", "stripe_api_key", "GH_TOKEN", "GITHUB_TOKEN",
		"DB_PASSWORD", "AZURE_CLIENT_SECRET", "PRIVATE_KEY", "AWS_SECRET_ACCESS_KEY",
		"SESSION_TOKEN", "REFRESH_TOKEN", "ADMIN_PASSWORD", "SERVICE_ACCOUNT_CREDENTIALS",
	}
	for _, k := range secret {
		require.True(t, isSecretEnvKey(k), "%s must be recognised as a credential name", k)
	}

	benign := []string{
		"PATH", "HOME", "TERM", "EDITOR", "LANG", "NODE_ENV", "LOG_LEVEL",
		"GOFLAGS", "PYTHONUNBUFFERED", "NO_PROXY", "HTTP_PROXY", "TMPDIR", "CI",
	}
	for _, k := range benign {
		require.False(t, isSecretEnvKey(k), "%s must not be treated as a credential name", k)
	}

	require.False(t, isSecretEnvKey(""), "the empty key is never a secret name")
	require.False(t, isSecretEnvKey("   "), "whitespace-only key is never a secret name")
}

// Even when an operator (mistakenly or deliberately) lists a credential-shaped
// variable in the allowlist, filterEnv must still drop it: the dynamic-suffix
// predicate is the defense-in-depth floor under the allowlist.
func TestFilterEnv_DropsSecretNamedKeysEvenIfAllowlisted(t *testing.T) {
	environ := []string{
		"PATH=/usr/bin",
		"MYAPP_TOKEN=super-secret-token",
		"DB_PASSWORD=hunter2",
		"NODE_ENV=production",
	}
	// The allowlist explicitly permits the secret-named keys, which the deny
	// predicate must override.
	allowlist := buildAllowlist([]string{"PATH", "MYAPP_TOKEN", "DB_PASSWORD", "NODE_ENV"})

	got := filterEnv(environ, allowlist)
	joined := joinEnv(got)

	require.Contains(t, joined, "PATH=/usr/bin")
	require.Contains(t, joined, "NODE_ENV=production")
	require.NotContains(t, joined, "super-secret-token")
	require.NotContains(t, joined, "hunter2")
	require.NotContains(t, joined, "MYAPP_TOKEN=")
	require.NotContains(t, joined, "DB_PASSWORD=")
}

func TestFilterEnv_AlwaysStripKeysAreRemovedRegardless(t *testing.T) {
	environ := []string{"GH_TOKEN=ghp_deadbeef", "PATH=/bin", "HOME=/home/u"}
	// Even an explicit allowlist entry does not save an always-strip key.
	allowlist := buildAllowlist([]string{"GH_TOKEN", "PATH", "HOME"})
	joined := joinEnv(filterEnv(environ, allowlist))
	require.NotContains(t, joined, "ghp_deadbeef")
	require.NotContains(t, joined, "GH_TOKEN=")
	require.Contains(t, joined, "PATH=/bin")
}

// joinEnv renders a filtered environment back into a single newline-delimited
// string so assertions can use Contains/NotContains over the whole result.
func joinEnv(env []string) string {
	out := ""
	for _, e := range env {
		out += e + "\n"
	}
	return out
}
