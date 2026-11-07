//go:build linux

package sandbox

import (
	"os"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

// bpfClass extracts the 3-bit instruction class from a cBPF opcode. The
// x/sys/unix class constants (BPF_LD=0x0, BPF_JMP=0x5, BPF_RET=0x6) are
// class *values*, not bit masks — comparing with a raw bitwise AND
// against BPF_LD (0x0) always yields 0, so decode the class field first.
func bpfClass(code uint16) uint16 {
	return code & 0x07
}

// evalSeccompProg runs the cBPF interpreter semantics by hand for the
// two loads the program performs (arch at offset 4, syscall nr at
// offset 0). It returns the RET value the kernel would produce.
func evalSeccompProg(t *testing.T, prog []unix.SockFilter, arch, nr uint32) uint32 {
	t.Helper()
	ip := 0
	load := uint32(arch)
	for ip < len(prog) {
		f := prog[ip]
		next := ip + 1
		switch bpfClass(f.Code) {
		case unix.BPF_LD:
			if f.K == 4 {
				load = arch
			} else {
				load = nr
			}
		case unix.BPF_JMP:
			switch f.Code {
			case unix.BPF_JMP | unix.BPF_JEQ | unix.BPF_K:
				if load == f.K {
					next = ip + 1 + int(f.Jt)
				} else {
					next = ip + 1 + int(f.Jf)
				}
			case unix.BPF_JMP | unix.BPF_JSET | unix.BPF_K:
				if load&f.K != 0 {
					next = ip + 1 + int(f.Jt)
				} else {
					next = ip + 1 + int(f.Jf)
				}
			default:
				t.Fatalf("unexpected jump at %d: %+v", ip, f)
			}
		case unix.BPF_RET:
			return f.K
		default:
			t.Fatalf("unexpected instruction at %d: %+v", ip, f)
		}
		ip = next
	}
	t.Fatal("program ran off the end without a RET")
	return 0
}

func TestSeccompProgramDeniesListedSyscalls(t *testing.T) {
	t.Parallel()
	prog := seccompProgram()

	for _, nr := range deniedSyscalls {
		got := evalSeccompProg(t, prog, unix.AUDIT_ARCH_X86_64, nr)
		require.Equalf(t, uint32(unix.SECCOMP_RET_ERRNO|uint32(unix.EPERM)), got,
			"denied syscall %d must reach RET ERRNO, not ALLOW", nr)
	}

	// A benign syscall must still fall through to RET ALLOW.
	got := evalSeccompProg(t, prog, unix.AUDIT_ARCH_X86_64, unix.SYS_READ)
	require.Equal(t, uint32(unix.SECCOMP_RET_ALLOW), got)
}

func TestSeccompProgramRejectsX32Abi(t *testing.T) {
	t.Parallel()
	prog := seccompProgram()

	// An x32-ABI number (nr | 0x40000000) carries the same arch tag but
	// a different number space; it must be refused rather than allowed.
	got := evalSeccompProg(t, prog, unix.AUDIT_ARCH_X86_64, uint32(unix.SYS_READ)|x32SyscallBit)
	require.Equal(t, uint32(unix.SECCOMP_RET_ERRNO|uint32(unix.EPERM)), got)
}

func TestSeccompProgramKillsForeignArch(t *testing.T) {
	t.Parallel()
	prog := seccompProgram()

	got := evalSeccompProg(t, prog, unix.AUDIT_ARCH_I386, unix.SYS_READ)
	require.Equal(t, uint32(unix.SECCOMP_RET_KILL_PROCESS), got)
}

// confineShStrict applies the full kernel policy to a shell command and
// returns its combined output and exit code.
func confineShStrict(t *testing.T, workspace, script string) (int, string) {
	t.Helper()
	cmd := exec.Command("/bin/sh", "-c", script)
	cmd.Dir = workspace
	cmd.Env = []string{"PATH=/usr/bin:/bin"}
	degraded, err := Confine(cmd, "strict", SpecFor(workspace, nil))
	require.NoError(t, err)
	require.False(t, degraded)
	out, _ := cmd.CombinedOutput()
	return cmd.ProcessState.ExitCode(), string(out)
}

// TestKernelDeniesSensitiveEtcFiles pins the narrowing of /etc: the
// account databases and other world-readable sensitive files must not be
// readable from confined children, even when code is invoked through a
// shell script body (the shebang escape the argv policy cannot see).
func TestKernelDeniesSensitiveEtcFiles(t *testing.T) {
	if ok, _ := (landlockIsolator{}).Available(); !ok {
		t.Skip("landlock unavailable on this kernel")
	}
	workspace := t.TempDir()

	for _, path := range []string{"/etc/passwd", "/etc/shadow", "/etc/group"} {
		code, out := confineShStrict(t, workspace, "cat "+path)
		require.NotEqual(t, 0, code, "%s must not be readable: %s", path, out)
		require.Contains(t, out, "Permission denied", "%s denial must be EACCES", path)
	}
}

// TestKernelAllowsToolchainEtcFiles pins the counterpart: the
// toolchain-critical /etc files stay readable under the same policy, so
// the denial above is scoping, not a broken ruleset.
func TestKernelAllowsToolchainEtcFiles(t *testing.T) {
	if ok, _ := (landlockIsolator{}).Available(); !ok {
		t.Skip("landlock unavailable on this kernel")
	}
	workspace := t.TempDir()

	code, out := confineShStrict(t, workspace, "cat /etc/hosts >/dev/null && echo hosts_ok")
	require.Equal(t, 0, code, "/etc/hosts must stay readable: %s", out)

	// DNS resolution exercises resolv.conf, nsswitch.conf, hosts, and
	// gai.conf through the narrowed file rules.
	code, out = confineShStrict(t, workspace, "getent hosts localhost && echo dns_ok")
	require.Equal(t, 0, code, "name resolution must work under narrowed /etc rules: %s", out)
}

// TestKernelDeniesRuntimeSecrets pins the /run denial: the whole runtime
// dir is unreadable, which covers both /run/secrets (container canonical)
// and the rootless docker/podman sockets under /run/user/<uid> — a socket
// connect there is a host escape and unix sockets are not landlock-gated.
func TestKernelDeniesRuntimeSecrets(t *testing.T) {
	if ok, _ := (landlockIsolator{}).Available(); !ok {
		t.Skip("landlock unavailable on this kernel")
	}
	workspace := t.TempDir()

	code, out := confineShStrict(t, workspace, "ls /run >/dev/null")
	require.NotEqual(t, 0, code, "/run itself must not be listable: %s", out)
	require.Contains(t, out, "Permission denied", "/run denial must be EACCES")

	if _, err := os.Stat("/run/user"); err == nil {
		code, out = confineShStrict(t, workspace, "ls /run/user >/dev/null")
		require.NotEqual(t, 0, code, "/run/user runtime sockets must not be reachable: %s", out)
	}
}
