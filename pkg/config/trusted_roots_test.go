package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateTrustedExtraRoots(t *testing.T) {
	wsRoot := t.TempDir()
	workspace := filepath.Join(wsRoot, "proj")
	require.NoError(t, os.MkdirAll(workspace, 0o755))
	legit := filepath.Join(wsRoot, "shared-build-cache")

	c := &Config{}
	c.Tools.Bash.TrustedExtraRoots = []string{legit}
	require.NoError(t, c.ValidateTrustedExtraRoots(workspace))

	for _, bad := range []string{"/", `C:\`, workspace, wsRoot, "", ".config", "relative/dir"} {
		c.Tools.Bash.TrustedExtraRoots = []string{bad}
		err := c.ValidateTrustedExtraRoots(workspace)
		require.Error(t, err, "refused root %q must fail validation", bad)
	}
}

func TestValidateTrustedExtraRoots_SymlinkLaundering(t *testing.T) {
	wsRoot := t.TempDir()
	workspace := filepath.Join(wsRoot, "proj")
	require.NoError(t, os.MkdirAll(workspace, 0o755))

	link := filepath.Join(wsRoot, "innocent")
	if err := os.Symlink(wsRoot, link); err != nil {
		t.Skipf("symlinks unavailable on this host: %v", err)
	}

	c := &Config{}
	c.Tools.Bash.TrustedExtraRoots = []string{link}
	err := c.ValidateTrustedExtraRoots(workspace)
	require.Error(t, err)
	require.Contains(t, err.Error(), "resolves to")
}
