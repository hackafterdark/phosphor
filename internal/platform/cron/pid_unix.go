//go:build !windows

package cron

import (
	"errors"
	"os"
	"syscall"
)

// pidAlive reports whether a process with the given PID appears to exist.
// It is a best-effort check used only to reclaim stale job locks.
func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// On Unix FindProcess always succeeds; probe liveness with signal 0.
	// EPERM means the process exists but belongs to another user, which
	// still counts as alive.
	if err := p.Signal(syscall.Signal(0)); err != nil {
		return errors.Is(err, syscall.EPERM)
	}
	return true
}
