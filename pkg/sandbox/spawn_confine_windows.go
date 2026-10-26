//go:build windows

package sandbox

import (
	"errors"
	"os/exec"
)

// errNoPath guards against launching a command whose path was never
// resolved; a blank argv[0] would fail deep inside CreateProcess.
var errNoPath = errors.New("confined spawn requires cmd.Path")

// spawnConfinedChild routes the Windows 11 tier through the raw
// AppContainer launch: exec.Cmd cannot carry
// PROC_THREAD_ATTRIBUTE_SECURITY_CAPABILITY, so on hosts where the
// probe selected tierAppContainer the confinement travels through the
// returned ConfinedChild instead of cmd. The restricted-token tier
// returns (nil, nil) and keeps confining through Confine.
func spawnConfinedChild(cmd *exec.Cmd, spec Spec) (ConfinedChild, error) {
	if selectedTier() != tierAppContainer {
		return nil, nil
	}
	if cmd.Path == "" {
		return nil, errNoPath
	}
	return SpawnConfined(cmd.Args, cmd.Env, cmd.Dir, spec, cmd.Stdin, cmd.Stdout, cmd.Stderr)
}
