//go:build !windows

package sandbox

import (
	"os"
	"syscall"
	"time"
)

// processStartTime is the non-Windows counterpart of the lease
// start-time helper. Outside Windows the sandbox tier carries
// confinement in the exec.Cmd itself and no host DACLs are touched,
// so the value is only used to stamp the lease file for uniformity.
func processStartTime(int) time.Time { return time.Now() }

// processStarted reports liveness of a pid via the OS process table.
// The sandbox teardown that consults leases is Windows-only, so the
// check is a coarse existence probe here.
func processStarted(pid int) (time.Time, bool) {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return time.Time{}, false
	}
	// On Unix FindProcess always succeeds; signal 0 distinguishes a
	// live process from a recycled PID slot.
	if err := proc.Signal(syscall.Signal(0)); err != nil {
		return time.Time{}, false
	}
	return time.Now(), true
}
