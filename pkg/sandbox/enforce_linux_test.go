//go:build linux

package sandbox

import (
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
