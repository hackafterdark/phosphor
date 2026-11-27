//go:build windows

package sandbox

import (
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// processStartTime returns when the given process PID started, using
// the process creation time from its kernel object. It is the
// start-time source for lease liveness checks: a PID recycled after
// the lease was written has a later creation time.
func processStartTime(pid int) time.Time {
	started, ok := processStarted(pid)
	if !ok {
		return time.Now()
	}
	return started
}

// processStarted reports whether pid is a live process and returns
// its creation time. Creation-time comparison rules out recycled
// PIDs: a lease holder that has since exited cannot leave its
// creation time in the future relative to the lease timestamp.
func processStarted(pid int) (time.Time, bool) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return time.Time{}, false
	}
	defer windows.CloseHandle(h)

	var creation, exit, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(h, &creation, &exit, &kernel, &user); err != nil {
		return time.Time{}, false
	}
	// #nosec G103 -- the pointer cast is required by the Windows
	// FILETIME/uint64 layout and does not alias live memory.
	return time.Unix(0, *(*int64)(unsafe.Pointer(&creation))), true
}
