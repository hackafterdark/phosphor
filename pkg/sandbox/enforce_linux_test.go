//go:build linux

package sandbox

import (
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

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
		switch {
		case f.Code&unix.BPF_LD != 0:
			if f.K == 4 {
				load = arch
			} else {
				load = nr
			}
		case f.Code&unix.BPF_JMP != 0 && f.Code&unix.BPF_JEQ != 0:
			if load == f.K {
				next = ip + 1 + int(f.Jt)
			} else {
				next = ip + 1 + int(f.Jf)
			}
		case f.Code&unix.BPF_JMP != 0 && f.Code&unix.BPF_JSET != 0:
			if load&f.K != 0 {
				next = ip + 1 + int(f.Jt)
			} else {
				next = ip + 1 + int(f.Jf)
			}
		case f.Code&unix.BPF_RET != 0:
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
