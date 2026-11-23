//go:build !windows

package sandbox

// PreWarm is a no-op outside Windows: the Unix isolators carry
// confinement in the exec.Cmd itself and no host DACLs are touched,
// so there is no subtree crawl to pre-apply.
func PreWarm(mode, workspace string, limits ResourceLimits) bool { return false }

// WaitPreWarm has nothing to wait for outside Windows: PreWarm never
// starts a background crawl, so it returns immediately.
func WaitPreWarm() {}
