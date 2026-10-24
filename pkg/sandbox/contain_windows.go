//go:build windows

package sandbox

import (
	"fmt"
	"os/exec"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Per-command containment limits. The active-process cap stops fork
// bombs; the per-process memory cap stops a runaway process from
// exhausting the machine. Values are deliberately generous: legitimate
// dev tooling must keep working.
const (
	jobActiveProcessLimit = 256
	jobProcessMemoryLimit = 4 * 1024 * 1024 * 1024
)

// ContainProcess assigns cmd to a fresh Job Object with kill-on-close,
// an active-process cap, and a per-process memory cap. The returned
// release function closes the job handle; keep it alive for the whole
// lifetime of the command — closing it while the child runs terminates
// the child and its descendants (the kill-on-close guarantee).
//
// Assignment happens after spawn: Go's exec on Windows offers no
// pre-spawn job hook, so a grandchild spawned within microseconds of
// start can theoretically escape the job. Acceptable for v1.
func ContainProcess(cmd *exec.Cmd) (release func(), err error) {
	if cmd.Process == nil {
		return nil, fmt.Errorf("containment requires a started process")
	}
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, fmt.Errorf("create job object: %w", err)
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags:         windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE | windows.JOB_OBJECT_LIMIT_ACTIVE_PROCESS,
			ActiveProcessLimit: jobActiveProcessLimit,
		},
		ProcessMemoryLimit: jobProcessMemoryLimit,
	}
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		windows.CloseHandle(job)
		return nil, fmt.Errorf("configure job object: %w", err)
	}
	ph, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if err != nil {
		windows.CloseHandle(job)
		return nil, fmt.Errorf("open process: %w", err)
	}
	defer windows.CloseHandle(ph)
	if err := windows.AssignProcessToJobObject(job, ph); err != nil {
		windows.CloseHandle(job)
		return nil, fmt.Errorf("assign process to job: %w", err)
	}
	return func() { windows.CloseHandle(job) }, nil
}
