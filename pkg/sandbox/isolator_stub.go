//go:build !linux && !darwin && !windows

package sandbox

import (
	"os/exec"
	"runtime"
)

// current returns the platform isolator. Phase 4 (AppContainer FS
// boundary on Windows) and later phases replace these stubs; until then
// enforcement stays in-process and Available() reports honestly.
func Current() Isolator { return stubIsolator{} }

type stubIsolator struct{}

func (stubIsolator) Confine(cmd *exec.Cmd, spec Spec) error {
	return nil
}

func (stubIsolator) Available() (bool, string) {
	return false, runtime.GOOS + " sandbox backend not implemented yet"
}
