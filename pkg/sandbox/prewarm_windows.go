//go:build windows

package sandbox

import (
	"log/slog"
	"time"

	"golang.org/x/sys/windows"
)

// PreWarm applies the container ACE grants a kernel boundary will
// later need for this workspace, in the background. The first confined
// launch of a session otherwise pays the kernel-side subtree crawl
// (see grantAccess) while the caller waits — on a large workspace or
// toolchain cache that crawl runs for minutes and the command appears
// to hang. Pre-warming at app startup overlaps the crawl with normal
// startup work; the memoized grantAccess makes the first launch itself
// free. Returns true when the pre-warm was started (sandboxing active
// and available); grants stay additive and are revoked at shutdown by
// CleanupContainerGrants like any other grant.
func PreWarm(mode, workspace string, limits ResourceLimits) bool {
	if !ModeEnabled(mode) || workspace == "" {
		return false
	}
	if ok, _ := Available(); !ok {
		return false
	}
	spec := SpecForLimits(workspace, nil, limits)
	go func() {
		if err := preWarmGrants(workspace, spec); err != nil {
			slog.Warn("Failed to pre-apply sandbox container ACEs",
				"workspace", workspace, "error", err)
			return
		}
		slog.Debug("Pre-applied sandbox container ACEs", "workspace", workspace)
	}()
	return true
}

// preWarmGrants derives the workspace's container SID and applies the
// spec's root grants. The grants memoize, so a confined launch racing
// the pre-warm either waits on the ACL mutex for the crawl it would
// have paid itself, or finds the grant already applied. The total
// duration is logged so the pre-warm's overlap with startup work is
// measurable.
func preWarmGrants(workspace string, spec Spec) error {
	started := time.Now()
	sid, err := containerSidFor(workspace)
	if err != nil {
		return err
	}
	defer windows.FreeSid(sid)
	err = prepareConfinedRoots(sid, spec)
	slog.Info("Sandbox pre-warm finished",
		"workspace", workspace, "dur", time.Since(started), "err", err)
	return err
}
