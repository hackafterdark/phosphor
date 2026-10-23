//go:build !linux && !darwin

package sandbox

import (
	"os/exec"
	"runtime"
)

// current returns the platform isolator. Phase 3 (Windows restricted
// token) replaces this stub; until then enforcement stays in-process and
// Available() reports honestly.
func Current() Isolator { return stubIsolator{} }

type stubIsolator struct{}

func (stubIsolator) Confine(cmd *exec.Cmd, spec Spec) error {
	return nil
}

func (stubIsolator) Available() (bool, string) {
	return false, runtime.GOOS + " sandbox backend not implemented yet"
}
