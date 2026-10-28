//go:build windows

package sandbox

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows"
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

// TestNetworkCapabilitiesForDefaultAllow pins the network posture:
// the default spec (NetworkAllow) and the tool-layer egress tiers get
// the internetClient capability, matching the tool layer's default
// allow; only NetworkDeny stays kernel-denied.
func TestNetworkCapabilitiesForDefaultAllow(t *testing.T) {
	t.Parallel()

	for _, mode := range []NetworkMode{NetworkAllow, NetworkProxyOnly} {
		caps, free, err := networkCapabilitiesFor(Spec{Network: mode})
		require.NoError(t, err)
		require.Len(t, caps, 1, "mode %d must grant internetClient", mode)
		require.Equal(t, "S-1-15-3-1", caps[0].Sid.String())
		free()
	}
	caps, free, err := networkCapabilitiesFor(Spec{Network: NetworkDeny})
	require.NoError(t, err)
	require.Empty(t, caps, "NetworkDeny must grant no capability")
	free()
}

// TestSpawnConfinedChildRoutesByTier pins the routing contract: on
// AppContainer hosts the confinement travels through the returned
// ConfinedChild (kernel-launched, workspace writable); on
// restricted-token hosts SpawnConfinedChild hands the exec.Cmd path
// back untouched.
func TestSpawnConfinedChildRoutesByTier(t *testing.T) {
	requireAppContainer(t)
	ws := t.TempDir()

	cmd := exec.Command(cmdExe(t), "/c", "echo x > marker.txt")
	cmd.Dir = ws
	child, err := SpawnConfinedChild(cmd, "workspace", Spec{WorkspaceRoot: ws})
	require.NoError(t, err)
	require.NotNil(t, child, "AppContainer tier must kernel-launch the child")

	code, err := child.Wait()
	require.NoError(t, err)
	require.Equal(t, 0, code)
	_, err = os.Stat(filepath.Join(ws, "marker.txt"))
	require.NoError(t, err, "kernel-launched child must keep the workspace writable")
}

// TestSpawnConfinedChildSkipsMissingCacheRoots pins the graceful
// degradation for lazily-created toolchain caches: a ReadWriteRoots
// entry that does not exist yet must not veto the kernel boundary for
// the whole launch.
func TestSpawnConfinedChildSkipsMissingCacheRoots(t *testing.T) {
	requireAppContainer(t)
	ws := t.TempDir()
	missing := filepath.Join(t.TempDir(), "not-created-yet")

	cmd := exec.Command(cmdExe(t), "/c", "echo x > marker.txt")
	cmd.Dir = ws
	spec := Spec{WorkspaceRoot: ws, ReadWriteRoots: []string{missing}}
	child, err := SpawnConfinedChild(cmd, "workspace", spec)
	require.NoError(t, err, "missing optional root must not drop the launch to v1")
	require.NotNil(t, child)

	code, err := child.Wait()
	require.NoError(t, err)
	require.Equal(t, 0, code)
	_, err = os.Stat(filepath.Join(ws, "marker.txt"))
	require.NoError(t, err)
}

// TestQuoteCmdlineEscaping pins the CRT argument-escaping rules:
// backslashes before quotes are doubled, embedded quotes are
// backslash-escaped, and trailing backslashes cannot escape the
// closing quote.
func TestQuoteCmdlineEscaping(t *testing.T) {
	for _, tc := range []struct {
		arg, want string
	}{
		{`C:\Program Files\dir\`, `"C:\Program Files\dir\\"`},
		{`echo \"hi\"`, `"echo \\\"hi\\\""`},
		{`plain`, `plain`},
		{`with space`, `"with space"`},
		{``, `""`},
	} {
		require.Equal(t, tc.want, quoteCmdline([]string{tc.arg}))
	}
}

// TestGrantLaunchBinarySkipsRelativeAndSystem guards against DACL
// writes on the wrong directory: a bare or relative name resolves
// against the parent's cwd, and %SystemRoot% trees are off-limits
// (stock ACEs already cover them; writes can block on Defender tamper
// protection).
func TestGrantLaunchBinarySkipsRelativeAndSystem(t *testing.T) {
	require.False(t, grantsOwnDirectory("git"), "bare name must not grant")
	require.False(t, grantsOwnDirectory(`..\tool.exe`), "relative path must not grant")

	sysRoot := os.Getenv("SystemRoot")
	require.NotEmpty(t, sysRoot)
	require.False(t, grantsOwnDirectory(filepath.Join(sysRoot, "System32", "cmd.exe")),
		"system directory must not grant")

	outside := filepath.Join(t.TempDir(), "tool.exe")
	require.True(t, grantsOwnDirectory(outside), "third-party directory must grant")
}

// TestConfinedProcessKillReleasesJob pins the cancellation contract:
// Kill releases the containment job (kill-on-close terminates
// descendants) even when Wait is blocked on stdio copiers, and release
// is idempotent across the two paths. The kernel check is observable:
// with the job released, the ping descendant is gone; without it, the
// child survives Kill until Wait drains.
func TestConfinedProcessKillReleasesJob(t *testing.T) {
	requireAppContainer(t)
	ws := t.TempDir()
	spec := Spec{WorkspaceRoot: ws}

	// ping -t runs until killed; its copiers stay blocked until the
	// child's handles close.
	proc, err := SpawnConfined(
		[]string{"ping.exe", "-t", "127.0.0.1"},
		os.Environ(), ws, spec,
		nil, io.Discard, io.Discard,
	)
	require.NoError(t, err)

	require.NoError(t, proc.Kill())
	// Kill released the job; closing it terminated the child and its
	// descendants via kill-on-close. Wait must observe the termination
	// (the copiers drain once the child's handles close) within a
	// bounded window.
	code, err := proc.Wait()
	t.Logf("killed child exited %d (%v)", code, err)
	if err == nil {
		t.Fatal("killed child reported success; termination was not observed")
	}
	var errno syscall.Errno
	if errors.As(err, &errno) && errno == windows.ERROR_ACCESS_DENIED {
		t.Fatal("confined child outlived the job handle; kill-on-close did not fire")
	}
}
