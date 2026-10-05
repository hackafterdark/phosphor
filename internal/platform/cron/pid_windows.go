//go:build windows

package cron

import "syscall"

// processQueryLimitedInformation allows reading the process exit code.
// The stdlib syscall package does not export it.
const processQueryLimitedInformation = 0x1000

// pidAlive reports whether a process with the given PID appears to exist.
// It is a best-effort check used only to reclaim stale job locks.
func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	// A process object can outlive its process while another handle to it
	// is open, so a successful lookup is not proof of liveness: check the
	// exit code instead.
	handle, err := syscall.OpenProcess(processQueryLimitedInformation, false, uint32(pid))
	if err != nil {
		return false
	}
	defer syscall.CloseHandle(handle)
	var code uint32
	if err := syscall.GetExitCodeProcess(handle, &code); err != nil {
		return false
	}
	return code == stillActive
}

// stillActive is the exit code Windows reports while a process runs.
const stillActive = 259
