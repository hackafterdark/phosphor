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

	execPath, ro, rw, net, argv, err := splitPolicyArgs([]string{
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
	require.Equal(t, NetworkAllow, net.mode)
	require.Equal(t, []string{"true", "--flag"}, argv)
}

func TestSplitPolicyArgsNetwork(t *testing.T) {
	t.Parallel()

	_, _, _, net, _, err := splitPolicyArgs([]string{
		"--exec", "/usr/bin/true",
		"--net-proxy", "3128",
		"--", "true",
	})
	require.NoError(t, err)
	require.Equal(t, NetworkProxyOnly, net.mode)
	require.Equal(t, uint16(3128), net.port)

	_, _, _, net, _, err = splitPolicyArgs([]string{
		"--exec", "/usr/bin/true",
		"--net-deny",
		"--", "true",
	})
	require.NoError(t, err)
	require.Equal(t, NetworkDeny, net.mode)

	_, _, _, _, _, err = splitPolicyArgs([]string{
		"--exec", "/usr/bin/true",
		"--net-proxy", "99999",
		"--", "true",
	})
	require.Error(t, err)

	_, _, _, _, _, err = splitPolicyArgs([]string{
		"--exec", "/usr/bin/true",
		"--net-proxy",
		"--", "true",
	})
	require.Error(t, err)
}

func TestSplitPolicyArgsErrors(t *testing.T) {
	t.Parallel()

	_, _, _, _, _, err := splitPolicyArgs([]string{"--exec"})
	require.Error(t, err)

	_, _, _, _, _, err = splitPolicyArgs([]string{"--ro", "/usr"})
	require.Error(t, err)

	_, _, _, _, _, err = splitPolicyArgs([]string{"--exec", "/bin/true", "--ro", "/usr"})
	require.Error(t, err)

	_, _, _, _, _, err = splitPolicyArgs([]string{"--exec", "/bin/true", "--bogus", "x", "--"})
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

func TestConfineProxyOnlyArgv(t *testing.T) {
	t.Parallel()

	if ok, _ := (landlockIsolator{}).Available(); !ok {
		t.Skip("landlock unavailable on this kernel")
	}
	if abi, err := landlockABIVersion(); err != nil || abi < 4 {
		t.Skip("proxy-only network tier needs landlock abi v4")
	}

	cmd := exec.Command("true")
	cmd.Path = "/usr/bin/true"
	spec := Spec{
		WorkspaceRoot: "/ws",
		Network:       NetworkProxyOnly,
		ProxyPort:     3128,
	}
	require.NoError(t, (landlockIsolator{}).Confine(cmd, spec))
	require.Contains(t, cmd.Args, "--net-proxy")
	require.Contains(t, cmd.Args, "3128")
}

func TestConfineProxyOnlyRequiresAbiV4(t *testing.T) {
	t.Parallel()

	if ok, _ := (landlockIsolator{}).Available(); !ok {
		t.Skip("landlock unavailable on this kernel")
	}
	abi, err := landlockABIVersion()
	if err == nil && abi >= 4 {
		t.Skip("kernel enforces proxy-only; error path cannot be exercised")
	}

	cmd := exec.Command("true")
	cmd.Path = "/usr/bin/true"
	spec := Spec{
		WorkspaceRoot: "/ws",
		Network:       NetworkProxyOnly,
		ProxyPort:     3128,
	}
	require.Error(t, (landlockIsolator{}).Confine(cmd, spec),
		"proxy-only must fail Confine on kernels without landlock abi v4")
}

func TestConfineProxyOnlyRequiresValidPort(t *testing.T) {
	t.Parallel()

	if ok, _ := (landlockIsolator{}).Available(); !ok {
		t.Skip("landlock unavailable on this kernel")
	}

	cmd := exec.Command("true")
	cmd.Path = "/usr/bin/true"
	spec := Spec{
		WorkspaceRoot: "/ws",
		Network:       NetworkProxyOnly,
		ProxyPort:     0,
	}
	require.Error(t, (landlockIsolator{}).Confine(cmd, spec))
}

func TestConfineRequiresWorkspace(t *testing.T) {
	t.Parallel()

	if ok, _ := (landlockIsolator{}).Available(); !ok {
		t.Skip("landlock unavailable on this kernel")
	}

	cmd := exec.Command("true")
	require.Error(t, (landlockIsolator{}).Confine(cmd, Spec{}))
}
