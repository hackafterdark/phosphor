package sandbox

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func leaseFileName(pid int) string {
	return fmt.Sprintf(leaseFileNameFormat, pid)
}

func writeLeaseFile(t *testing.T, dir string, lease leaseInfo) string {
	t.Helper()
	payload, err := json.Marshal(lease)
	require.NoError(t, err)
	path := filepath.Join(dir, "sandbox", "leases", leaseFileName(lease.PID))
	require.NoError(t, os.WriteFile(path, payload, 0o600))
	return path
}

func TestLeaseAlive(t *testing.T) {
	t.Parallel()

	now := time.Now()
	lease := leaseInfo{PID: 4242, Started: now.Add(-time.Hour)}

	// A live holder whose start predates the lease.
	alive := leaseAlive(lease, func(int) (time.Time, bool) {
		return now.Add(-2 * time.Hour), true
	}, now)
	require.True(t, alive)

	// A recycled PID: started after the lease was written.
	recycled := leaseAlive(lease, func(int) (time.Time, bool) {
		return now.Add(-time.Minute), true
	}, now)
	require.False(t, recycled)

	// An unverifiable process is treated as alive: a false positive
	// only defers teardown, a false negative strips a live sibling.
	unverifiable := leaseAlive(lease, func(int) (time.Time, bool) {
		return time.Time{}, false
	}, now)
	require.True(t, unverifiable)
}

func TestForeignInstanceLeases(t *testing.T) {
	// Not parallel: RegisterInstanceLease mutates package lease state.
	dir := t.TempDir()
	require.NoError(t, RegisterInstanceLease(dir))
	t.Cleanup(func() { _ = ReleaseInstanceLease() })

	now := time.Now()
	live := func(int) (time.Time, bool) { return now.Add(-2 * time.Hour), true }

	// Only our own lease: no foreign holder.
	require.False(t, foreignInstanceLeases(live))

	// A live foreign lease defers teardown.
	writeLeaseFile(t, dir, leaseInfo{PID: 999999, Started: now.Add(-time.Hour)})
	require.True(t, foreignInstanceLeases(live))

	// A recycled-PID lease (the current process started after the
	// lease was written) is pruned, not counted.
	recycled := func(int) (time.Time, bool) { return now.Add(time.Minute), true }
	require.False(t, foreignInstanceLeases(recycled))
	// The recycled lease was pruned on the pass above.
	entries, err := os.ReadDir(filepath.Join(dir, "sandbox", "leases"))
	require.NoError(t, err)
	for _, e := range entries {
		require.NotContains(t, e.Name(), "999999")
	}
}

func TestLeaseRoundTrip(t *testing.T) {
	// Not parallel: RegisterInstanceLease mutates package lease state.
	dir := t.TempDir()
	require.NoError(t, RegisterInstanceLease(dir))

	leasePath := filepath.Join(dir, "sandbox", "leases", leaseFileName(os.Getpid()))
	lease, ok := readLease(leasePath)
	require.True(t, ok)
	require.Equal(t, os.Getpid(), lease.PID)

	require.NoError(t, ReleaseInstanceLease())
	_, err := os.Stat(leasePath)
	require.True(t, os.IsNotExist(err))
}
