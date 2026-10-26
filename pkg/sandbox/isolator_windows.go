//go:build windows

package sandbox

import (
	"fmt"
	"os/exec"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Windows v1 sandbox posture (plan §3): restricted token + Job Objects.
//
//   - The restricted token strips elevation privileges and runs the child
//     filtered (UAC-limited). Stdio and workspace writes keep working —
//     verified on-host (Phase 3 probe).
//   - The per-command Job Object (see contain_windows.go) provides
//     kill-on-close cleanup, an active-process cap, and a per-process
//     memory cap.
//   - There is NO kernel-enforced workspace FS boundary on Windows v1:
//     low-IL confinement was tested and rejected (SetTokenInformation
//     returns ERROR_BAD_LENGTH on this host, and mandatory integrity
//     control would deny workspace writes too, since the workspace
//     carries the caller's medium label). The FS boundary arrives with
//     the AppContainer phase; until then workspace confinement stays
//     in-process and Available() reports that honestly.
const (
	disableMaxPrivilege = 0x00000001
	limitedToken        = 0x00000002
)

var (
	advapi32             = syscall.NewLazyDLL("advapi32.dll")
	procCreateRestricted = advapi32.NewProc("CreateRestrictedToken")
)

// Current returns the platform isolator.
func Current() Isolator { return windowsIsolator{} }

// Windows returns the restricted-token isolator.
func Windows() Isolator { return windowsIsolator{} }

type windowsIsolator struct{}

// restrictedToken is created once per Phosphor process and reused for
// every confined child; the token is immutable, so sharing is safe and
// avoids leaking one handle per command.
var (
	restrictedOnce  sync.Once
	restrictedToken windows.Token
	restrictedErr   error
)

func sharedRestrictedToken() (windows.Token, error) {
	restrictedOnce.Do(func() {
		var token windows.Token
		if err := windows.OpenProcessToken(windows.CurrentProcess(),
			windows.TOKEN_QUERY|windows.TOKEN_DUPLICATE|windows.TOKEN_ASSIGN_PRIMARY,
			&token); err != nil {
			restrictedErr = err
			return
		}
		var restricted windows.Token
		r1, _, err := procCreateRestricted.Call(
			uintptr(token), uintptr(disableMaxPrivilege|limitedToken),
			0, 0, // sidsAndPrivileges: NULL, 0
			0, 0, // privileges: NULL, 0
			0, 0, // restrictedSids: NULL, 0
			uintptr(unsafe.Pointer(&restricted)),
		)
		if r1 == 0 {
			restrictedErr = err
			return
		}
		restrictedToken = restricted
	})
	return restrictedToken, restrictedErr
}

func (windowsIsolator) Confine(cmd *exec.Cmd, spec Spec) error {
	if spec.WorkspaceRoot == "" {
		return fmt.Errorf("sandbox spec requires a workspace root")
	}
	if cmd.Path == "" {
		return fmt.Errorf("sandbox confine requires cmd.Path")
	}
	if !spec.Limits.Empty() {
		// POSIX rlimits have no Windows equivalent; the Job Object caps
		// (active-process, per-process memory) are applied separately in
		// contain_windows.go and are not configurable per spec. Refuse
		// rather than promise caps this host cannot enforce (invariant 1).
		return fmt.Errorf("sandbox resource limits are POSIX-only and cannot be enforced on Windows")
	}
	// DenyRead cannot be enforced with a restricted token (same limitation
	// as Codex's unelevated tier); the in-process pathguard gates those
	// paths until the AppContainer backend lands. The spec is still
	// carried so both layers agree on the secret set.
	token, err := sharedRestrictedToken()
	if err != nil {
		return fmt.Errorf("restricted token: %w", err)
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Token = syscall.Token(token)
	return nil
}

func (windowsIsolator) Available() (bool, string) {
	if _, err := sharedRestrictedToken(); err != nil {
		return false, "restricted token unavailable: " + err.Error()
	}
	if selectedTier() == tierAppContainer {
		return true, "windows v2: AppContainer workspace FS boundary kernel-enforced; " +
			"restricted token + job containment for the fallback tier"
	}
	return false, "windows v1: restricted-token + job containment kernel-enforced; workspace FS boundary advisory until AppContainer phase"
}
