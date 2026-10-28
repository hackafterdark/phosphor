//go:build windows

package sandbox

import (
	"testing"
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
