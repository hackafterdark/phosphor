//go:build darwin

package sandbox

import (
	"os/exec"
	"slices"
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

func TestParseRLimitArgs(t *testing.T) {
	t.Parallel()

	rl, payload, err := parseRLimitArgs([]string{
		"--rlimit", "nproc=128",
		"--rlimit", "fsize-mb=512",
		"--", "/bin/true", "--flag",
	})
	require.NoError(t, err)
	require.Equal(t, uint64(128), rl.nproc)
	require.Equal(t, uint64(512), rl.fsizeMB)
	require.Equal(t, []string{"/bin/true", "--flag"}, payload)

	_, _, err = parseRLimitArgs([]string{"--rlimit", "nproc=0", "--", "true"})
	require.Error(t, err, "zero is not a meaningful rlimit")

	_, _, err = parseRLimitArgs([]string{"--rlimit", "bogus=1", "--", "true"})
	require.Error(t, err, "unknown resource must fail closed")

	_, _, err = parseRLimitArgs([]string{"--rlimit", "nproc=1"})
	require.Error(t, err, "missing -- separator must fail closed")
}

func TestSeatbeltConfineAppliesRLimitWrapper(t *testing.T) {
	t.Parallel()

	if ok, _ := (seatbeltIsolator{}).Available(); !ok {
		t.Skip("sandbox-exec unavailable on this host")
	}

	cmd := exec.Command("true", "--flag")
	cmd.Path = "/bin/true"
	spec := Spec{
		WorkspaceRoot:  "/ws",
		ReadWriteFiles: []string{"/dev/null"},
		Limits:         ResourceLimits{MaxProcs: 128, MaxFileMB: 512},
	}
	require.NoError(t, (seatbeltIsolator{}).Confine(cmd, spec))

	// The Seatbelt-wrapped argv is itself wrapped through the hidden
	// --rlimit-exec mode; the payload (sandbox-exec + profile + cmd)
	// rides behind the -- separator unchanged. The payload executable
	// must be the resolved path — execve does not search PATH.
	require.Equal(t, "--rlimit-exec", cmd.Args[1])
	require.Contains(t, cmd.Args, "--rlimit")
	require.Contains(t, cmd.Args, "nproc=128")
	require.Contains(t, cmd.Args, "fsize-mb=512")
	sep := slices.Index(cmd.Args, "--")
	require.Greater(t, sep, 0)
	require.Equal(t, seatbeltExecPath, cmd.Args[sep+1])
	require.Equal(t, []string{"/bin/true", "--flag"}, cmd.Args[sep+4:])
}

func TestSeatbeltConfineRequiresWorkspace(t *testing.T) {
	t.Parallel()

	if ok, _ := (seatbeltIsolator{}).Available(); !ok {
		t.Skip("sandbox-exec unavailable on this host")
	}

	cmd := exec.Command("true")
	require.Error(t, (seatbeltIsolator{}).Confine(cmd, Spec{}))
}
