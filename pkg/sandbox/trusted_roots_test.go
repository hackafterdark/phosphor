package sandbox

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRootTooBroad_FilesystemAndDriveRoots(t *testing.T) {
	t.Parallel()
	homeErr := errors.New("no home")
	for _, root := range []string{"/", `C:\`, `D:\`, `F:\`, `C:/`, `\\server\share`} {
		broad, reason := RootTooBroadFor(root, "", homeErr, "", "")
		require.True(t, broad, "%s must be refused", root)
		require.Contains(t, reason, "filesystem or drive root")
	}
	// Windows extended-length spellings match their normal forms.
	for _, root := range []string{`\\?\C:\`, `\\?\C:\Windows`, `\\?\UNC\server\share`} {
		broad, reason := RootTooBroadFor(root, "", homeErr, "", "")
		require.True(t, broad, "%s must be refused", root)
		if root == `\\?\C:\Windows` {
			require.Contains(t, reason, "system directory")
		} else {
			require.Contains(t, reason, "filesystem or drive root")
		}
	}
}

func TestRootTooBroad_SensitiveSystemDirectories(t *testing.T) {
	t.Parallel()
	homeErr := errors.New("no home")
	// Each platform's deny-list covers its own layouts; the other
	// platform's spellings are inert on this host.
	dirs := []string{"/etc", "/tmp", "/var", "/root", "/proc", "/dev"}
	if runtime.GOOS == "windows" {
		dirs = []string{`C:\Windows`, `C:\Program Files`, `C:\Program Files (x86)`, `C:\ProgramData`, `C:\Users`}
	}
	for _, dir := range dirs {
		broad, reason := RootTooBroadFor(dir, "", homeErr, "", "")
		require.True(t, broad, "%s must be refused", dir)
		require.Contains(t, reason, "system directory")
	}
	// The host's own system directories, resolved through their env vars,
	// are refused too, whatever volume they live on.
	if windir := os.Getenv("WINDIR"); windir != "" {
		broad, reason := RootTooBroadFor(windir, "", homeErr, "", "")
		require.True(t, broad)
		require.Contains(t, reason, "system directory")
	}
}

func TestRootTooBroad_SensitiveSubdirectoriesAreAllowed(t *testing.T) {
	t.Parallel()
	homeErr := errors.New("no home")
	for _, dir := range []string{"/etc/opt/tool", `C:\Tools\llvm\bin`, "/var/lib/build-cache"} {
		broad, _ := RootTooBroadFor(dir, "", homeErr, "", "")
		require.False(t, broad, "%s must stay listable", dir)
	}
}

func TestRootTooBroad_HomeAndTemp(t *testing.T) {
	t.Parallel()
	home := filepath.Join(string(filepath.Separator), "home", "dev")
	temp := filepath.Join(home, ".cache", "tmp")

	broad, reason := RootTooBroadFor(home, home, nil, temp, "")
	require.True(t, broad)
	require.Contains(t, reason, "home directory")

	broad, reason = RootTooBroadFor(temp, home, nil, temp, "")
	require.True(t, broad)
	require.Contains(t, reason, "temporary directory")

	// The home directory itself stays listable-adjacent: its non-credential
	// sub-directories are legitimate extra roots.
	broad, _ = RootTooBroadFor(filepath.Join(home, "src"), home, nil, temp, "")
	require.False(t, broad)
}

func TestRootTooBroad_WorkspaceAncestor(t *testing.T) {
	t.Parallel()
	homeErr := errors.New("no home")
	ws := filepath.Join(string(filepath.Separator), "home", "dev", "proj")

	broad, reason := RootTooBroadFor(filepath.Dir(ws), "", homeErr, "", ws)
	require.True(t, broad)
	require.Contains(t, reason, "ancestor of the workspace")

	broad, reason = RootTooBroadFor(ws, "", homeErr, "", ws)
	require.True(t, broad)
	require.Contains(t, reason, "workspace itself")

	// A sibling directory is a legitimate extra root.
	broad, _ = RootTooBroadFor(filepath.Join(filepath.Dir(ws), "other"), "", homeErr, "", ws)
	require.False(t, broad)
}

func TestRootTooBroad_CredentialDenyListOverlap(t *testing.T) {
	home, err := os.UserHomeDir()
	require.NoError(t, err)
	home = filepath.Clean(home)

	// Containing a deny path (Landlock-unenforceable overlap): the parent
	// of a home-relative deny root is the home directory itself, which the
	// home rule refuses first; the deny-list parents that are not the home
	// directory itself (e.g. ~/.config on POSIX) are exercised here.
	overlapped := false
	for _, deny := range CredentialDenyPaths() {
		parent := filepath.Dir(filepath.Clean(deny))
		if samePath(parent, home) {
			continue
		}
		broad, reason := RootTooBroadFor(parent, home, nil, "", "")
		require.True(t, broad, "%s contains deny path %s", parent, deny)
		require.Contains(t, reason, "contains credential deny-list path")
		overlapped = true
	}
	if !overlapped {
		t.Skip("this host's deny-list parents are all the home directory itself")
	}

	// Sitting inside a deny path (unreadable in the sandbox anyway).
	ssh := filepath.Join(home, ".ssh")
	broad, reason := RootTooBroadFor(filepath.Join(ssh, "keys"), home, nil, "", "")
	require.True(t, broad)
	require.Contains(t, reason, "inside credential deny-list path")
}
