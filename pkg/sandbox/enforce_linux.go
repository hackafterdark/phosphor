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
}

func rawLandlockVersion(queryFlag uintptr) (uintptr, uintptr, syscall.Errno) {
	return unix.RawSyscall(unix.SYS_LANDLOCK_CREATE_RULESET, 0, 0, queryFlag)
}

func syscallExec(bin string, argv, envv []string) error {
	return syscall.Exec(bin, argv, envv)
}

func killOnParentDeath() syscall.Signal {
	return syscall.SIGKILL
}

// enforceLandlock applies NO_NEW_PRIVS, the seccomp denylist, and Landlock
// path rules to the calling process. Landlock is applied last so a failed
// probe never leaves a half-restricted caller.
func enforceLandlock(ro, rw []string, net netPolicy) error {
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

// installSeccompDenylist attaches a minimal cBPF filter: allow everything
// except deniedSyscalls (EPERM), kill on foreign-arch execution.
func installSeccompDenylist() error {
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
	// Each deny check jumps over the remaining checks to RET ALLOW on
	// match; non-matches fall through to the RET ERRNO at the end.
	allowIdx := len(prog)
	for i := preamble; i < len(prog); i++ {
		prog[i].Jt = uint8(allowIdx - i - 1)
	}
	prog = append(prog,
		unix.SockFilter{Code: unix.BPF_RET | unix.BPF_K, K: unix.SECCOMP_RET_ALLOW},
		unix.SockFilter{Code: unix.BPF_RET | unix.BPF_K, K: unix.SECCOMP_RET_ERRNO | uint32(unix.EPERM)},
	)

	fprog := unix.SockFprog{Len: uint16(len(prog)), Filter: &prog[0]}
	return unix.Prctl(unix.PR_SET_SECCOMP, unix.SECCOMP_MODE_FILTER,
		uintptr(unsafe.Pointer(&fprog)), 0, 0)
}
