//go:build !windows

package sandbox

import "os/exec"

// ContainProcess is a no-op outside Windows: Linux uses session/PGID
// isolation (see pkg/shell/exec_unix.go) and macOS v1 relies on the
// Seatbelt profile alone. The limits flag mirrors the Windows signature
// (see contain_windows.go); it is ignored here.
func ContainProcess(cmd *exec.Cmd, limits bool) (release func(), err error) {
	return nil, nil
}
