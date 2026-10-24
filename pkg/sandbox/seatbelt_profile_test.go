package sandbox

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// seatbeltTestSpec returns a representative workspace-mode spec without
// touching the host filesystem.
func seatbeltTestSpec() Spec {
	return Spec{
		WorkspaceRoot:  "/Users/dev/ws",
		ReadOnlyRoots:  []string{"/usr", "/bin"},
		ReadWriteRoots: []string{"/var/folders/xx/T"},
		ReadWriteFiles: []string{"/dev/null"},
	}
}

// writeRules returns only the file-write* rules of a profile.
func writeRules(t *testing.T, profile string) string {
	t.Helper()
	var b strings.Builder
	for _, line := range strings.Split(profile, "\n") {
		if strings.Contains(line, "file-write*") {
			b.WriteString(line)
			b.WriteString("\n")
		}
	}
	return b.String()
}

func TestSeatbeltProfileDenyDefault(t *testing.T) {
	t.Parallel()

	profile := seatbeltProfile(seatbeltTestSpec())
	require.True(t, strings.HasPrefix(profile, "(version 1)\n(deny default)\n"),
		"profile must open with the deny-default preamble")
}

func TestSeatbeltProfileWorkspaceWritable(t *testing.T) {
	t.Parallel()

	profile := seatbeltProfile(seatbeltTestSpec())
	writes := writeRules(t, profile)
	require.Contains(t, writes, `(subpath "/Users/dev/ws")`)
}

func TestSeatbeltProfileCanonicalizesPrivateVariants(t *testing.T) {
	t.Parallel()

	// /var/folders/... resolves through the /var symlink; Seatbelt sees
	// the canonicalized /private path, so both forms must be in the rule.
	profile := seatbeltProfile(seatbeltTestSpec())
	writes := writeRules(t, profile)
	require.Contains(t, writes, `(subpath "/var/folders/xx/T")`)
	require.Contains(t, writes, `(subpath "/private/var/folders/xx/T")`)
}

func TestSeatbeltProfileReadOnlyRootsNotWritable(t *testing.T) {
	t.Parallel()

	profile := seatbeltProfile(seatbeltTestSpec())
	writes := writeRules(t, profile)
	require.NotContains(t, writes, `"/usr"`)
	require.NotContains(t, writes, `"/bin"`)
}

func TestSeatbeltProfileDevNullWritable(t *testing.T) {
	t.Parallel()

	profile := seatbeltProfile(seatbeltTestSpec())
	writes := writeRules(t, profile)
	require.Contains(t, writes, `(literal "/dev/null")`)
}

func TestSeatbeltProfileQuotesPaths(t *testing.T) {
	t.Parallel()

	spec := Spec{WorkspaceRoot: `/Users/dev/my "ws"`}
	profile := seatbeltProfile(spec)
	writes := writeRules(t, profile)
	require.Contains(t, writes, `(subpath "/Users/dev/my \"ws\"")`)
}

func TestSeatbeltProfileBaseCapabilities(t *testing.T) {
	t.Parallel()

	profile := seatbeltProfile(seatbeltTestSpec())
	for _, rule := range []string{
		"(allow process-exec)",
		"(allow process-fork)",
		"(allow signal (target same-sandbox))",
		"(allow file-ioctl (regex #\"^/dev/\"))",
		"(allow sysctl-read)",
		"(allow file-read*)",
	} {
		require.Contains(t, profile, rule)
	}
}
func TestSeatbeltProfileAllowNetworkByDefault(t *testing.T) {
	t.Parallel()

	profile := seatbeltProfile(seatbeltTestSpec())
	require.Contains(t, profile, "(allow network*)")
	require.NotContains(t, profile, "(deny network*)")
}

func TestSeatbeltProfileProxyOnlyNetwork(t *testing.T) {
	t.Parallel()

	spec := seatbeltTestSpec()
	spec.Network = NetworkProxyOnly
	spec.ProxyPort = 3128
	profile := seatbeltProfile(spec)

	// ProxyOnly must deny raw egress and allow only the loopback broker.
	require.Contains(t, profile, "(deny network*)")
	require.Contains(t, profile, `(allow network-outbound (remote ip "127.0.0.1:3128"))`)
	require.NotContains(t, profile, "(allow network*)")
}

func TestSeatbeltProfileProxyOnlyInvalidPortFailsClosed(t *testing.T) {
	t.Parallel()

	spec := seatbeltTestSpec()
	spec.Network = NetworkProxyOnly
	spec.ProxyPort = 0
	profile := seatbeltProfile(spec)

	// A bad port must fall to deny-all, never the raw network surface.
	require.Contains(t, profile, "(deny network*)")
	require.NotContains(t, profile, "(allow network*)")
}

func TestSeatbeltProfileDenyNetwork(t *testing.T) {
	t.Parallel()

	spec := seatbeltTestSpec()
	spec.Network = NetworkDeny
	profile := seatbeltProfile(spec)

	require.Contains(t, profile, "(deny network*)")
	require.NotContains(t, profile, "(allow network*)")
}
