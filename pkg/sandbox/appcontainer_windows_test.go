//go:build windows

package sandbox

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// confineTestHelper skips the kernel validation tests on hosts where
// the AppContainer tier is unavailable (older Windows, locked-down
// policy).
func requireAppContainer(t *testing.T) {
	t.Helper()
	if selectedTier() != tierAppContainer {
		t.Skip("AppContainer tier unavailable on this host")
	}
}

func cmdExe(t *testing.T) string {
	t.Helper()
	path, err := exec.LookPath("cmd.exe")
	require.NoError(t, err)
	return path
}

func TestAppContainerAllowsWorkspaceWrite(t *testing.T) {
	requireAppContainer(t)
	ws := t.TempDir()
	spec := Spec{WorkspaceRoot: ws}

	var out, errOut bytes.Buffer
	proc, err := SpawnConfined(
		[]string{cmdExe(t), "/c", "echo x > marker.txt"},
		os.Environ(), ws, spec,
		nil, &out, &errOut,
	)
	require.NoError(t, err)
	code, err := proc.Wait()
	t.Logf("child output: %s / %s (exit %d)", out.String(), errOut.String(), code)
	require.NoError(t, err)
	require.Equal(t, 0, code)

	_, err = os.Stat(filepath.Join(ws, "marker.txt"))
	require.NoError(t, err, "workspace write must succeed inside the container")
}

func TestAppContainerDeniesWriteOutsideWorkspace(t *testing.T) {
	requireAppContainer(t)
	ws := t.TempDir()
	outside := t.TempDir()
	spec := Spec{WorkspaceRoot: ws}

	var out, errOut bytes.Buffer
	proc, err := SpawnConfined(
		[]string{cmdExe(t), "/c", "echo x > " + filepath.Join(outside, "escape.txt")},
		os.Environ(), ws, spec,
		nil, &out, &errOut,
	)
	require.NoError(t, err)
	code, _ := proc.Wait()
	t.Logf("child output: %s / %s (exit %d)", out.String(), errOut.String(), code)
	require.NotEqual(t, 0, code, "write outside the workspace must be denied")

	_, err = os.Stat(filepath.Join(outside, "escape.txt"))
	require.Error(t, err, "escape file must not exist")
}

func TestAppContainerDeniesReadOutsideWorkspace(t *testing.T) {
	requireAppContainer(t)
	ws := t.TempDir()
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.txt")
	require.NoError(t, os.WriteFile(secret, []byte("hunter2"), 0o600))
	spec := Spec{WorkspaceRoot: ws}

	var out, errOut bytes.Buffer
	proc, err := SpawnConfined(
		[]string{cmdExe(t), "/c", "type " + secret},
		os.Environ(), ws, spec,
		nil, &out, &errOut,
	)
	require.NoError(t, err)
	code, _ := proc.Wait()
	t.Logf("child output: %s / %s (exit %d)", out.String(), errOut.String(), code)
	require.NotEqual(t, 0, code, "read outside the workspace must be denied")
	require.NotContains(t, out.String(), "hunter2")
}

func TestAppContainerWorkspaceReadBack(t *testing.T) {
	requireAppContainer(t)
	ws := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(ws, "read.txt"), []byte("ok"), 0o600))
	spec := Spec{WorkspaceRoot: ws}

	var out, errOut bytes.Buffer
	proc, err := SpawnConfined(
		[]string{cmdExe(t), "/c", "type read.txt"},
		os.Environ(), ws, spec,
		nil, &out, &errOut,
	)
	require.NoError(t, err)
	code, err := proc.Wait()
	require.NoError(t, err)
	require.Equal(t, 0, code)
	require.Contains(t, out.String(), "ok")
}
