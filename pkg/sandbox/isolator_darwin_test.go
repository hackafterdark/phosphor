//go:build darwin

package sandbox

import (
	"os/exec"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSeatbeltConfineWrapsArgv(t *testing.T) {
	t.Parallel()

	if ok, _ := (seatbeltIsolator{}).Available(); !ok {
		t.Skip("sandbox-exec unavailable on this host")
	}

	cmd := exec.Command("true", "--flag")
	cmd.Path = "/bin/true"
	spec := Spec{
		WorkspaceRoot:  "/ws",
		ReadWriteFiles: []string{"/dev/null"},
	}
	require.NoError(t, (seatbeltIsolator{}).Confine(cmd, spec))

	require.Equal(t, seatbeltExecPath, cmd.Path)
	require.Equal(t, "sandbox-exec", cmd.Args[0])
	require.Equal(t, "-p", cmd.Args[1])

	// The profile carries the workspace rule and the deny preamble.
	require.Contains(t, cmd.Args[2], "(deny default)")
	require.Contains(t, cmd.Args[2], `(subpath "/ws")`)

	// The wrapped command keeps its resolved path and argv tail.
	require.Equal(t, "/bin/true", cmd.Args[3])
	require.Equal(t, []string{"--flag"}, cmd.Args[4:])
}

func TestSeatbeltConfineRequiresWorkspace(t *testing.T) {
	t.Parallel()

	if ok, _ := (seatbeltIsolator{}).Available(); !ok {
		t.Skip("sandbox-exec unavailable on this host")
	}

	cmd := exec.Command("true")
	require.Error(t, (seatbeltIsolator{}).Confine(cmd, Spec{}))
}
