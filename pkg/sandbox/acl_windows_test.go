//go:build windows

package sandbox

import (
	"context"
	"sync"
	"testing"
	"time"
	"unsafe"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows"
)

// hasContainerAce reports whether path's DACL carries an allow ACE for
// sid (test-side ACL walk; production code never needs it).
func hasContainerAce(t *testing.T, path string, sid *windows.SID) bool {
	t.Helper()
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	require.NoError(t, err)
	dacl, _, err := sd.DACL()
	require.NoError(t, err)
	if dacl == nil {
		return false
	}
	for i := uint32(0); i < uint32(dacl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil {
			continue
		}
		aceSid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if windows.EqualSid(aceSid, sid) {
			return true
		}
	}
	return false
}

func TestGrantAccessAddsContainerAce(t *testing.T) {
	dir := t.TempDir()
	sid, err := deriveContainerSid("phosphor.sandbox.grant")
	require.NoError(t, err)
	defer windows.FreeSid(sid)

	require.NoError(t, grantAccess(dir, sid, accessFor(false)))
	require.True(t, hasContainerAce(t, dir, sid),
		"container ACE missing after grant")
}

func TestGrantAccessPreservesExistingAces(t *testing.T) {
	dir := t.TempDir()
	sid, err := deriveContainerSid("phosphor.sandbox.preserve")
	require.NoError(t, err)
	defer windows.FreeSid(sid)

	sdBefore, err := windows.GetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	require.NoError(t, err)
	daclBefore, _, err := sdBefore.DACL()
	require.NoError(t, err)
	before := uint32(0)
	if daclBefore != nil {
		before = uint32(daclBefore.AceCount)
	}

	require.NoError(t, grantAccess(dir, sid, accessFor(true)))

	sdAfter, err := windows.GetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	require.NoError(t, err)
	daclAfter, _, err := sdAfter.DACL()
	require.NoError(t, err)
	if daclAfter == nil {
		t.Fatal("DACL became NULL after grant")
	}
	// SetEntriesInAclW splits the inheritable grant into two ACEs
	// (self + inherit-only); the original ACEs must survive verbatim.
	require.GreaterOrEqual(t, uint32(daclAfter.AceCount), before+1)
	require.True(t, hasContainerAce(t, dir, sid))
}

func TestCleanupContainerGrantsRevokesAces(t *testing.T) {
	dir := t.TempDir()
	sid, err := deriveContainerSid("phosphor.sandbox.teardown")
	require.NoError(t, err)
	defer windows.FreeSid(sid)

	require.NoError(t, grantAccess(dir, sid, accessFor(false)))
	require.True(t, hasContainerAce(t, dir, sid),
		"container ACE missing after grant")

	require.NoError(t, CleanupContainerGrants(context.Background()))
	require.False(t, hasContainerAce(t, dir, sid),
		"container ACE must be revoked on teardown")
}

func TestCleanupContainerGrantsAllowsRegrant(t *testing.T) {
	dir := t.TempDir()
	sid, err := deriveContainerSid("phosphor.sandbox.regrant")
	require.NoError(t, err)
	defer windows.FreeSid(sid)

	require.NoError(t, grantAccess(dir, sid, accessFor(false)))
	require.NoError(t, CleanupContainerGrants(context.Background()))

	// A second launch after teardown must re-grant: the memoized
	// grant was cleared, so the ACE reappears.
	require.NoError(t, grantAccess(dir, sid, accessFor(false)))
	require.True(t, hasContainerAce(t, dir, sid),
		"ACE must be re-grantable after teardown")
	require.NoError(t, CleanupContainerGrants(context.Background()))
}

// TestCleanupContainerGrantsHonorsDeadline verifies the teardown is
// bounded: with an already-expired context the revoke tree crawl is
// never started, the caller returns promptly with the deadline error,
// and the grant memo keeps its entry so the ACE is revoked at the
// next launch's teardown instead of lingering untracked.
func TestCleanupContainerGrantsHonorsDeadline(t *testing.T) {
	dir := t.TempDir()
	sid, err := deriveContainerSid("phosphor.sandbox.deadline")
	require.NoError(t, err)
	defer windows.FreeSid(sid)

	require.NoError(t, grantAccess(dir, sid, accessFor(false)))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = CleanupContainerGrants(ctx)
	require.ErrorIs(t, err, context.Canceled,
		"cleanup must surface the caller's deadline error")
	require.True(t, hasContainerAce(t, dir, sid),
		"ACE must survive an abandoned teardown (tracked for the next launch)")

	// The next teardown revokes the retained grant.
	require.NoError(t, CleanupContainerGrants(context.Background()))
	require.False(t, hasContainerAce(t, dir, sid),
		"retained grant must be revoked on the next teardown")
}

// TestGrantAccessPersistentSurvivesTeardown pins the cache-root
// contract: persistent grants are not revoked at teardown, so the
// kernel-side subtree crawl is reused by the next session.
func TestGrantAccessPersistentSurvivesTeardown(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	sid, err := deriveContainerSid("phosphor.sandbox.persist")
	require.NoError(t, err)
	defer windows.FreeSid(sid)

	require.NoError(t, grantAccessPersistent(dir, sid, accessFor(false)))
	require.True(t, hasContainerAce(t, dir, sid),
		"container ACE missing after persistent grant")

	require.NoError(t, CleanupContainerGrants(context.Background()))
	require.True(t, hasContainerAce(t, dir, sid),
		"persistent ACE must survive teardown for reuse next session")
}

// TestGrantAccessPersistentReusesMemoAcrossRestart verifies the
// persisted memo short-circuits the crawl after a restart: with the
// in-memory memo forgotten, the DACL-verified memo hit must return
// promptly instead of re-crawling the subtree.
func TestGrantAccessPersistentReusesMemoAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	sid, err := deriveContainerSid("phosphor.sandbox.restart")
	require.NoError(t, err)
	defer windows.FreeSid(sid)

	g := grant{path: dir, sid: sid.String(), mask: accessFor(false), persistent: true}
	require.NoError(t, grantAccessPersistent(dir, sid, accessFor(false)))

	// Simulate process restart: forget the in-memory memo entry.
	aclMu.Lock()
	delete(grantedAce, g.key())
	aclMu.Unlock()

	start := time.Now()
	require.NoError(t, grantAccessPersistent(dir, sid, accessFor(false)))
	require.Less(t, time.Since(start), 5*time.Second,
		"memo reuse must skip the kernel crawl")
	require.True(t, hasContainerAce(t, dir, sid))
	require.NoError(t, CleanupContainerGrants(context.Background()))
}

// TestGrantAccessPersistentRecrawlsWhenAceRemoved verifies the memo
// file is only a cache hint: when a third party resets the DACL, the
// DACL probe must force the full crawl again rather than trusting the
// file.
func TestGrantAccessPersistentRecrawlsWhenAceRemoved(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	sid, err := deriveContainerSid("phosphor.sandbox.invalidation")
	require.NoError(t, err)
	defer windows.FreeSid(sid)

	g := grant{path: dir, sid: sid.String(), mask: accessFor(false), persistent: true}
	require.NoError(t, grantAccessPersistent(dir, sid, accessFor(false)))
	require.True(t, hasContainerAce(t, dir, sid))

	// A third party resets the DACL: the memo file still says
	// granted, but the missing ACE must force a re-grant.
	require.NoError(t, revokeAccess(context.Background(), g))
	require.False(t, hasContainerAce(t, dir, sid))

	aclMu.Lock()
	delete(grantedAce, g.key())
	aclMu.Unlock()
	require.NoError(t, grantAccessPersistent(dir, sid, accessFor(false)))
	require.True(t, hasContainerAce(t, dir, sid),
		"missing ACE must force a re-grant despite the memo file")
	require.NoError(t, CleanupContainerGrants(context.Background()))
}

// TestGrantAccessConcurrentRoots verifies independent roots can
// crawl in parallel: the ACL mutex now covers only the DACL merge,
// so concurrent grants on distinct paths must both land their ACEs.
// Run with -race to catch ACL-mutex contract violations.
func TestGrantAccessConcurrentRoots(t *testing.T) {
	dirA := t.TempDir()
	dirB := t.TempDir()
	sid, err := deriveContainerSid("phosphor.sandbox.parallel")
	require.NoError(t, err)
	defer windows.FreeSid(sid)

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, dir := range []string{dirA, dirB} {
		wg.Add(1)
		go func(dir string) {
			defer wg.Done()
			if err := grantAccess(dir, sid, accessFor(false)); err != nil {
				errs <- err
			}
		}(dir)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	require.True(t, hasContainerAce(t, dirA, sid))
	require.True(t, hasContainerAce(t, dirB, sid))
	require.NoError(t, CleanupContainerGrants(context.Background()))
}
