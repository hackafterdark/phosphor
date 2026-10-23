//go:build linux

package sandbox

import (
	"fmt"
	"os"
	"os/exec"
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
	if cmd.Path == "" {
		return fmt.Errorf("sandbox confine requires cmd.Path")
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
func splitPolicyArgs(args []string) (execPath string, ro, rw []string, argv []string, err error) {
	i := 0
	for ; i < len(args); i++ {
		switch args[i] {
		case "--exec":
			i++
			if i >= len(args) {
				return "", nil, nil, nil, fmt.Errorf("missing argument for --exec")
			}
			execPath = args[i]
		case "--ro":
			i++
			if i >= len(args) {
				return "", nil, nil, nil, fmt.Errorf("missing argument for --ro")
			}
			ro = append(ro, args[i])
		case "--rw":
			i++
			if i >= len(args) {
				return "", nil, nil, nil, fmt.Errorf("missing argument for --rw")
			}
			rw = append(rw, args[i])
		case "--":
			return execPath, ro, rw, args[i+1:], nil
		default:
			return "", nil, nil, nil, fmt.Errorf("unexpected argument %q", args[i])
		}
	}
	return "", nil, nil, nil, fmt.Errorf("no -- separator found")
}

// RunSandboxExec is the entry point of the hidden sandbox-exec subcommand:
// it parses the wrapped argv, enforces policy on itself, and execs the
// payload. It never returns on success.
func RunSandboxExec(args []string) error {
	execPath, ro, rw, argv, err := splitPolicyArgs(args)
	if err != nil {
		return err
	}
	return EnforceAndExec(execPath, ro, rw, argv)
}

// EnforceAndExec applies NO_NEW_PRIVS, the seccomp denylist, and Landlock
// path rules to the calling process, then execs the pre-resolved binary
// with the original argv (argv[0] preserved). It never returns on success.
func EnforceAndExec(execPath string, ro, rw, argv []string) error {
	if len(argv) == 0 {
		return fmt.Errorf("no command given after --")
	}
	if execPath == "" {
		execPath = argv[0]
	}
	if err := enforceLandlock(ro, rw); err != nil {
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
