//go:build linux

package sandbox

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"

	"github.com/landlock-lsm/go-landlock/landlock"
	"golang.org/x/sys/unix"
)

// init intercepts the sandbox-exec argv wrap before any other code runs,
// so *any* binary that imports pkg/sandbox (the CLI, a test binary, a
// helper) can serve as the confinement bootstrap. Without this, re-execing
// a binary that lacks the cobra subcommand re-runs the binary with the
// policy argv as ordinary arguments — the test-binary recursion bug that
// hangs the kernel vector tests.
func init() {
	if len(os.Args) > 2 && os.Args[1] == "sandbox-exec" && os.Args[2] == "--exec" {
		if err := RunSandboxExec(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "phosphor sandbox-exec:", err)
			os.Exit(126)
		}
		os.Exit(1) // RunSandboxExec only returns on failure.
	}
}

// deniedSyscalls is the seccomp denylist from _PLANS/NATIVE_SANDBOX.md §3:
// no privilege escalation, tracing, eBPF/keyring, module loading, namespace
// or mount tampering.
var deniedSyscalls = []uint32{
	unix.SYS_PTRACE,
	unix.SYS_PROCESS_VM_READV,
	unix.SYS_PROCESS_VM_WRITEV,
	unix.SYS_BPF,
	unix.SYS_KEYCTL,
	unix.SYS_INIT_MODULE,
	unix.SYS_FINIT_MODULE,
	unix.SYS_DELETE_MODULE,
	unix.SYS_KEXEC_LOAD,
	unix.SYS_UNSHARE,
	unix.SYS_MOUNT,
	unix.SYS_UMOUNT2,
	unix.SYS_PIVOT_ROOT,
	unix.SYS_REBOOT,
	// io_uring lets the kernel perform syscalls on the process's behalf
	// after the ring is set up, bypassing both this filter and Landlock's
	// path mediation for the ring's lifetime. Deny the ring itself.
	unix.SYS_IO_URING_SETUP,
	unix.SYS_IO_URING_ENTER,
	unix.SYS_IO_URING_REGISTER,
}

// x32SyscallBit marks x32-ABI syscall numbers (nr | 0x40000000). They
// report the same AUDIT_ARCH_X86_64 as native 64-bit code but carry
// different syscall numbers, so they would slip past the denylist; no
// legitimate x86_64 exec tree uses them.
const x32SyscallBit = 0x40000000

func rawLandlockVersion(queryFlag uintptr) (uintptr, uintptr, syscall.Errno) {
	return unix.RawSyscall(unix.SYS_LANDLOCK_CREATE_RULESET, 0, 0, queryFlag)
}

func syscallExec(bin string, argv, envv []string) error {
	return syscall.Exec(bin, argv, envv)
}

func killOnParentDeath() syscall.Signal {
	return syscall.SIGKILL
}

// applyRLimits caps the wrapper's (and therefore the payload's) process
// count and file size before any sandbox state is attached, so a
// misconfigured limit cannot leave a half-restricted process running.
// Raising the hard limit requires CAP_SYS_RESOURCE; we lower the soft
// limit to the requested value and keep the hard limit, so the caps can
// never outlive the child.
//
// RLIMIT_NPROC is enforced by the kernel against the real UID's total
// process count, not the confined subtree: the cap reads as a per-user
// ceiling while the wrapper's exec tree is alive. Lowering the soft
// limit below the current hard limit always succeeds; the hard limit is
// left untouched so a request above it degrades to the kernel's own
// ceiling instead of failing the exec with EPERM.
func applyRLimits(net netPolicy) error {
	if net.nproc != 0 {
		var cur unix.Rlimit
		if err := unix.Getrlimit(unix.RLIMIT_NPROC, &cur); err != nil {
			return err
		}
		soft := net.nproc
		if cur.Max != 0 && cur.Max < soft {
			soft = cur.Max
		}
		if err := unix.Setrlimit(unix.RLIMIT_NPROC, &unix.Rlimit{
			Cur: soft, Max: cur.Max,
		}); err != nil {
			return err
		}
	}
	if net.fsizeMB != 0 {
		limit := net.fsizeMB * 1024 * 1024
		var cur unix.Rlimit
		if err := unix.Getrlimit(unix.RLIMIT_FSIZE, &cur); err != nil {
			return err
		}
		soft := limit
		if cur.Max != 0 && cur.Max < soft {
			soft = cur.Max
		}
		if err := unix.Setrlimit(unix.RLIMIT_FSIZE, &unix.Rlimit{
			Cur: soft, Max: cur.Max,
		}); err != nil {
			return err
		}
	}
	return nil
}

// enforceLandlock applies NO_NEW_PRIVS, the seccomp denylist, and Landlock
// path rules to the calling process. Landlock is applied last so a failed
// probe never leaves a half-restricted caller.
func enforceLandlock(ro, rw []string, net netPolicy) error {
	if err := applyRLimits(net); err != nil {
		return err
	}
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		return err
	}
	if err := installSeccompDenylist(); err != nil {
		return err
	}
	abi, err := landlockABIVersion()
	if err != nil || abi == 0 {
		return fmt.Errorf("kernel does not support landlock (err=%v)", err)
	}
	// BestEffort degrades to the kernel"s ABI; the probe above guarantees
	// we are not silently sandboxing nothing.
	rules := []landlock.Rule{
		landlock.RODirs(dirsOf(ro)...),
		landlock.ROFiles(filesOf(ro)...),
		landlock.RWDirs(dirsOf(rw)...),
		landlock.RWFiles(filesOf(rw)...),
	}
	if err := landlock.V5.BestEffort().RestrictPaths(rules...); err != nil {
		return err
	}
	// Network rules are applied strictly (no BestEffort): a kernel that
	// cannot enforce the requested posture must fail the exec rather than
	// run with raw egress open (invariant 6, fail closed).
	switch net.mode {
	case NetworkProxyOnly:
		// connect(2) to the broker port only. The kernel"s implicit
		// bind of the ephemeral source port at connect() time is
		// covered by the port-0 bind rule, mirroring the UDP
		// BindUDP(0) pattern in the go-landlock docs.
		return landlock.V4.RestrictNet(
			landlock.ConnectTCP(net.port),
			landlock.BindTCP(0),
		)
	case NetworkDeny:
		// No TCP rules granted: every bind/connect is denied.
		return landlock.V4.RestrictNet()
	default:
		return nil
	}
}

// dirsOf/filesOf split paths by type; Landlock rules are typed and
// well-known exceptions like /dev/null must be granted as file rules.
func dirsOf(paths []string) []string {
	var out []string
	for _, p := range paths {
		if st, err := os.Stat(p); err == nil && st.IsDir() {
			out = append(out, p)
		}
	}
	return out
}

func filesOf(paths []string) []string {
	var out []string
	for _, p := range paths {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			out = append(out, p)
		}
	}
	return out
}

// seccompProgram builds the cBPF denylist filter: kill on foreign-arch
// execution, EPERM on any denied syscall or x32-ABI number, ALLOW
// otherwise. Split from installSeccompDenylist so the jump arithmetic is
// unit-testable without attaching the filter.
func seccompProgram() []unix.SockFilter {
	prog := []unix.SockFilter{
		{Code: unix.BPF_LD | unix.BPF_W | unix.BPF_ABS, K: 4},
		{Code: unix.BPF_JMP | unix.BPF_JEQ | unix.BPF_K, K: unix.AUDIT_ARCH_X86_64, Jt: 2},
		{Code: unix.BPF_JMP | unix.BPF_JEQ | unix.BPF_K, K: unix.AUDIT_ARCH_AARCH64, Jt: 1},
		{Code: unix.BPF_RET | unix.BPF_K, K: unix.SECCOMP_RET_KILL_PROCESS},
		{Code: unix.BPF_LD | unix.BPF_W | unix.BPF_ABS, K: 0},
	}
	preamble := len(prog)
	for _, nr := range deniedSyscalls {
		prog = append(prog, unix.SockFilter{
			Code: unix.BPF_JMP | unix.BPF_JEQ | unix.BPF_K,
			K:    nr,
		})
	}
	// x32-ABI numbers cannot be mapped back to the denylist; reject them
	// all rather than let them through unfiltered.
	prog = append(prog, unix.SockFilter{
		Code: unix.BPF_JMP | unix.BPF_JSET | unix.BPF_K,
		K:    x32SyscallBit,
	})
	// A deny or x32 match jumps to RET ERRNO; a non-match falls through
	// to the next check, and the last check falls through to RET ALLOW.
	allowIdx := len(prog)
	errnoIdx := allowIdx + 1
	for i := preamble; i < len(prog); i++ {
		prog[i].Jt = uint8(errnoIdx - i - 1)
	}
	prog = append(prog,
		unix.SockFilter{Code: unix.BPF_RET | unix.BPF_K, K: unix.SECCOMP_RET_ALLOW},
		unix.SockFilter{Code: unix.BPF_RET | unix.BPF_K, K: unix.SECCOMP_RET_ERRNO | uint32(unix.EPERM)},
	)
	return prog
}

// installSeccompDenylist attaches the cBPF denylist filter to the calling
// thread.
func installSeccompDenylist() error {
	prog := seccompProgram()
	fprog := unix.SockFprog{Len: uint16(len(prog)), Filter: &prog[0]}
	return unix.Prctl(unix.PR_SET_SECCOMP, unix.SECCOMP_MODE_FILTER,
		uintptr(unsafe.Pointer(&fprog)), 0, 0)
}
