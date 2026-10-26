//go:build windows

package sandbox

import (
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Windows kernel enforcement tiers, selected once per process by
// windowsTierProbe.
type windowsTier int

const (
	// tierRestrictedToken is the v1 backend: restricted token + Job
	// Object. Functionally Codex's unelevated tier.
	tierRestrictedToken windowsTier = iota
	// tierAppContainer is the Windows 11 backend: an MXC-shaped policy
	// enforced on a derived AppContainer identity.
	tierAppContainer
)

// win11MinBuild is the first Windows 11 build (22000). Windows 10 and
// older hosts stay on the restricted-token tier: AppContainer
// capability handling on those builds predates the MXC policy model
// and their DACL semantics differ.
const win11MinBuild = 22000

// hostWindowsBuild returns the real OS build number. RtlGetVersion
// bypasses the manifest-cap that pins GetVersionEx to 9200 for
// unsurfaced applications.
func hostWindowsBuild() uint32 {
	return windows.RtlGetVersion().BuildNumber
}

// procDeriveAppContainerSid resolves
// DeriveAppContainerSidFromAppContainerName, exported from userenv.dll.
var userenv = syscall.NewLazyDLL("userenv.dll")

var procDeriveAppContainerSid = userenv.NewProc("DeriveAppContainerSidFromAppContainerName")

// deriveContainerSid derives the deterministic AppContainer SID for a
// stable container name (same name always yields the same SID, and
// Phosphor derives the name from the workspace root). The caller must
// release the SID with windows.FreeSid.
func deriveContainerSid(name string) (*windows.SID, error) {
	namePtr, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, err
	}
	var sid *windows.SID
	hr, _, _ := procDeriveAppContainerSid.Call(
		uintptr(unsafe.Pointer(namePtr)),
		uintptr(unsafe.Pointer(&sid)),
	)
	if int32(hr) != 0 || sid == nil {
		return nil, syscall.Errno(hr)
	}
	return sid, nil
}

// windowsTierProbe selects the enforcement tier for this host. The
// build check alone is not the capability: a policy-restricted host
// (WDAC, lockdown GPO) can refuse AppContainer derivation, so the
// probe exercises the real primitive with a throwaway name and falls
// back to the restricted-token tier on any failure.
func windowsTierProbe() windowsTier {
	if hostWindowsBuild() < win11MinBuild {
		return tierRestrictedToken
	}
	sid, err := deriveContainerSid("phosphor.sandbox.probe")
	if err != nil {
		return tierRestrictedToken
	}
	windows.FreeSid(sid)
	return tierAppContainer
}

// selectedTier caches the probe per process: the capability is static
// for the host's kernel and does not change mid-session.
var selectedTier = sync.OnceValue(windowsTierProbe)
