//go:build !linux

package sandbox

import (
	"os/exec"
	"runtime"
)

// current returns the platform isolator. Phases 2 (macOS Seatbelt) and 3
// (Windows restricted token) replace these stubs; until then enforcement
// stays in-process and Available() reports honestly.
func Current() Isolator { return stubIsolator{} }

type stubIsolator struct{}

func (stubIsolator) Confine(cmd *exec.Cmd, spec Spec) error {
	return nil
}

func (stubIsolator) Available() (bool, string) {
	return false, runtime.GOOS + " sandbox backend not implemented yet"
}
