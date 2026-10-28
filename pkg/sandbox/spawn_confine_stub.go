//go:build !windows

package sandbox

import "os/exec"

// spawnConfinedChild is a no-op outside Windows: the Unix isolators
// carry their confinement in the exec.Cmd itself (posix_spawn attrs,
// Landlock/seccomp, Seatbelt), so no direct kernel launch is needed.
func spawnConfinedChild(cmd *exec.Cmd, spec Spec) (ConfinedChild, error) {
	return nil, nil
}
