package shell

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

// Audit finding (low, #10): updateShellFromRunner compared the new cwd with a
// raw case-folded filepath.Rel, so a cd through a symlink (or junction) that
// lives inside the workspace but resolves outside it was accepted and the
// shell's cwd stayed on the outside directory. The fix routes the check
// through filepathext.IsInside, which resolves symlinks on both sides.

// makeLinkInsideWorkspace creates a directory link inside workspace that
// resolves to target. On Windows it uses a junction (plain file symlinks
// require elevated privileges); elsewhere it uses os.Symlink.
func makeLinkInsideWorkspace(t *testing.T, workspace, target, name string) string {
	t.Helper()
	link := filepath.Join(workspace, name)
	if runtime.GOOS == "windows" {
		out, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput()
		require.NoError(t, err, "mklink failed: %s", out)
	} else {
		require.NoError(t, os.Symlink(target, link))
	}
	return link
}

func TestUpdateShellFromRunnerResetsCwdThroughSymlinkEscape(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	outside := t.TempDir()
	link := makeLinkInsideWorkspace(t, workspace, outside, "escape")

	s := NewShell(&Options{WorkingDir: workspace, Workspace: workspace})

	// Lexically the link target looks inside the workspace; resolved, it is
	// outside. The shell must reset its cwd back to the workspace root.
	runner, err := s.newInterp(nil, os.Stdout, os.Stderr)
	require.NoError(t, err)
	runner.Dir = link
	s.updateShellFromRunner(runner)

	require.Equal(t, workspace, s.cwd,
		"cwd reached through a symlink to outside must be reset to the workspace")
}

func TestUpdateShellFromRunnerKeepsLegitimateCwd(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	inner := filepath.Join(workspace, "inner")
	require.NoError(t, os.MkdirAll(inner, 0o755))

	s := NewShell(&Options{WorkingDir: workspace, Workspace: workspace})

	runner, err := s.newInterp(nil, os.Stdout, os.Stderr)
	require.NoError(t, err)
	runner.Dir = inner
	s.updateShellFromRunner(runner)

	require.Equal(t, inner, s.cwd, "a legitimate cd inside the workspace must persist")
}