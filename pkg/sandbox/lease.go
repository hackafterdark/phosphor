package sandbox

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Instance leases coordinate sandbox teardown across multiple
// phosphor processes sharing one workspace. The Windows sandbox tier
// grants host-directory ACEs keyed by a workspace-derived container
// SID (see containerNameFor), so the SID — and therefore the grants —
// is shared by every instance of the workspace. Revoking them while
// another instance is still running strips that instance's confined
// children mid-run.
//
// Each running instance holds a lease file under
// {dataDir}/sandbox/leases/ named after its PID, recording the
// process start time. At teardown, an instance revokes its container
// ACEs only when no live foreign lease remains; otherwise it leaves
// the grants (and their memo entries) for the last instance to clean
// up, mirroring the existing "late revoke" path where a
// deadline-missed revoke lingers until the next launch.

// leaseFileNameFormat is the on-disk name of a lease file.
const leaseFileNameFormat = "pid-%d.json"

// leaseInfo is the JSON payload of a lease file.
type leaseInfo struct {
	PID     int       `json:"pid"`
	Started time.Time `json:"started"`
}

var (
	leaseDirMu sync.Mutex
	leaseDir   string
)

// RegisterInstanceLease records this process's lease in dataDir so
// sibling instances can detect it, and prunes leases left behind by
// crashed instances. It is called once at app startup; an empty
// dataDir disables lease coordination (teardown then behaves as if
// no sibling instances existed).
func RegisterInstanceLease(dataDir string) error {
	leaseDirMu.Lock()
	defer leaseDirMu.Unlock()
	leaseDir = dataDir
	if dataDir == "" {
		return nil
	}
	dir := leaseDirPath(dataDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create sandbox lease dir: %w", err)
	}
	pruneStaleLeases(dir, processStarted, time.Now())
	lease := leaseInfo{PID: os.Getpid(), Started: processStartTime(os.Getpid())}
	payload, err := json.Marshal(lease)
	if err != nil {
		return err
	}
	name := filepath.Join(dir, fmt.Sprintf(leaseFileNameFormat, lease.PID))
	return os.WriteFile(name, payload, 0o600)
}

// ReleaseInstanceLease removes this process's lease file. It runs at
// shutdown, before the teardown decision below, so the instance's own
// lease never blocks its own cleanup.
func ReleaseInstanceLease() error {
	leaseDirMu.Lock()
	defer leaseDirMu.Unlock()
	if leaseDir == "" {
		return nil
	}
	name := filepath.Join(leaseDirPath(leaseDir), fmt.Sprintf(leaseFileNameFormat, os.Getpid()))
	if err := os.Remove(name); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// hasForeignInstanceLeases reports whether another live phosphor
// instance holds a lease in the registered data directory. Leases
// whose PID is gone (or whose PID has been recycled — detected via a
// start-time mismatch) are pruned instead of counted.
func hasForeignInstanceLeases() bool {
	return foreignInstanceLeases(processStarted)
}

// foreignInstanceLeases is the testable core of
// hasForeignInstanceLeases: startedAt judges lease liveness so tests
// can simulate live and dead holders without real processes.
func foreignInstanceLeases(startedAt func(int) (time.Time, bool)) bool {
	leaseDirMu.Lock()
	dir := leaseDir
	leaseDirMu.Unlock()
	if dir == "" {
		return false
	}
	dir = leaseDirPath(dir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	now := time.Now()
	self := os.Getpid()
	found := false
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), "pid-") || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		lease, ok := readLease(path)
		if !ok || lease.PID == self {
			// Unreadable or self-owned: remove; it proves nothing
			// about a live sibling.
			_ = os.Remove(path)
			continue
		}
		if leaseAlive(lease, startedAt, now) {
			found = true
			continue
		}
		_ = os.Remove(path)
	}
	return found
}

// leaseAlive reports whether the process behind lease is provably
// still running, judged by startedAt(pid). The check is deliberately
// monotone-safe: a process whose start time cannot be verified (e.g.
// the API refused) is treated as alive, because a false negative here
// strips access from a live sibling while a false positive only
// defers teardown to the next launch.
func leaseAlive(lease leaseInfo, startedAt func(int) (time.Time, bool), now time.Time) bool {
	started, ok := startedAt(lease.PID)
	if !ok {
		return true
	}
	// A live holder of the lease must have started before (or right
	// around) the time the lease was written. A process that started
	// after the lease is a recycled PID slot, not the original holder.
	// The one-second slack absorbs clock/timestamp granularity.
	return !started.After(lease.Started.Add(time.Second))
}

// pruneStaleLeases removes lease files whose process is gone or whose
// PID has been recycled. now is passed in so the logic is testable
// without waiting out real process lifetimes.
func pruneStaleLeases(dir string, startedAt func(int) (time.Time, bool), now time.Time) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), "pid-") || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		lease, ok := readLease(path)
		if !ok {
			_ = os.Remove(path)
			continue
		}
		if lease.PID == os.Getpid() {
			continue
		}
		if !leaseAlive(lease, startedAt, now) {
			_ = os.Remove(path)
		}
	}
}

// readLease parses a lease file; malformed or partial files are
// treated as invalid.
func readLease(path string) (leaseInfo, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return leaseInfo{}, false
	}
	var lease leaseInfo
	if err := json.Unmarshal(data, &lease); err != nil || lease.PID <= 0 {
		return leaseInfo{}, false
	}
	return lease, true
}

// leaseDirPath returns the lease directory inside the data directory.
func leaseDirPath(dataDir string) string {
	return filepath.Join(dataDir, "sandbox", "leases")
}
