//go:build windows

package sandbox

import (
	"fmt"
	"runtime"
	"sync"

	"golang.org/x/sys/windows"
)

// Container SID trustee + ACE helpers for the AppContainer tier.
//
// Grants are ADDITIVE: the container SID is appended to the object's
// existing DACL, so normal users' access is unchanged and the ACE is
// scoped to the container identity only. The same technique is used by
// the Windows-11 MXC backends; cleanup of container ACEs after exit is
// a tracked Phase-2 follow-up.

// grantAccess adds an ACE allowing sid accessMask on path. Existing
// ACEs are preserved (SetEntriesInAclW merges). Objects with a NULL
// DACL (allow-all) keep their semantics; the container grant is added
// on top.
//
// The Get→merge→Set sequence is a read-modify-write on the object's
// DACL, so it runs under aclMu: concurrent launches that share a root
// (e.g. a toolchain cache) must not overwrite each other's ACE.
// Successful grants are memoized — SetNamedSecurityInfo on a directory
// propagates the inheritable ACE to the whole tree, which is expensive
// on large workspaces and would otherwise repeat on every launch.
var (
	aclMu      sync.Mutex
	grantedAce = map[string]struct{}{}
)

func grantAccess(path string, sid *windows.SID, accessMask windows.ACCESS_MASK) error {
	key := path + "\x00" + sid.String() + "\x00" + fmt.Sprint(uint32(accessMask))
	aclMu.Lock()
	defer aclMu.Unlock()
	if _, ok := grantedAce[key]; ok {
		return nil
	}

	trustee := windows.TRUSTEE{
		TrusteeForm:  windows.TRUSTEE_IS_SID,
		TrusteeType:  windows.TRUSTEE_IS_WELL_KNOWN_GROUP,
		TrusteeValue: windows.TrusteeValueFromSID(sid),
	}
	ea := windows.EXPLICIT_ACCESS{
		AccessPermissions: accessMask,
		AccessMode:        windows.GRANT_ACCESS,
		Inheritance:       windows.OBJECT_INHERIT_ACE | windows.CONTAINER_INHERIT_ACE,
		Trustee:           trustee,
	}

	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return fmt.Errorf("get security info %q: %w", path, err)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return fmt.Errorf("read DACL %q: %w", path, err)
	}

	newAcl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{ea}, dacl)
	// The descriptor lives on the Go heap; its DACL pointer may dangle
	// once we stop referencing sd. Pin it across the ACL merge.
	runtime.KeepAlive(sd)
	if err != nil {
		return fmt.Errorf("merge ACE %q: %w", path, err)
	}
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION, nil, nil, newAcl, nil); err != nil {
		return fmt.Errorf("set DACL %q: %w", path, err)
	}
	grantedAce[key] = struct{}{}
	return nil
}

// accessFor returns the access mask for a root of the given kind.
// Read/write roots also carry execute so spawned tools can traverse
// and load their own binaries.
func accessFor(readOnly bool) windows.ACCESS_MASK {
	if readOnly {
		return windows.GENERIC_READ | windows.GENERIC_EXECUTE
	}
	return windows.GENERIC_READ | windows.GENERIC_WRITE | windows.GENERIC_EXECUTE
}
