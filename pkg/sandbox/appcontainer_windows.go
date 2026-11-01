//go:build windows

package sandbox

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// AppContainer launch path for the Windows 11 tier. exec.Cmd cannot
// carry PROC_THREAD_ATTRIBUTE_SECURITY_CAPABILITY, so confined children
// are created through this wrapper instead of os/exec.

// securityCapabilities mirrors the SECURITY_CAPABILITIES struct
// (winnt.h): AppContainerSid, then the capability list. x/sys does
// not export it.
type securityCapabilities struct {
	AppContainerSid *windows.SID
	Capabilities    *sidAndAttributes
	CapabilityCount uint32
	Reserved        uint32
}

// sidAndAttributes mirrors SID_AND_ATTRIBUTES (winnt.h) — x/sys does
// not export the struct itself.
type sidAndAttributes struct {
	Sid              *windows.SID
	AccessAttributes uint32
	_pad             uint32
}

// procThreadAttributeSecurityCapability per winbase.h
// (ProcThreadAttributeValue(PROC_THREAD_ATTRIBUTE_SECURITY_CAPABILITIES)).
const procThreadAttributeSecurityCapability = 0x00020009

// procThreadAttributeHandleList whitelist: without it, bInheritHandles
// passes EVERY inheritable handle (including our parent-side pipe
// readers) into the child, and the copy goroutines never see EOF.
// ProcThreadAttributeValue(2, FALSE, TRUE, FALSE) per winbase.h.
const procThreadAttributeHandleList = 0x00020002

// createSuspended (CREATE_SUSPENDED, winbase.h): the child's primary
// thread stays suspended until the containment job is attached, so the
// process never runs a single instruction outside the boundary.
// x/sys does not export the flag.
const createSuspended = 0x00000004

// containerNameFor derives the stable AppContainer profile name for a
// workspace root. The name is deterministic so ACEs granted on host
// directories keep pointing at the same container identity across
// sessions. The hash is truncated because the AppContainer name API
// rejects names beyond ~99 characters.
func containerNameFor(workspace string) string {
	h := sha256.Sum256([]byte(strings.ToLower(workspace)))
	return fmt.Sprintf("phosphor.sandbox.%x", h[:8])
}

// procCreateAppContainerProfile registers (or returns the existing)
// AppContainer profile for a name. The kernel validates capability
// SIDs against registered profiles at launch, so spawning requires
// the profile to exist — Derive alone is pure computation.
var procCreateAppContainerProfile = userenv.NewProc("CreateAppContainerProfile")

// containerSidFor returns the AppContainer SID for a workspace,
// creating the profile on first use and deriving it once it exists.
// The profile persists per-user (same posture as the MXC backends).
func containerSidFor(workspace string) (*windows.SID, error) {
	name := containerNameFor(workspace)
	namePtr, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, err
	}
	display, err := windows.UTF16PtrFromString("Phosphor sandbox container")
	if err != nil {
		return nil, err
	}
	desc, err := windows.UTF16PtrFromString("Phosphor workspace sandbox")
	if err != nil {
		return nil, err
	}
	var sid *windows.SID
	hr, _, _ := procCreateAppContainerProfile.Call(
		uintptr(unsafe.Pointer(namePtr)),
		uintptr(unsafe.Pointer(display)),
		uintptr(unsafe.Pointer(desc)),
		0, // no static capabilities: per-launch grants carry the policy
		0,
		uintptr(unsafe.Pointer(&sid)),
	)
	if int32(hr) == 0 && sid != nil {
		return sid, nil
	}
	// The profile already exists (or the name is claimed): derive the
	// SID for the existing identity.
	return deriveContainerSid(name)
}

// prepareConfinedRoots grants the container SID the access the spec
// describes and returns the capability list for the launch. The
// workspace root is always writable; declared read-only roots are
// granted read+execute.
//
// Roots that do not exist yet (GOCACHE-style caches created lazily by
// the toolchain) are skipped — their absence is not a policy failure,
// and the kernel default-denies the child for that path until the
// directory appears.
//
// System program directories are NOT touched: stock Windows already
// grants ALL APPLICATION PACKAGES read+execute there, and attempting
// a DACL change on them is both refused and (worse) can block on
// Defender tamper protection. Instead, grantLaunchBinary adds the
// launched binary"s own directory per launch.
//
// Grants are additive and shared across launches (the memoized
// grantAccess makes repeated launches free); CleanupContainerGrants
// revokes them on app shutdown so host DACLs return to their
// pre-launch state.
func prepareConfinedRoots(sid *windows.SID, spec Spec) error {
	if err := grantRoot(spec.WorkspaceRoot, sid, accessFor(false)); err != nil {
		return err
	}
	for _, root := range spec.ReadOnlyRoots {
		if err := grantRoot(root, sid, accessFor(true)); err != nil {
			return err
		}
	}
	for _, root := range spec.ReadWriteRoots {
		if err := grantRoot(root, sid, accessFor(false)); err != nil {
			return err
		}
	}
	// DenyRead needs no code here: everything outside the granted
	// roots is default-denied for the container identity.
	return nil
}

// grantRoot applies grantAccess, tolerating roots the toolchain has
// not created yet and roots we do not own: a read-only root that
// refuses the ACE (POSIX-style paths like /dev, foreign-volume
// directories) stays default-denied for the container, which is the
// safe direction — the kernel boundary still holds everywhere else.
func grantRoot(path string, sid *windows.SID, accessMask windows.ACCESS_MASK) error {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	err := grantAccess(path, sid, accessMask)
	if err != nil && errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		return nil
	}
	return err
}

// grantLaunchBinary grants the container read+execute on the launched
// binary"s directory so spawned tools and their sibling DLLs load.
// System directories carry the stock ALL APPLICATION PACKAGES ACE
// already and refuse DACL changes; those failures are expected and
// ignored — the binary is readable anyway. A third-party directory
// that is neither grants access nor has the stock ACE stays
// default-denied, and the launch fails closed.
func grantLaunchBinary(sid *windows.SID, binary string) {
	if !grantsOwnDirectory(binary) {
		return
	}
	_ = grantAccess(filepath.Dir(binary), sid, accessFor(true))
}

// grantsOwnDirectory reports whether binary's directory is one we can
// and should grant on: absolute (a bare or relative name would resolve
// against the parent's cwd, not the binary) and outside %SystemRoot%
// (stock Windows already grants ALL APPLICATION PACKAGES there, and
// touching those DACLs is refused — and can block on Defender tamper
// protection).
func grantsOwnDirectory(binary string) bool {
	if !filepath.IsAbs(binary) {
		return false
	}
	sysRoot := os.Getenv("SystemRoot")
	if sysRoot == "" {
		return true
	}
	dir := filepath.Dir(binary)
	rel, err := filepath.Rel(sysRoot, dir)
	if err != nil {
		return true // different volume: outside the system tree
	}
	if insideRel(rel) {
		return false
	}
	// A mismatch can be a symlink or short-name disguise; resolve both
	// sides once and re-check before granting.
	dirR, err1 := filepath.EvalSymlinks(dir)
	sysR, err2 := filepath.EvalSymlinks(sysRoot)
	if err1 != nil || err2 != nil {
		return true // outside on the fast path, unresolvable: grant
	}
	rel, err = filepath.Rel(sysR, dirR)
	if err != nil {
		return true
	}
	return !insideRel(rel)
}

// insideRel reports whether filepath.Rel's result stays inside the
// base (i.e. is neither "." nor climbs with "..").
func insideRel(rel string) bool {
	return rel != "." && rel != ".." && !strings.HasPrefix(rel, "..\\") &&
		!strings.HasPrefix(rel, "../")
}

// quoteCmdline joins argv into a Windows command line using the
// documented CRT escaping rules (windows.EscapeArg): backslashes
// before a quote are doubled, embedded quotes are backslash-escaped,
// and arguments containing whitespace are wrapped in quotes.
func quoteCmdline(argv []string) string {
	parts := make([]string, len(argv))
	for i, arg := range argv {
		parts[i] = windows.EscapeArg(arg)
	}
	return strings.Join(parts, " ")
}

// envBlock encodes env (KEY=VALUE strings) as the UTF-16,
// NUL-separated, double-NUL-terminated block CreateProcess expects.
func envBlock(env []string) *uint16 {
	if len(env) == 0 {
		return nil
	}
	var buf []uint16
	for _, kv := range env {
		buf = append(buf, windows.StringToUTF16(kv)...)
	}
	buf = append(buf, 0)
	return &buf[0]
}

// childStdio wires parent io streams to inheritable child handles.
// File-backed streams pass their handle directly; anything else is
// bridged through a pipe with a copy goroutine (the goroutine owns
// and closes the parent side).
type childStdio struct {
	in, out, err windows.Handle
	// owned holds the pipe handles we created for this launch. The
	// child receives inheritable copies at CreateProcess; the parent
	// copies must be closed once the kernel has duplicated them, or
	// the output pipes never reach EOF and the copiers hang.
	owned   []windows.Handle
	copiers sync.WaitGroup
}

// closeOwned releases the parent's copies of the launch pipe handles.
// File-backed stdio handles belong to the caller and are left open.
func (st *childStdio) closeOwned() {
	for _, h := range st.owned {
		windows.CloseHandle(h)
	}
	st.owned = nil
}

func newChildStdio(stdin io.Reader, stdout, stderr io.Writer) (*childStdio, error) {
	st := &childStdio{}
	bridge := func(isIn bool, r io.Reader, w io.Writer) (windows.Handle, error) {
		if f, ok := fileHandle(isIn, r, w); ok {
			return f, nil
		}
		var readH, writeH windows.Handle
		if err := windows.CreatePipe(&readH, &writeH, nil, 0); err != nil {
			return 0, err
		}
		// The child receives the stream-appropriate end; the parent
		// side stays non-inheritable and is closed here.
		child, parent := readH, writeH
		if !isIn {
			child, parent = writeH, readH
		}
		if err := windows.SetHandleInformation(child, windows.HANDLE_FLAG_INHERIT, windows.HANDLE_FLAG_INHERIT); err != nil {
			windows.CloseHandle(child)
			windows.CloseHandle(parent)
			return 0, err
		}
		st.owned = append(st.owned, child)
		if isIn {
			if r == nil {
				// No parent stdin: child sees immediate EOF.
				windows.CloseHandle(parent)
				return child, nil
			}
			st.copiers.Add(1)
			go func() {
				defer st.copiers.Done()
				f := os.NewFile(uintptr(parent), "phosphor-sandbox-in")
				_, _ = io.Copy(f, r)
				f.Close()
			}()
		} else {
			st.copiers.Add(1)
			go func() {
				defer st.copiers.Done()
				f := os.NewFile(uintptr(parent), "phosphor-sandbox-out")
				_, _ = io.Copy(w, f)
				f.Close()
			}()
		}
		return child, nil
	}
	var err error
	if st.in, err = bridge(true, stdin, nil); err != nil {
		return nil, err
	}
	if st.out, err = bridge(false, nil, stdout); err != nil {
		return nil, err
	}
	if st.err, err = bridge(false, nil, stderr); err != nil {
		return nil, err
	}
	return st, nil
}

// fileHandle returns the inheritable handle for file-backed stdio.
func fileHandle(isIn bool, r io.Reader, w io.Writer) (windows.Handle, bool) {
	var v any
	if isIn {
		v = r
	} else {
		v = w
	}
	f, ok := v.(*os.File)
	if !ok {
		return 0, false
	}
	h := windows.Handle(f.Fd())
	if err := windows.SetHandleInformation(h, windows.HANDLE_FLAG_INHERIT, windows.HANDLE_FLAG_INHERIT); err != nil {
		return 0, false
	}
	return h, true
}

// ConfinedProcess is a live sandboxed child with the containment job
// attached.
type ConfinedProcess struct {
	proc    *os.Process
	release func()
	copiers *sync.WaitGroup
	once    sync.Once
}

// doRelease closes the containment job exactly once. Kill uses it too:
// a descendant that inherited an output pipe can hold the pipe open
// after the main process dies, and Wait's copier drain would then
// block before its deferred release runs.
func (c *ConfinedProcess) doRelease() { c.once.Do(c.release) }

// Wait blocks until the child exits and its stdio copiers drain, then
// returns the exit code.
func (c *ConfinedProcess) Wait() (int, error) {
	defer c.doRelease()
	state, err := c.proc.Wait()
	c.copiers.Wait()
	if err != nil {
		return -1, err
	}
	if state.ExitCode() != 0 {
		return state.ExitCode(), &exec.ExitError{ProcessState: state}
	}
	return 0, nil
}

// Kill terminates the child; the job's kill-on-close covers descendants.
// The job is released here as well, so cancellation cannot strand the
// child's descendants behind a copier that never sees EOF.
func (c *ConfinedProcess) Kill() error {
	err := c.proc.Kill()
	c.doRelease()
	return err
}

// internetClientCap builds the internetClient capability SID
// (S-1-15-3-1) for network egress. Capability SIDs are synthetic:
// they live under the APPLICATION PACKAGE AUTHORITY and never need to
// exist on disk, so the kernel validates them purely by structure.
func internetClientCap() (*windows.SID, error) {
	var sid *windows.SID
	err := windows.AllocateAndInitializeSid(
		&windows.SidIdentifierAuthority{Value: [6]byte{0, 0, 0, 0, 0, 15}},
		2, // two sub-authorities follow
		3, // capability SIDs
		1, // internetClient
		0, 0, 0, 0, 0, 0,
		&sid,
	)
	if err != nil {
		return nil, err
	}
	return sid, nil
}

// networkCapabilitiesFor maps the spec's network posture onto the
// kernel capability list. The default posture (NetworkAllow, and the
// tool-layer cooperative egress policy of NetworkProxyOnly) keeps
// egress open, matching the tool layer's default-allow behavior;
// NetworkDeny is the only kernel-deny mode.
func networkCapabilitiesFor(spec Spec) ([]sidAndAttributes, func(), error) {
	if spec.Network == NetworkDeny {
		return nil, func() {}, nil
	}
	sid, err := internetClientCap()
	if err != nil {
		return nil, nil, err
	}
	// AccessAttributes 0 marks the capability active (SE_GROUP_ENABLED
	// semantics for capability SIDs).
	return []sidAndAttributes{{Sid: sid}}, func() { windows.FreeSid(sid) }, nil
}

// SpawnConfined launches argv inside the workspace's AppContainer with
// the spec's grants applied and the containment job attached. The
// kernel boundary denies everything outside the granted roots, so
// DenyRead paths need no separate rule.
func SpawnConfined(argv []string, env []string, dir string, spec Spec, stdin io.Reader, stdout, stderr io.Writer) (*ConfinedProcess, error) {
	if len(argv) == 0 {
		return nil, fmt.Errorf("spawn requires argv")
	}
	if spec.WorkspaceRoot == "" {
		return nil, fmt.Errorf("sandbox spec requires a workspace root")
	}
	// exec.Cmd leaves Stdout/Stderr nil when the caller doesn't wire
	// them; the stdio bridge needs a writer to drain the pipes, and
	// io.Discard keeps the child's output flowing (and Wait
	// unblocked) without a destination.
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}
	sid, err := containerSidFor(spec.WorkspaceRoot)
	if err != nil {
		return nil, fmt.Errorf("container sid: %w", err)
	}
	defer windows.FreeSid(sid)

	if err := prepareConfinedRoots(sid, spec); err != nil {
		return nil, err
	}
	grantLaunchBinary(sid, argv[0])

	// SECURITY_CAPABILITIES: run the child in the workspace's
	// AppContainer. The per-file ACE grants carry the filesystem policy;
	// the capability list carries the network posture (internetClient
	// unless the spec denies egress).
	var sc securityCapabilities
	sc.AppContainerSid = sid
	caps, freeCaps, err := networkCapabilitiesFor(spec)
	if err != nil {
		return nil, fmt.Errorf("network capability: %w", err)
	}
	defer freeCaps()
	if len(caps) > 0 {
		sc.Capabilities = &caps[0]
		sc.CapabilityCount = uint32(len(caps))
	}

	al, err := windows.NewProcThreadAttributeList(2)
	if err != nil {
		return nil, fmt.Errorf("attribute list: %w", err)
	}
	defer al.Delete()
	if err := al.Update(procThreadAttributeSecurityCapability,
		unsafe.Pointer(&sc), unsafe.Sizeof(sc)); err != nil {
		return nil, fmt.Errorf("security capability attribute: %w", err)
	}

	stdio, err := newChildStdio(stdin, stdout, stderr)
	if err != nil {
		return nil, fmt.Errorf("stdio: %w", err)
	}
	// Close our copies of the launch pipe handles once the kernel has
	// duplicated them into the child; until then the output copiers
	// would never see EOF and Wait would hang.
	defer stdio.closeOwned()
	handles := []windows.Handle{stdio.in, stdio.out, stdio.err}
	if err := al.Update(procThreadAttributeHandleList,
		unsafe.Pointer(&handles[0]),
		uintptr(len(handles)*int(unsafe.Sizeof(handles[0])))); err != nil {
		return nil, fmt.Errorf("handle list attribute: %w", err)
	}

	si := windows.StartupInfoEx{
		StartupInfo: windows.StartupInfo{
			StdInput:  stdio.in,
			StdOutput: stdio.out,
			StdErr:    stdio.err,
			Flags:     windows.STARTF_USESTDHANDLES,
		},
		ProcThreadAttributeList: al.List(),
	}
	// The kernel reads Cb to distinguish StartupInfo from
	// StartupInfoEx; with EXTENDED_STARTUPINFO_PRESENT it must cover
	// the full extended struct.
	si.StartupInfo.Cb = uint32(unsafe.Sizeof(si))

	var dirPtr *uint16
	if dir != "" {
		dirPtr, err = windows.UTF16PtrFromString(dir)
		if err != nil {
			return nil, err
		}
	}
	cmdLine, err := windows.UTF16PtrFromString(quoteCmdline(argv))
	if err != nil {
		return nil, err
	}

	var pi windows.ProcessInformation
	// EXTENDED_STARTUPINFO_PRESENT: the kernel reads the extended
	// STARTUPINFOEX (attribute list). CREATE_UNICODE_ENVIRONMENT:
	// lpEnvironment is a UTF-16 block. CREATE_SUSPENDED: the primary
	// thread waits until the containment job is attached below.
	// CREATE_NO_WINDOW: the child gets a console with no window, so
	// direct console writes never reach the TUI"s screen buffer.
	// Confined grandchildren inherit that hidden console, which is
	// what keeps cygwin"s early fatal-error path (ssh.exe failing
	// inside the AppContainer, for instance) from painting ghost
	// text over the live UI — the stdio bridge carries everything
	// through pipes regardless.
	if err := windows.CreateProcess(
		nil, cmdLine, nil, nil,
		true, // inherit stdio handles
		windows.EXTENDED_STARTUPINFO_PRESENT|windows.CREATE_UNICODE_ENVIRONMENT|
			createSuspended|windows.CREATE_NO_WINDOW,
		envBlock(env), dirPtr, &si.StartupInfo, &pi,
	); err != nil {
		return nil, fmt.Errorf("create process in appcontainer: %w", err)
	}
	// The attribute list holds raw pointers into sc and handles; keep
	// them alive until the kernel has consumed them.
	runtime.KeepAlive(handles)
	runtime.KeepAlive(sc)

	proc, err := os.FindProcess(int(pi.ProcessId))
	if err != nil {
		// The child exists suspended; tearing down without
		// terminating it would leak an uncontained (if paused)
		// process into the session.
		_ = windows.TerminateProcess(pi.Process, 1)
		windows.CloseHandle(pi.Process)
		return nil, err
	}
	// The os.Process from FindProcess opened its own handle; close the
	// launcher's copy after the job assignment keeps one alive.
	releaseJob, cerr := attachContainmentJob(pi.Process, true)
	if cerr != nil {
		_ = windows.TerminateProcess(pi.Process, 1)
		windows.CloseHandle(pi.Process)
		return nil, cerr
	}
	// Containment is in place: let the child run.
	if _, err := windows.ResumeThread(pi.Thread); err != nil {
		releaseJob()
		_ = windows.TerminateProcess(pi.Process, 1)
		windows.CloseHandle(pi.Process)
		windows.CloseHandle(pi.Thread)
		return nil, fmt.Errorf("resume confined child: %w", err)
	}
	windows.CloseHandle(pi.Thread)
	return &ConfinedProcess{proc: proc, release: func() {
		releaseJob()
		windows.CloseHandle(pi.Process)
	}, copiers: &stdio.copiers}, nil
}
