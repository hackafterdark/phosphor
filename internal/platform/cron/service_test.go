package cron

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/hackafterdark/phosphor/pkg/config"
)

// TestMain runs the package with mock providers so no test pays the
// provider-discovery network fetch inside config.Init. Setting the flag once
// here (rather than per test) keeps t.Parallel tests race-free.
func TestMain(m *testing.M) {
	config.UseMockProviders = true
	code := m.Run()
	config.UseMockProviders = false
	config.ResetProviders()
	os.Exit(code)
}

// newTestService builds a cron service over an isolated config store. The
// XDG redirects keep config.Init off the developer's real global config,
// which may define custom providers whose endpoints would otherwise be
// dialed on every call. Tests using this helper must not be parallel
// (t.Setenv is incompatible with t.Parallel).
func newTestService(t *testing.T) *Service {
	t.Helper()
	isolated := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(isolated, ".config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(isolated, ".local", "share"))
	workingDir := t.TempDir()
	store, err := config.Init(workingDir, filepath.Join(workingDir, "data"), false)
	require.NoError(t, err)
	return NewService(nil, store, nil)
}

func TestLoadJobFile_DefaultsSessionMode(t *testing.T) {
	s := newTestService(t)

	path := filepath.Join(t.TempDir(), "job.md")
	err := os.WriteFile(path, []byte("---\ntitle: \"Test\"\nschedule: \"0 9 * * *\"\n---\n\nDo the thing.\n"), 0o644)
	require.NoError(t, err)

	job, err := s.loadJobFile(path)
	require.NoError(t, err)
	require.Equal(t, "ephemeral", job.SessionMode)
}

func TestLoadJobFile_KeepsExplicitSessionMode(t *testing.T) {
	s := newTestService(t)

	path := filepath.Join(t.TempDir(), "job.md")
	err := os.WriteFile(path, []byte("---\ntitle: \"Test\"\nschedule: \"0 9 * * *\"\nsession_mode: \"persistent\"\n---\n\nDo the thing.\n"), 0o644)
	require.NoError(t, err)

	job, err := s.loadJobFile(path)
	require.NoError(t, err)
	require.Equal(t, "persistent", job.SessionMode)
}

func TestScheduleJob_InvalidSchedule(t *testing.T) {
	s := newTestService(t)

	err := s.scheduleJob(context.Background(), "bad", &Job{
		Name:        "bad",
		Schedule:    "definitely not a cron spec",
		SessionMode: "ephemeral",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid schedule")

	// The job must not appear in the scheduled-jobs listing.
	require.Empty(t, s.GetScheduledJobs())
}

func TestScheduleJob_ValidSchedule(t *testing.T) {
	s := newTestService(t)

	err := s.scheduleJob(context.Background(), "good", &Job{
		Name:        "good",
		Schedule:    "0 9 * * *",
		SessionMode: "ephemeral",
	})
	require.NoError(t, err)
	require.Len(t, s.GetScheduledJobs(), 1)
}

func TestAcquireJobLock_BlocksWhenHeldByLiveProcess(t *testing.T) {
	s := newTestService(t)

	jobDir := filepath.Join(s.cfg.WorkingDir(), ".phosphor/jobs", "locked")
	require.NoError(t, os.MkdirAll(jobDir, 0o755))
	lockFile := filepath.Join(jobDir, ".job.lock")
	require.NoError(t, os.WriteFile(lockFile, []byte(strconv.Itoa(os.Getpid())), 0o644))

	unlock, ok := s.acquireJobLock("locked")
	require.False(t, ok)
	require.Nil(t, unlock)
}

func TestAcquireJobLock_ReclaimsStaleLock(t *testing.T) {
	s := newTestService(t)

	jobDir := filepath.Join(s.cfg.WorkingDir(), ".phosphor/jobs", "stale")
	require.NoError(t, os.MkdirAll(jobDir, 0o755))
	lockFile := filepath.Join(jobDir, ".job.lock")
	// A PID that cannot be alive: far above any valid PID.
	require.NoError(t, os.WriteFile(lockFile, []byte("2147483647"), 0o644))

	unlock, ok := s.acquireJobLock("stale")
	require.True(t, ok)
	require.NotNil(t, unlock)

	// The reclaimed lock holds our PID, and releasing removes it.
	pid, err := strconv.Atoi(readFile(t, lockFile))
	require.NoError(t, err)
	require.Equal(t, os.Getpid(), pid)

	unlock()
	_, err = os.Stat(lockFile)
	require.True(t, os.IsNotExist(err))
}

func TestAcquireJobLock_ReclaimsLegacyEmptyLock(t *testing.T) {
	s := newTestService(t)

	jobDir := filepath.Join(s.cfg.WorkingDir(), ".phosphor/jobs", "legacy")
	require.NoError(t, os.MkdirAll(jobDir, 0o755))
	lockFile := filepath.Join(jobDir, ".job.lock")
	// Pre-PID lock files were empty and unverifiable; they must not wedge
	// the job forever.
	require.NoError(t, os.WriteFile(lockFile, []byte{}, 0o644))

	unlock, ok := s.acquireJobLock("legacy")
	require.True(t, ok)
	unlock()
}

func TestPidAlive(t *testing.T) {
	t.Parallel()
	require.True(t, pidAlive(os.Getpid()))
	require.False(t, pidAlive(0))
	require.False(t, pidAlive(-1))
	require.False(t, pidAlive(2147483647))
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(data)
}
