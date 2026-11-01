//go:build windows

package sandbox

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

// findGit resolves git.exe for the confined-git workflow test and
// skips the test on hosts without git.
func findGit(t *testing.T) string {
	t.Helper()
	path, err := exec.LookPath("git.exe")
	if err != nil {
		path, err = exec.LookPath("git")
		if err != nil {
			t.Skip("git unavailable on this host")
		}
	}
	return path
}

// TestAppContainerConfinedGitWorkflow pins that a confined child can
// run real git plumbing: git init/add/commit need to create, rename,
// and unlink files under .git (config.lock, index.lock, tmp_obj_*).
// The .git write fence is a guard-layer policy for shell redirects —
// git commands themselves must keep working inside the container.
func TestAppContainerConfinedGitWorkflow(t *testing.T) {
	requireAppContainer(t)
	// The workspace must sit outside the container's own profile:
	// granting ACEs inside AC/Temp is unreliable, and the profile
	// itself is not a representative host directory.
	repo, rerr := os.MkdirTemp(".", "acgit-")
	require.NoError(t, rerr)
	t.Cleanup(func() { os.RemoveAll(repo) })
	ws, werr := filepath.Abs(repo)
	require.NoError(t, werr)
	// Confine under the *session* container identity, mirroring
	// production: one container per Phosphor session, with the
	// scratch repo granted through the same path grantAccess uses.
	sessionWS, serr := filepath.Abs(".")
	require.NoError(t, serr)
	sid, serr := containerSidFor(sessionWS)
	if serr != nil {
		t.Skipf("host cannot register the sandbox container profile: %v", serr)
	}
	if gerr := grantAccess(ws, sid, accessFor(false)); gerr != nil {
		t.Skipf("host cannot grant ACEs on scratch workspaces: %v", gerr)
	}
	spec := Spec{WorkspaceRoot: ws}
	git := findGit(t)

	env := append(os.Environ(),
		"GIT_AUTHOR_NAME=sbx", "GIT_AUTHOR_EMAIL=sbx@local",
		"GIT_COMMITTER_NAME=sbx", "GIT_COMMITTER_EMAIL=sbx@local",
		"GIT_CONFIG_NOSYSTEM=1", "HOME="+ws,
	)

	run := func(args ...string) (int, string, string) {
		var out, errOut bytes.Buffer
		proc, err := SpawnConfined(
			append([]string{git}, args...), env, ws, spec,
			nil, &out, &errOut,
		)
		if err != nil {
			// Hosts whose own sandbox cannot attach a nested
			// boundary cannot host this test; report honestly.
			return 126, "", err.Error()
		}
		code, err := proc.Wait()
		if err != nil && code == 0 {
			code = 1
		}
		return code, out.String(), errOut.String()
	}

	code, _, errOut := run("init", "-q", ".")
	if code == 126 {
		t.Skipf("host cannot spawn confined children into a granted scratch workspace: %s", errOut)
	}
	if code != 0 && strings.Contains(errOut, "/dev/null") {
		// Hardened hosts (GitHub runners) deny the container access
		// to the device namespace, and \dev\null cannot be ACE-granted
		// like a filesystem path. The confinement boundary itself is
		// fine; this host simply cannot host real git inside the
		// container, so report honestly instead of failing the suite.
		t.Skipf("host denies the container the NUL device needed by git: %s", errOut)
	}
	if code != 0 {
		t.Fatalf("confined git init failed in %s (%d): %s", ws, code, errOut)
	}
	require.NoError(t, os.WriteFile(filepath.Join(ws, "f.txt"), []byte("x\n"), 0o644))
	if code, _, errOut := run("add", "f.txt"); code != 0 {
		t.Fatalf("confined git add failed (%d): %s", code, errOut)
	}
	if code, _, errOut := run("commit", "-q", "-m", "t"); code != 0 {
		t.Fatalf("confined git commit failed (%d): %s", code, errOut)
	}
	// The workflow leaves no git lock behind.
	_, err := os.Stat(filepath.Join(ws, ".git", "index.lock"))
	require.True(t, os.IsNotExist(err), "index.lock must be removed by git itself")
}
