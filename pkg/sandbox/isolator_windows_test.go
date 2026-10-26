//go:build windows

package sandbox

import (
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestWindowsConfineAppliesRestrictedToken(t *testing.T) {
	t.Parallel()

	cmd := exec.Command("cmd.exe", "/c", "echo hi")
	spec := Spec{WorkspaceRoot: t.TempDir()}
	require.NoError(t, (windowsIsolator{}).Confine(cmd, spec))
	require.NotNil(t, cmd.SysProcAttr)
	require.NotZero(t, uint64(cmd.SysProcAttr.Token),
		"confined child must carry the restricted token")
}

func TestWindowsConfineRequiresWorkspace(t *testing.T) {
	t.Parallel()

	cmd := exec.Command("cmd.exe", "/c", "echo hi")
	require.Error(t, (windowsIsolator{}).Confine(cmd, Spec{}))
}

func TestWindowsAvailableHonestAboutFSBoundary(t *testing.T) {
	t.Parallel()

	ok, detail := (windowsIsolator{}).Available()
	// v1 cannot kernel-enforce the workspace FS boundary on Windows, so
	// workspace mode must degrade with a visible warning and strict mode
	// must refuse exec (invariant 1).
	require.False(t, ok)
	require.Contains(t, detail, "FS boundary")
}

func TestWindowsContainmentKillOnClose(t *testing.T) {
	// Not parallel: the child is a long-running ping.
	cmd := exec.Command("cmd.exe", "/c", "ping -n 30 127.0.0.1 >nul")
	require.NoError(t, cmd.Start())

	release, err := ContainProcess(cmd, true)
	require.NoError(t, err)
	require.NotNil(t, release)

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	release() // closing the job must terminate the child
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		cmd.Process.Kill()
		t.Fatal("child survived job close; kill-on-close is not attached")
	}
}

func TestWindowsConfinedChildWritesWorkspace(t *testing.T) {
	t.Parallel()

	ws := t.TempDir()
	inside := ws + `\inside.txt`
	cmd := exec.Command("cmd.exe", "/c", "echo ok > "+inside)
	require.NoError(t, (windowsIsolator{}).Confine(cmd, Spec{WorkspaceRoot: ws}))
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "restricted-token child must still run: %s", out)
	require.FileExists(t, inside)
}
