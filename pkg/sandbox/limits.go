package sandbox

import "fmt"

// ResourceLimits are optional kernel resource caps for confined
// children. Zero values mean "no limit" — limits are opt-in so an
// unset config can never break legitimate toolchains.
type ResourceLimits struct {
	// MaxProcs caps the per-user process/thread count while the sandboxed
	// child runs (RLIMIT_NPROC). The kernel enforces it against every process
	// owned by the real UID, not just the confined subtree.
	MaxProcs uint64
	// MaxFileMB caps the size of files the child may create
	// (RLIMIT_FSIZE), in megabytes.
	MaxFileMB uint64
}

// Empty reports whether no limits are requested.
func (l ResourceLimits) Empty() bool {
	return l.MaxProcs == 0 && l.MaxFileMB == 0
}

// Sanity bounds for configured limits. A max_procs below the floor is
// almost certainly a mistake (the Go runtime alone runs many threads),
// and a value above the ceiling defeats the fork-bomb purpose; both
// fail closed at Confine time rather than silently weakening the
// sandbox (invariant 1).
const (
	minMaxProcs  = 64
	maxMaxProcs  = 100_000
	maxMaxFileMB = 1_000_000
)

// Validate rejects out-of-range non-zero limits.
func (l ResourceLimits) Validate() error {
	if l.MaxProcs != 0 && (l.MaxProcs < minMaxProcs || l.MaxProcs > maxMaxProcs) {
		return fmt.Errorf("max_procs %d out of range [%d, %d]", l.MaxProcs, minMaxProcs, maxMaxProcs)
	}
	if l.MaxFileMB != 0 && l.MaxFileMB > maxMaxFileMB {
		return fmt.Errorf("max_file_mb %d exceeds %d", l.MaxFileMB, maxMaxFileMB)
	}
	return nil
}
