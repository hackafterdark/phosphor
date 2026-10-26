//go:build linux

package sandbox

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

// Current returns the platform isolator.
func Current() Isolator { return landlockIsolator{} }

// landlockIsolator confines children by re-execing this binary in hidden
// sandbox-exec mode (the Codex argv-wrap pattern), instead of hooking
// fork/exec directly.
type landlockIsolator struct{}

// Linux returns the Landlock+seccomp isolator.
func Linux() Isolator { return landlockIsolator{} }

func (landlockIsolator) Confine(cmd *exec.Cmd, spec Spec) error {
	if ok, detail := (landlockIsolator{}).Available(); !ok {
		return fmt.Errorf("sandbox unavailable: %s", detail)
	}
	if spec.WorkspaceRoot == "" {
		return fmt.Errorf("sandbox spec requires a workspace root")
	}
	// Landlock is allow-list-only: a deny path inside an allowed root
	// cannot be enforced, so fail loudly instead of sandboxing less
	// than the spec claims (invariant 6, fail closed).
	if err := spec.ValidateDenyRead(); err != nil {
		return err
	}
	if cmd.Path == "" {
		return fmt.Errorf("sandbox confine requires cmd.Path")
	}
	if spec.Network == NetworkProxyOnly {
		if spec.ProxyPort <= 0 || spec.ProxyPort > 65535 {
			return fmt.Errorf("proxy-only network requires a valid broker port")
		}
		// TCP port rules need Landlock ABI v4 (kernel 6.7+). Fail at
		// Confine time, not inside the exec wrapper, so the caller can
		// degrade (workspace) or refuse (strict) per invariant 6.
		if abi, err := landlockABIVersion(); err != nil || abi < 4 {
			return fmt.Errorf("proxy-only network needs landlock abi v4, kernel has v%d", abi)
		}
	}

	exe, err := os.Executable()
	if err != nil {
		return err
	}

	var argv []string
	// argv[0] is reserved for the program name; the subcommand token
	// follows it so cobra dispatches to sandbox-exec instead of choking on
	// the flag.
	argv = append(argv, exe, "sandbox-exec", "--exec", cmd.Path)
	argv = append(argv, "--rw", spec.WorkspaceRoot)
	for _, p := range spec.ReadOnlyRoots {
		argv = append(argv, "--ro", p)
	}
	for _, p := range spec.ReadOnlyFiles {
		argv = append(argv, "--ro", p)
	}
	for _, p := range spec.ReadWriteRoots {
		argv = append(argv, "--rw", p)
	}
	for _, p := range spec.ReadWriteFiles {
		argv = append(argv, "--rw", p)
	}
	switch spec.Network {
	case NetworkDeny:
		argv = append(argv, "--net-deny")
	case NetworkProxyOnly:
		argv = append(argv, "--net-proxy", strconv.Itoa(spec.ProxyPort))
	}
	if spec.Limits.MaxProcs != 0 {
		argv = append(argv, "--rlimit", "nproc="+strconv.FormatUint(spec.Limits.MaxProcs, 10))
	}
	if spec.Limits.MaxFileMB != 0 {
		argv = append(argv, "--rlimit", "fsize-mb="+strconv.FormatUint(spec.Limits.MaxFileMB, 10))
	}
	argv = append(argv, "--")
	argv = append(argv, cmd.Args...)

	cmd.Path = exe
	cmd.Args = argv
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	// If Phosphor dies, sandboxed children die with it; the kernel enforces
	// this even if the wrapper process is skipped.
	cmd.SysProcAttr.Pdeathsig = killOnParentDeath()
	return nil
}

func (landlockIsolator) Available() (bool, string) {
	abi, err := landlockABIVersion()
	if err != nil || abi == 0 {
		return false, "landlock unsupported by kernel"
	}
	network := "advisory (kernel <6.7)"
	if abi >= 4 {
		network = "kernel-enforced (landlock abi v4)"
	}
	return true, fmt.Sprintf("landlock abi v%d; network: %s", abi, network)
}

// landlockABIVersion returns the kernel's Landlock ABI level; zero means
// Landlock is unavailable. The kernel-defined query form passes attr=NULL,
// size=0 and the version flag.
func landlockABIVersion() (int, error) {
	const queryVersion = 1
	v, _, errno := rawLandlockVersion(queryVersion)
	if errno != 0 {
		return 0, errno
	}
	return int(v), nil
}

// splitPolicyArgs is exported to internal/cmd for argv parsing of the
// hidden sandbox-exec subcommand.
func splitPolicyArgs(args []string) (execPath string, ro, rw []string, net netPolicy, argv []string, err error) {
	i := 0
	for ; i < len(args); i++ {
		switch args[i] {
		case "--exec":
			i++
			if i >= len(args) {
				return "", nil, nil, net, nil, fmt.Errorf("missing argument for --exec")
			}
			execPath = args[i]
		case "--ro":
			i++
			if i >= len(args) {
				return "", nil, nil, net, nil, fmt.Errorf("missing argument for --ro")
			}
			ro = append(ro, args[i])
		case "--rw":
			i++
			if i >= len(args) {
				return "", nil, nil, net, nil, fmt.Errorf("missing argument for --rw")
			}
			rw = append(rw, args[i])
		case "--net-deny":
			net.mode = NetworkDeny
		case "--net-proxy":
			i++
			if i >= len(args) {
				return "", nil, nil, net, nil, fmt.Errorf("missing argument for --net-proxy")
			}
			port, perr := strconv.Atoi(args[i])
			if perr != nil || port <= 0 || port > 65535 {
				return "", nil, nil, net, nil, fmt.Errorf("invalid --net-proxy port %q", args[i])
			}
			net.mode = NetworkProxyOnly
			net.port = uint16(port)
		case "--rlimit":
			i++
			if i >= len(args) {
				return "", nil, nil, net, nil, fmt.Errorf("missing argument for --rlimit")
			}
			res, val, ok := strings.Cut(args[i], "=")
			if !ok {
				return "", nil, nil, net, nil, fmt.Errorf("invalid --rlimit %q", args[i])
			}
			n, nerr := strconv.ParseUint(val, 10, 64)
			if nerr != nil || n == 0 {
				return "", nil, nil, net, nil, fmt.Errorf("invalid --rlimit value %q", args[i])
			}
			switch res {
			case "nproc":
				net.nproc = n
			case "fsize-mb":
				net.fsizeMB = n
			default:
				return "", nil, nil, net, nil, fmt.Errorf("unknown --rlimit resource %q", res)
			}
		case "--":
			return execPath, ro, rw, net, args[i+1:], nil
		default:
			return "", nil, nil, net, nil, fmt.Errorf("unexpected argument %q", args[i])
		}
	}
	return "", nil, nil, net, nil, fmt.Errorf("no -- separator found")
}

// netPolicy carries the kernel network posture parsed from the wrapped
// argv. Zero value means no network restriction (NetworkAllow).
type netPolicy struct {
	mode    NetworkMode
	port    uint16
	nproc   uint64
	fsizeMB uint64
}

// RunSandboxExec is the entry point of the hidden sandbox-exec subcommand:
// it parses the wrapped argv, enforces policy on itself, and execs the
// payload. It never returns on success.
func RunSandboxExec(args []string) error {
	execPath, ro, rw, net, argv, err := splitPolicyArgs(args)
	if err != nil {
		return err
	}
	return EnforceAndExec(execPath, ro, rw, net, argv)
}

// EnforceAndExec applies NO_NEW_PRIVS, the seccomp denylist, and Landlock
// path rules to the calling process, then execs the pre-resolved binary
// with the original argv (argv[0] preserved). It never returns on success.
func EnforceAndExec(execPath string, ro, rw []string, net netPolicy, argv []string) error {
	if len(argv) == 0 {
		return fmt.Errorf("no command given after --")
	}
	if execPath == "" {
		execPath = argv[0]
	}
	if err := enforceLandlock(ro, rw, net); err != nil {
		return err
	}
	return syscallExec(execPath, argv, os.Environ())
}

// String renders the policy for diagnostics (no secrets: paths only).
func (s Spec) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "workspace=%s", s.WorkspaceRoot)
	if len(s.ReadOnlyRoots) > 0 {
		fmt.Fprintf(&b, " ro=%v", s.ReadOnlyRoots)
	}
	if len(s.ReadWriteRoots) > 0 {
		fmt.Fprintf(&b, " rw=%v", s.ReadWriteRoots)
	}
	return b.String()
}
