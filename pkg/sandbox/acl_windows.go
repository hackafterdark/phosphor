//go:build windows

package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
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
//
// persistent grants (toolchain caches) are not revoked at teardown:
// their subtree crawl is recorded in a per-container memo file and
// reused by the next session.
type grant struct {
	path       string
	sid        string
	mask       windows.ACCESS_MASK
	persistent bool
}

// key returns the memoization key for the grant.
func (g grant) key() string {
	return g.path + "\x00" + g.sid + "\x00" + fmt.Sprint(uint32(g.mask))
}

// grantAccess adds an ephemeral ACE allowing sid accessMask on path:
// CleanupContainerGrants revokes it at shutdown. Cache-style roots
// that churn rarely (GOCACHE/GOMODCACHE) should use
// grantAccessPersistent instead, so the kernel-side subtree crawl is
// reused across sessions.
//
// The Get→merge half is a read-modify-write on the object's DACL and
// runs under aclMu: concurrent launches that share a root (e.g. a
// toolchain cache) must not overwrite each other's ACE. The kernel
// subtree crawl itself runs without the mutex, so independent roots
// crawl in parallel; two grants racing on the same path serialize
// their read-modify-write through rootWrites, and the later caller
// waits for the in-flight write's result rather than reading a DACL
// the earlier write has not updated yet. Successful grants are
// memoized so the crawl does not repeat on every launch; a memo hit
// is validated against the live DACL so an external reset re-applies
// the grant instead of denying the child. The crawl is bounded by
// grantDeadline: a workspace whose tree is too slow to crawl in time
// fails the launch instead of stalling forever.
var (
	aclMu      sync.Mutex
	grantedAce = map[string]grant{}
	// rootWrites tracks the in-progress DACL read-modify-write per
	// root path. Concurrent grants for the same root — e.g. two
	// workspaces' containers on the shared temp dir — must not
	// interleave their Get/merge/Set steps, or the later write drops
	// the earlier SID's ACE while its memo entry stays behind. The
	// later caller waits for the in-flight write's result instead.
	rootWrites = map[string]*rootWrite{}
)

// rootWrite is one in-progress DACL write for a root. done is closed
// when the write returns, with err holding its result.
type rootWrite struct {
	done chan struct{}
	err  error
}

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

// grantAccessPersistent adds an ACE that survives app shutdown: the
// grant is recorded in a per-container memo file and deliberately not
// revoked by CleanupContainerGrants, so the next session skips the
// kernel-side subtree crawl entirely. The persisted state is only a
// cache: a memo hit is confirmed by reading the root's DACL, and a
// missing ACE (third-party reset, manual cleanup) re-applies the
// grant with the usual kernel crawl.
func grantAccessPersistent(path string, sid *windows.SID, accessMask windows.ACCESS_MASK) error {
	return grantAccessImpl(path, sid, accessMask, true)
}

func grantAccess(path string, sid *windows.SID, accessMask windows.ACCESS_MASK) error {
	return grantAccessImpl(path, sid, accessMask, false)
}

func grantAccessImpl(path string, sid *windows.SID, accessMask windows.ACCESS_MASK, persistent bool) error {
	g := grant{path: path, sid: sid.String(), mask: accessMask, persistent: persistent}
	key := g.key()

	var w *rootWrite
	for {
		aclMu.Lock()
		if existing, ok := grantedAce[key]; ok {
			// The workspace root can be granted both ephemeral and
			// persistent with the same mask; the entry lifetime
			// upgrades monotonically so teardown never skips a
			// persistent grant based on memo-entry ordering.
			if g.persistent && !existing.persistent {
				existing.persistent = true
				grantedAce[key] = existing
				savePersistentGrants()
			}
			// A third party can reset the DACL while the memo entry
			// stays behind; probe the live ACE and re-apply on a
			// stale hit instead of denying the child until restart.
			if sidHasAccess(path, g.sid, g.mask) {
				aclMu.Unlock()
				return nil
			}
			delete(grantedAce, key)
		}
		if persistent && persistedGrantOK(g) {
			grantedAce[key] = g
			aclMu.Unlock()
			slog.Debug("Reused sandbox container ACE grant from memo",
				"path", path, "persistent", true)
			return nil
		}
		if rw, ok := rootWrites[path]; ok {
			// A DACL write for this root is already in flight; wait
			// for its result (bounded like our own crawl would be)
			// and retry the memo path.
			aclMu.Unlock()
			ctx, cancel := context.WithTimeout(context.Background(), grantDeadline)
			werr := waitRootWrite(ctx, rw)
			cancel()
			if werr != nil {
				return werr
			}
			continue
		}
		w = &rootWrite{done: make(chan struct{})}
		rootWrites[path] = w
		aclMu.Unlock()
		break
	}

	// The crawl runs unlocked: independent roots propagate in
	// parallel, and the caller is still bounded by grantDeadline.
	// Crawls are long and invisible when they succeed, so the
	// duration is always logged; it is the primary signal for
	// slow-warmup complaints.
	merged, err := mergeGrantAce(path, sid, accessMask)
	if err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), grantDeadline)
		defer cancel()
		started := time.Now()
		err = setDaclBounded(ctx, path, merged)
		if err != nil {
			// The failure itself is reported by the caller (launch or
			// pre-warm), which decides whether a refused root is
			// tolerated or fatal; only the timing is recorded here.
			slog.Info("Sandbox container ACE crawl failed",
				"path", path, "dur", time.Since(started), "error", err)
		} else {
			slog.Info("Applied sandbox container ACE grant",
				"path", path, "dur", time.Since(started), "persistent", persistent)
		}
	}
	if err == nil {
		aclMu.Lock()
		if prev, ok := grantedAce[key]; ok && prev.persistent {
			// The entry may have been upgraded while the crawl ran.
			g.persistent = true
		}
		grantedAce[key] = g
		if g.persistent {
			savePersistentGrants()
		}
		aclMu.Unlock()
	}

	w.err = err
	aclMu.Lock()
	delete(rootWrites, path)
	aclMu.Unlock()
	close(w.done)
	return err
}

// waitRootWrite waits for an in-flight DACL write on the same root,
// bounded by ctx. A failed write returns its error so the caller
// fails closed the same way the writer did.
func waitRootWrite(ctx context.Context, w *rootWrite) error {
	select {
	case <-w.done:
		return w.err
	case <-ctx.Done():
		return fmt.Errorf("waiting for in-flight DACL write: %w", ctx.Err())
	}
}

// mergeGrantAce builds the DACL for path with sid's accessMask added
// on top of the existing ACEs (SetEntriesInAclW merges; objects with
// a NULL DACL keep their semantics).
func mergeGrantAce(path string, sid *windows.SID, accessMask windows.ACCESS_MASK) (*windows.ACL, error) {
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
		return nil, fmt.Errorf("get security info %q: %w", path, err)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return nil, fmt.Errorf("read DACL %q: %w", path, err)
	}

	newAcl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{ea}, dacl)
	// The descriptor lives on the Go heap; its DACL pointer may dangle
	// once we stop referencing sd. Pin it across the ACL merge.
	runtime.KeepAlive(sd)
	if err != nil {
		return nil, fmt.Errorf("merge ACE %q: %w", path, err)
	}
	return newAcl, nil
}

// persistedGrant is one entry of the on-disk grant memo. The trustee
// SID is the memo file's name, so it is not repeated per entry.
type persistedGrant struct {
	Path string              `json:"path"`
	Mask windows.ACCESS_MASK `json:"mask"`
}

// persistentGrantFile returns the per-container memo file listing the
// grants that survive app shutdown. It lives outside the workspace
// (the crawled cache roots are host-wide) under the user cache
// directory, keyed by the container SID.
func persistentGrantFile(sid string) string {
	cache := os.Getenv("XDG_CACHE_HOME")
	if cache == "" {
		if dir, err := os.UserCacheDir(); err == nil {
			cache = dir
		}
	}
	if cache == "" {
		return ""
	}
	return filepath.Join(cache, "phosphor", "sandbox-grants", sid+".json")
}

// persistedGrantOK reports whether the memo file records g and the
// root object still carries the trustee's ACE. The file is only a
// cache hint; the DACL check is the source of truth, so a third-party
// ACL reset forces the full crawl again. Reading the root descriptor
// is cheap — the expensive part is the subtree propagation, which the
// kernel already completed when the ACE was applied.
func persistedGrantOK(g grant) bool {
	file := persistentGrantFile(g.sid)
	if file == "" {
		return false
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return false
	}
	var entries []persistedGrant
	if err := json.Unmarshal(data, &entries); err != nil {
		return false
	}
	for _, e := range entries {
		if e.Path == g.path && e.Mask == g.mask {
			return sidHasAccess(g.path, g.sid, g.mask)
		}
	}
	return false
}

// savePersistentGrants (re)writes the memo files for the persistent
// grants known to this process. Must be called with aclMu held; I/O
// failures degrade to the next session re-crawling, so they are
// ignored.
func savePersistentGrants() {
	bySid := map[string][]persistedGrant{}
	for _, g := range grantedAce {
		if !g.persistent {
			continue
		}
		bySid[g.sid] = append(bySid[g.sid], persistedGrant{Path: g.path, Mask: g.mask})
	}
	for sid, entries := range bySid {
		file := persistentGrantFile(sid)
		if file == "" {
			continue
		}
		data, err := json.Marshal(entries)
		if err != nil {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			continue
		}
		_ = os.WriteFile(file, data, 0o644)
	}
}

// sidHasAccess reports whether path's DACL carries an allow ACE for
// the trustee SID that covers accessMask. The check reads only the
// root descriptor; it is the cheap source-of-truth probe for memo
// reuse. The stored ACE may hold either the generic or the mapped
// file-specific spelling of the requested rights, so both are
// compared after mapping generic rights to specific rights.
func sidHasAccess(path, sid string, accessMask windows.ACCESS_MASK) bool {
	sidPtr, err := windows.UTF16PtrFromString(sid)
	if err != nil {
		return false
	}
	var parsed *windows.SID
	if err := windows.ConvertStringSidToSid(sidPtr, &parsed); err != nil {
		return false
	}
	// ConvertStringSidToSidW allocates with LocalAlloc; the SID must
	// be returned with LocalFree, not FreeSid.
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(parsed)))

	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return false
	}
	dacl, _, err := sd.DACL()
	if err != nil || dacl == nil {
		return false
	}
	for i := uint32(0); i < uint32(dacl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil {
			continue
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			continue
		}
		aceSid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if windows.EqualSid(aceSid, parsed) &&
			accessCovers(windows.ACCESS_MASK(ace.Mask), accessMask) {
			return true
		}
	}
	return false
}

// genericToSpecific maps each GENERIC_* right in mask to the
// file-specific rights it stands for at access-check time (the
// FILE_GENERIC_* macros of the Windows headers). ACEs written by
// SetEntriesInAcl keep the generic bits, but a third-party tool may
// have written the expanded spelling instead; both spellings are
// equivalent for the kernel's access check.
func genericToSpecific(mask windows.ACCESS_MASK) windows.ACCESS_MASK {
	mapped := mask
	if mask&windows.GENERIC_READ != 0 {
		mapped |= 0x00120089 // FILE_GENERIC_READ
	}
	if mask&windows.GENERIC_WRITE != 0 {
		mapped |= 0x00120116 // FILE_GENERIC_WRITE
	}
	if mask&windows.GENERIC_EXECUTE != 0 {
		mapped |= 0x001200A9 // FILE_GENERIC_EXECUTE
	}
	if mask&windows.GENERIC_ALL != 0 {
		mapped |= 0x001F01FF // FILE_ALL_ACCESS
	}
	return mapped
}

// accessCovers reports whether an ACE mask grants at least the
// requested access, after mapping generic rights to specific rights
// so either spelling of the grant satisfies the check.
func accessCovers(aceMask, requested windows.ACCESS_MASK) bool {
	need := genericToSpecific(requested)
	have := genericToSpecific(aceMask)
	return have&need == need
}

// CleanupContainerGrants revokes every ephemeral container-SID ACE
// this process granted, restoring host DACLs (workspace root,
// launched-binary directories) to their pre-launch state. It is wired
// into the app shutdown path so grants do not linger on host
// directories after Phosphor exits.
//
// Persistent grants (toolchain caches, see grantAccessPersistent) are
// intentionally left behind: revoking them would force the next
// session to re-crawl the same subtrees. The lingering ACE is inert —
// only this workspace's container can hold that SID — and the memo
// file records it so the next session skips the crawl after verifying
// the root ACE is still present.
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
	// Bound the wait for aclMu by the caller's deadline: if grantAccess
	// holds the mutex in a DACL write that is still crawling, shutdown
	// returns on its deadline instead of inheriting the write's (much
	// longer) bound. Poll with TryLock — abandoning a Lock waiter
	// cannot cancel the wait, and unlocking from the waiter goroutine
	// races a third party acquiring the mutex in between. The teardown
	// never unlinks or resets anything, so abandoning it is safe:
	// unrevoked ACEs stay granted (the container is gone) and are
	// revoked at the next launch's teardown.
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if aclMu.TryLock() {
			defer aclMu.Unlock()
			break
		}
		timer := time.NewTimer(50 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}

	var errs []error
	for _, g := range grantedAce {
		// Persistent grants are reused by the next session; leave
		// the ACE, the memo entry, and the memo file untouched.
		if g.persistent {
			continue
		}
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
		if err := revokeAccess(ctx, g); err != nil {
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
// The per-write deadline is derived from cleanupCtx, so a revoke can
// never outlive the teardown's own bound.
func revokeAccess(cleanupCtx context.Context, g grant) error {
	if err := cleanupCtx.Err(); err != nil {
		return err
	}
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
	timeout := revokeDeadline
	if dl, ok := cleanupCtx.Deadline(); ok {
		if remaining := time.Until(dl); remaining < timeout {
			timeout = remaining
		}
	}
	ctx, cancel := context.WithTimeout(cleanupCtx, timeout)
	defer cancel()
	return setDaclBounded(ctx, g.path, newAcl)
}

// fileDeleteChild is ACCESS_MASK.FILE_DELETE_CHILD; x/sys does not
// export the constant.
const fileDeleteChild = 0x00000040

// accessFor returns the access mask for a root of the given kind.
// Read/write roots also carry execute so spawned tools can traverse
// and load their own binaries.
//
// The writable mask carries DELETE and FILE_DELETE_CHILD explicitly:
// GENERIC_WRITE alone leaves confined children unable to unlink or
// rename their own scratch files (git's config.lock/index.lock/
// tmp_obj_* dance fails closed with "unable to unlink: Invalid
// argument"), because directory delete access lives on the parent
// object, not the file.
func accessFor(readOnly bool) windows.ACCESS_MASK {
	if readOnly {
		return windows.GENERIC_READ | windows.GENERIC_EXECUTE
	}
	return windows.GENERIC_READ | windows.GENERIC_WRITE | windows.GENERIC_EXECUTE |
		windows.DELETE | fileDeleteChild
}
