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

// ContainProcess assigns cmd to a fresh Job Object with kill-on-close so
// cancelling or exiting Phosphor terminates the child and its descendants
// (Windows' equivalent of the Unix negative-PID process-group kill). The
// returned release function closes the job handle; keep it alive for the
// whole lifetime of the command — closing it while the child runs
// terminates the child and its descendants (the kill-on-close guarantee).
//
// When limits is true the job additionally caps the active process count
// and per-process memory — the sandbox-tier limits, applied only when a
// kernel sandbox policy is active. With limits false the job stays a pure
// cleanup mechanism, matching the Unix behavior where process-group
// isolation applies regardless of sandbox mode.
//
// Assignment happens after spawn: Go's exec on Windows offers no
// pre-spawn job hook, so a grandchild spawned within microseconds of
// start can theoretically escape the job. Acceptable for v1.
func ContainProcess(cmd *exec.Cmd, limits bool) (release func(), err error) {
	if cmd.Process == nil {
		return nil, fmt.Errorf("containment requires a started process")
	}
	ph, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if err != nil {
		return nil, fmt.Errorf("open process: %w", err)
	}
	defer windows.CloseHandle(ph)
	return attachContainmentJob(ph, limits)
}

// attachContainmentJob applies the containment job policy to an
// already-open process handle. Shared by the exec.Cmd path
// (ContainProcess) and the AppContainer launcher, which has the
// process handle in hand.
func attachContainmentJob(h windows.Handle, limits bool) (release func(), err error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, fmt.Errorf("create job object: %w", err)
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
		},
	}
	if limits {
		info.BasicLimitInformation.LimitFlags |= windows.JOB_OBJECT_LIMIT_ACTIVE_PROCESS |
			windows.JOB_OBJECT_LIMIT_PROCESS_MEMORY
		info.BasicLimitInformation.ActiveProcessLimit = jobActiveProcessLimit
		info.ProcessMemoryLimit = jobProcessMemoryLimit
	}
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		windows.CloseHandle(job)
		return nil, fmt.Errorf("configure job object: %w", err)
	}
	if err := windows.AssignProcessToJobObject(job, h); err != nil {
		windows.CloseHandle(job)
		return nil, fmt.Errorf("assign process to job: %w", err)
	}
	return func() { windows.CloseHandle(job) }, nil
}
