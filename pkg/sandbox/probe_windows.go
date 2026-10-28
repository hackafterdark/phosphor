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

// procDeleteAppContainerProfile removes a throwaway profile again; x/sys
// does not export a wrapper for it.
var procDeleteAppContainerProfile = userenv.NewProc("DeleteAppContainerProfile")

// statusAppContainerAlreadyExists is the STATUS code (and its HRESULT
// form) returned when the profile is already registered; the probe
// treats either as success.
const (
	statusAppContainerAlreadyExists = 0x007A0054
	hresultAlreadyExists            = 0x800700B7
)

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
// probe exercises the real primitives — SID derivation and profile
// registration — with a throwaway name and falls back to the
// restricted-token tier on any failure. Registering the profile is
// what the launch path (containerSidFor) actually does; Derive alone
// is pure computation and can succeed while profile creation is
// blocked.
func windowsTierProbe() windowsTier {
	if hostWindowsBuild() < win11MinBuild {
		return tierRestrictedToken
	}
	sid, err := deriveContainerSid("phosphor.sandbox.probe")
	if err != nil {
		return tierRestrictedToken
	}
	windows.FreeSid(sid)

	// Exercise profile registration, the capability the kernel checks
	// at confined launch. Clean the throwaway profile up on every
	// path that created it.
	created := registerProbeProfile()
	if created {
		namePtr, err := windows.UTF16PtrFromString("phosphor.sandbox.probe")
		if err == nil {
			procDeleteAppContainerProfile.Call(uintptr(unsafe.Pointer(namePtr)))
		}
		return tierAppContainer
	}
	return tierRestrictedToken
}

// registerProbeProfile reports whether the throwaway AppContainer
// profile can be registered, which is the gate for the launch path.
func registerProbeProfile() bool {
	namePtr, err := windows.UTF16PtrFromString("phosphor.sandbox.probe")
	if err != nil {
		return false
	}
	display, err := windows.UTF16PtrFromString("Phosphor sandbox probe")
	if err != nil {
		return false
	}
	desc, err := windows.UTF16PtrFromString("Phosphor capability probe")
	if err != nil {
		return false
	}
	var sid *windows.SID
	hr, _, _ := procCreateAppContainerProfile.Call(
		uintptr(unsafe.Pointer(namePtr)),
		uintptr(unsafe.Pointer(display)),
		uintptr(unsafe.Pointer(desc)),
		0,
		0,
		uintptr(unsafe.Pointer(&sid)),
	)
	if sid != nil {
		windows.FreeSid(sid)
	}
	// The probe may run where the profile is already registered from
	// an earlier session; that still proves the capability.
	return int32(hr) == 0 || uint32(hr) == statusAppContainerAlreadyExists ||
		uint32(hr) == hresultAlreadyExists
}

// selectedTier caches the probe per process: the capability is static
// for the host's kernel and does not change mid-session.
var selectedTier = sync.OnceValue(windowsTierProbe)
