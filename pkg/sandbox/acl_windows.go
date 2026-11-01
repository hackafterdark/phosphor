//go:build windows

package sandbox

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// DACL writes go through SetNamedSecurityInfo, which propagates the
// ACE change through the whole subtree. On a large workspace or a
// toolchain cache (GOMODCACHE-style roots) that crawl can run for
// minutes, so every write is bounded by a deadline: callers waiting
// on the ACL mutex (launches, shutdown) are never trapped behind an
// unbounded kernel walk. The bound is deliberately generous — a
// legitimate crawl that overruns it fails the launch closed rather
// than silently dropping containment, and real hosts have been
// observed crawling cache roots for several minutes.
const (
	// grantDeadline bounds the startup-side tree crawl; exceeding it
	// fails the launch closed rather than stalling forever.
	grantDeadline = 10 * time.Minute
	// revokeDeadline bounds the teardown-side crawl so shutdown can
	// always continue; the grant memo keeps the entry and the next
	// launch re-grants (and re-revokes) the ACE.
	revokeDeadline = 10 * time.Minute
)

// Container SID trustee + ACE helpers for the AppContainer tier.
//
// Grants are ADDITIVE: the container SID is appended to the object's
// existing DACL, so normal users' access is unchanged and the ACE is
// scoped to the container identity only. The same technique is used by
// the Windows-11 MXC backends; unlike them, Phosphor revokes its
// container ACEs on teardown (see CleanupContainerGrants).

// grant is the material needed to undo a grantAccess: the path, the
// trustee SID in string form, and the access mask. The SID is stored
// as a string and reparsed during teardown.
type grant struct {
	path string
	sid  string
	mask windows.ACCESS_MASK
}

// key returns the memoization key for the grant.
func (g grant) key() string {
	return g.path + "\x00" + g.sid + "\x00" + fmt.Sprint(uint32(g.mask))
}

// grantAccess adds an ACE allowing sid accessMask on path. Existing
// ACEs are preserved (SetEntriesInAclW merges). Objects with a NULL
// DACL (allow-all) keep their semantics; the container grant is added
// on top.
//
// The Get→merge→Set sequence is a read-modify-write on the object's
// DACL, so it runs under aclMu: concurrent launches that share a root
// (e.g. a toolchain cache) must not overwrite each other's ACE.
// Successful grants are memoized so the expensive tree crawl does not
// repeat on every launch. The crawl itself is bounded by
// grantDeadline: a workspace whose tree is too slow to crawl in time
// fails the launch instead of parking the caller on the ACL mutex.
var (
	aclMu      sync.Mutex
	grantedAce = map[string]grant{}
)

// setDaclBounded runs the blocking DACL write on its own goroutine and
// waits no longer than ctx allows. If the deadline passes first, the
// syscall keeps running to completion in the background — Windows
// gives no way to cancel it — but the caller is free and the DACL is
// eventually consistent.
func setDaclBounded(ctx context.Context, path string, acl *windows.ACL) error {
	errCh := make(chan error, 1)
	go func() {
		errCh <- windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
			windows.DACL_SECURITY_INFORMATION, nil, nil, acl, nil)
	}()
	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		return fmt.Errorf("set DACL %q: %w", path, ctx.Err())
	}
}

func grantAccess(path string, sid *windows.SID, accessMask windows.ACCESS_MASK) error {
	g := grant{path: path, sid: sid.String(), mask: accessMask}
	key := g.key()
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
	ctx, cancel := context.WithTimeout(context.Background(), grantDeadline)
	defer cancel()
	if err := setDaclBounded(ctx, path, newAcl); err != nil {
		return err
	}
	grantedAce[key] = g
	return nil
}

// CleanupContainerGrants revokes every container-SID ACE this process
// granted, restoring host DACLs (workspace root, toolchain caches,
// launched-binary directories) to their pre-launch state. It is wired
// into the app shutdown path so grants do not linger on host
// directories after Phosphor exits.
//
// The AppContainer profile itself stays registered: it is inert
// without ACEs, costs nothing, and recreating it is cheap.
//
// The revoke of each grant is bounded by revokeDeadline and the whole
// teardown is bounded by ctx: shutdown callers pass a short-lived
// context and are guaranteed to return promptly. Revocations that miss
// their deadline keep their memo entry, so the ACE lingers harmlessly
// and the next launch re-grants (and re-revokes) it.
//
// Multi-instance caveat: if another Phosphor process is confining
// children in the same workspace when this one exits, that child can
// lose file access mid-run and its memoized grant will not re-apply
// until the process relaunches. Concurrent instances over one
// workspace are rare by design (the trust gate, terminal usage), and
// the next launch re-grants, so teardown stays unconditional.
func CleanupContainerGrants(ctx context.Context) error {
	// Hold aclMu for the whole teardown. grantAccess serializes on the
	// same lock, so a new grant for a path can never be skipped on the
	// memo while its revocation is still pending: the grant blocks
	// until cleanup returns, by which point the memo is empty and the
	// ACE is applied for real. The revokes themselves are bounded, so
	// this hold can never outlast revokeDeadline per grant.
	aclMu.Lock()
	defer aclMu.Unlock()

	var errs []error
	for _, g := range grantedAce {
		// Once the caller's deadline passes, stop revoking: each
		// remaining ACE keeps its memo entry and its grant, which is
		// safe (the container is gone) and re-doable next launch.
		if err := ctx.Err(); err != nil {
			return errors.Join(append(errs, err)...)
		}
		// A path deleted mid-session (toolchain caches churn) took
		// its ACE with it; drop the memo and move on. Other stat
		// failures say nothing about the ACE, so keep the memo and
		// report the error instead of guessing.
		if _, err := os.Stat(g.path); err != nil {
			if !os.IsNotExist(err) {
				errs = append(errs, fmt.Errorf("stat %q: %w", g.path, err))
				continue
			}
			delete(grantedAce, g.key())
			continue
		}
		if err := revokeAccess(g); err != nil {
			// A third party that changed the DACL mid-session can
			// refuse the revoke; the ACE then lingers and the next
			// launch re-grants it, so this is logged, not fatal.
			errs = append(errs, err)
			continue
		}
		delete(grantedAce, g.key())
	}
	return errors.Join(errs...)
}

// revokeAccess removes the trustee's access rights recorded in g from
// path's DACL via SetEntriesInAclW's REVOKE_ACCESS mode, which
// deletes matching allow ACEs (both the self and inherit-only halves
// of a split inheritable grant) without touching other trustees.
func revokeAccess(g grant) error {
	sidStr, err := windows.UTF16PtrFromString(g.sid)
	if err != nil {
		return fmt.Errorf("encode sid %q: %w", g.sid, err)
	}
	var sid *windows.SID
	if err := windows.ConvertStringSidToSid(sidStr, &sid); err != nil {
		return fmt.Errorf("parse sid %q: %w", g.sid, err)
	}
	// ConvertStringSidToSidW allocates with LocalAlloc; the SID must
	// be returned with LocalFree, not FreeSid.
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(sid)))

	trustee := windows.TRUSTEE{
		TrusteeForm:  windows.TRUSTEE_IS_SID,
		TrusteeType:  windows.TRUSTEE_IS_WELL_KNOWN_GROUP,
		TrusteeValue: windows.TrusteeValueFromSID(sid),
	}
	ea := windows.EXPLICIT_ACCESS{
		AccessPermissions: g.mask,
		AccessMode:        windows.REVOKE_ACCESS,
		Inheritance:       windows.OBJECT_INHERIT_ACE | windows.CONTAINER_INHERIT_ACE,
		Trustee:           trustee,
	}

	sd, err := windows.GetNamedSecurityInfo(g.path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return fmt.Errorf("get security info %q: %w", g.path, err)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return fmt.Errorf("read DACL %q: %w", g.path, err)
	}

	newAcl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{ea}, dacl)
	// The descriptor lives on the Go heap; its DACL pointer may dangle
	// once we stop referencing sd. Pin it across the ACL merge.
	runtime.KeepAlive(sd)
	if err != nil {
		return fmt.Errorf("revoke ACE %q: %w", g.path, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), revokeDeadline)
	defer cancel()
	return setDaclBounded(ctx, g.path, newAcl)
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
