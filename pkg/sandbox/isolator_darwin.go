//go:build darwin

package sandbox

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

// init intercepts the --rlimit-exec wrapper argv before any other code
// runs, mirroring the Linux Landlock bootstrap: *any* binary importing
// pkg/sandbox can serve as the rlimit wrapper. Without this, re-execing
// a binary that lacks the CLI flag re-runs it with the wrapper argv as
// ordinary arguments.
func init() {
	if len(os.Args) > 2 && os.Args[1] == "--rlimit-exec" {
		if err := RunRLimitExec(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "phosphor rlimit-exec:", err)
			os.Exit(126)
		}
		os.Exit(1) // RunRLimitExec only returns on failure.
	}
}

// seatbeltExecPath is Apple's own sandbox launcher. Apple deprecated
// sandbox-exec (the man page has said so since 10.7) but it ships on
// every macOS release and is the industry-standard Seatbelt delivery
// path (Claude Code, Chromium helpers). Pin this path; do not LookPath.
const seatbeltExecPath = "/usr/bin/sandbox-exec"

// Current returns the platform isolator.
func Current() Isolator { return seatbeltIsolator{} }

// MacOS returns the Seatbelt isolator.
func MacOS() Isolator { return seatbeltIsolator{} }

// seatbeltIsolator confines children by wrapping their argv through
// sandbox-exec with a generated SBPL profile (the Claude Code pattern).
type seatbeltIsolator struct{}

func (seatbeltIsolator) Confine(cmd *exec.Cmd, spec Spec) error {
	if ok, detail := (seatbeltIsolator{}).Available(); !ok {
		return fmt.Errorf("sandbox unavailable: %s", detail)
	}
	if spec.WorkspaceRoot == "" {
		return fmt.Errorf("sandbox spec requires a workspace root")
	}
	if cmd.Path == "" {
		return fmt.Errorf("sandbox confine requires cmd.Path")
	}

	// sandbox-exec execs the wrapped command by path, so cmd.Path goes
	// in as the command position and argv[1:] follows it.
	cmd.Args = append(
		[]string{"sandbox-exec", "-p", seatbeltProfile(spec), cmd.Path},
		cmd.Args[1:]...,
	)
	cmd.Path = seatbeltExecPath
	if err := applyDarwinRLimits(cmd, spec.Limits); err != nil {
		return err
	}
	return nil
}

// applyDarwinRLimits installs kernel resource caps on the child. Go's
// exec on darwin offers no pre-exec hook, so when limits are requested
// the argv is wrapped through this binary's hidden --rlimit-exec mode,
// which lowers the soft and hard limits in the wrapper process and then
// execs the payload — the same re-exec pattern the Linux isolator uses
// for Landlock. Lowering limits needs no privilege; the caps cannot
// outlive the child.
func applyDarwinRLimits(cmd *exec.Cmd, limits ResourceLimits) error {
	if limits.Empty() {
		return nil
	}
	if cmd.Path == "" {
		return fmt.Errorf("rlimit wrap requires cmd.Path")
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	argv := []string{exe, "--rlimit-exec"}
	if limits.MaxProcs != 0 {
		argv = append(argv, "--rlimit", fmt.Sprintf("nproc=%d", limits.MaxProcs))
	}
	if limits.MaxFileMB != 0 {
		argv = append(argv, "--rlimit", fmt.Sprintf("fsize-mb=%d", limits.MaxFileMB))
	}
	argv = append(argv, "--")
	// payload[0] is exec'd directly by RunRLimitExec and execve does
	// not search PATH, so the payload must carry the resolved path
	// (cmd.Path is seatbeltExecPath here, not the bare argv[0] name).
	argv = append(argv, cmd.Path)
	argv = append(argv, cmd.Args[1:]...)
	cmd.Path = exe
	cmd.Args = argv
	return nil
}

// rlimitArgs carries the resource caps parsed from the wrapped argv.
type rlimitArgs struct {
	nproc   uint64
	fsizeMB uint64
}

// parseRLimitArgs splits sandbox-exec-style rlimit flags from the
// wrapped argv: --rlimit <res>=<value> up to the -- separator.
func parseRLimitArgs(args []string) (rl rlimitArgs, payload []string, err error) {
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--rlimit":
			i++
			if i >= len(args) {
				return rl, nil, fmt.Errorf("missing argument for --rlimit")
			}
			res, val, ok := strings.Cut(args[i], "=")
			if !ok {
				return rl, nil, fmt.Errorf("invalid --rlimit %q", args[i])
			}
			n, nerr := strconv.ParseUint(val, 10, 64)
			if nerr != nil || n == 0 {
				return rl, nil, fmt.Errorf("invalid --rlimit value %q", args[i])
			}
			switch res {
			case "nproc":
				rl.nproc = n
			case "fsize-mb":
				rl.fsizeMB = n
			default:
				return rl, nil, fmt.Errorf("unknown --rlimit resource %q", res)
			}
		case "--":
			return rl, args[i+1:], nil
		default:
			return rl, nil, fmt.Errorf("unexpected argument %q", args[i])
		}
	}
	return rl, nil, fmt.Errorf("no -- separator found")
}

// applyRLimits lowers the process limits in the wrapper process. The
// soft limit is pinned at or below the existing hard limit so a request
// above the kernel's own ceiling degrades to that ceiling instead of
// failing with EPERM; the hard limit is preserved (raising it needs
// CAP_SYS_RESOURCE / root). RLIMIT_NPROC is enforced per-UID on macOS,
// like Linux: the cap reads as a per-user ceiling for the exec tree.
func applyRLimits(rl rlimitArgs) error {
	if rl.nproc != 0 {
		var cur unix.Rlimit
		if err := unix.Getrlimit(unix.RLIMIT_NPROC, &cur); err != nil {
			return err
		}
		soft := rl.nproc
		if cur.Max != 0 && cur.Max < soft {
			soft = cur.Max
		}
		if err := unix.Setrlimit(unix.RLIMIT_NPROC, &unix.Rlimit{
			Cur: soft, Max: cur.Max,
		}); err != nil {
			return err
		}
	}
	if rl.fsizeMB != 0 {
		limit := rl.fsizeMB * 1024 * 1024
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

// RunRLimitExec is the entry point of the hidden --rlimit-exec mode:
// it parses the wrapped argv, lowers resource limits on itself, and
// execs the payload. It never returns on success.
func RunRLimitExec(args []string) error {
	rl, payload, err := parseRLimitArgs(args)
	if err != nil {
		return err
	}
	if len(payload) == 0 {
		return fmt.Errorf("no command given after --")
	}
	if err := applyRLimits(rl); err != nil {
		return err
	}
	return syscall.Exec(payload[0], payload, os.Environ())
}

func (seatbeltIsolator) Available() (bool, string) {
	if _, err := os.Stat(seatbeltExecPath); err != nil {
		return false, "sandbox-exec not found at " + seatbeltExecPath
	}
	return true, "seatbelt (sandbox-exec); fs writes: kernel-enforced; reads and network: advisory"
}
