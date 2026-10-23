//go:build linux

package sandbox

import (
	"os"
	"os/exec"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSplitPolicyArgs(t *testing.T) {
	t.Parallel()

	execPath, ro, rw, argv, err := splitPolicyArgs([]string{
		"--exec", "/usr/bin/true",
		"--ro", "/usr",
		"--rw", "/ws",
		"--rw", "/dev/null",
		"--", "true", "--flag",
	})
	require.NoError(t, err)
	require.Equal(t, "/usr/bin/true", execPath)
	require.Equal(t, []string{"/usr"}, ro)
	require.Equal(t, []string{"/ws", "/dev/null"}, rw)
	require.Equal(t, []string{"true", "--flag"}, argv)
}

func TestSplitPolicyArgsErrors(t *testing.T) {
	t.Parallel()

	_, _, _, _, err := splitPolicyArgs([]string{"--exec"})
	require.Error(t, err)

	_, _, _, _, err = splitPolicyArgs([]string{"--ro", "/usr"})
	require.Error(t, err)

	_, _, _, _, err = splitPolicyArgs([]string{"--exec", "/bin/true", "--ro", "/usr"})
	require.Error(t, err)

	_, _, _, _, err = splitPolicyArgs([]string{"--exec", "/bin/true", "--bogus", "x", "--"})
	require.Error(t, err)
}

func TestConfineWrapsArgv(t *testing.T) {
	t.Parallel()

	if ok, _ := (landlockIsolator{}).Available(); !ok {
		t.Skip("landlock unavailable on this kernel")
	}

	cmd := exec.Command("true", "--flag")
	cmd.Path = "/usr/bin/true"
	spec := Spec{
		WorkspaceRoot:  "/ws",
		ReadOnlyRoots:  []string{"/usr"},
		ReadWriteFiles: []string{"/dev/null"},
	}
	require.NoError(t, (landlockIsolator{}).Confine(cmd, spec))

	exe, err := os.Executable()
	require.NoError(t, err)

	// argv[0] is the program-name slot and argv[1] dispatches cobra to
	// the hidden sandbox-exec subcommand.
	require.Equal(t, exe, cmd.Args[0])
	require.Equal(t, "sandbox-exec", cmd.Args[1])
	require.Equal(t, "--exec", cmd.Args[2])
	require.Equal(t, "/usr/bin/true", cmd.Args[3])

	// The workspace is always writable inside the sandbox.
	require.Contains(t, cmd.Args, "--rw")
	require.Contains(t, cmd.Args, "/ws")

	sep := slices.Index(cmd.Args, "--")
	require.Greater(t, sep, 0)
	require.Equal(t, []string{"true", "--flag"}, cmd.Args[sep+1:])
}

func TestConfineRequiresWorkspace(t *testing.T) {
	t.Parallel()

	if ok, _ := (landlockIsolator{}).Available(); !ok {
		t.Skip("landlock unavailable on this kernel")
	}

	cmd := exec.Command("true")
	require.Error(t, (landlockIsolator{}).Confine(cmd, Spec{}))
}
