//go:build darwin

package sandbox

import (
	"fmt"
	"os"
	"os/exec"
)

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
	return nil
}

func (seatbeltIsolator) Available() (bool, string) {
	if _, err := os.Stat(seatbeltExecPath); err != nil {
		return false, "sandbox-exec not found at " + seatbeltExecPath
	}
	return true, "seatbelt (sandbox-exec); fs writes: kernel-enforced; reads and network: advisory"
}
