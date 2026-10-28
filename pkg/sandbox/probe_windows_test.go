//go:build windows

package sandbox

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows"
)

func TestHostWindowsBuild(t *testing.T) {
	// Any host Go tests run on is far past the 9200 manifest cap; a
	// build below it means the probe is reading a lying API.
	require.Greater(t, hostWindowsBuild(), uint32(9200))
}

func TestDeriveContainerSid(t *testing.T) {
	sid, err := deriveContainerSid("phosphor.sandbox.test")
	require.NoError(t, err)
	defer windows.FreeSid(sid)

	// AppContainer SIDs carry the APPCONTAINER identifier authority
	// (S-1-15-*).
	require.True(t, strings.HasPrefix(sid.String(), "S-1-15-"),
		"derived SID %s is not an AppContainer SID", sid.String())
}

func TestDeriveContainerSidDeterministic(t *testing.T) {
	first, err := deriveContainerSid("phosphor.sandbox.det")
	require.NoError(t, err)
	defer windows.FreeSid(first)
	second, err := deriveContainerSid("phosphor.sandbox.det")
	require.NoError(t, err)
	defer windows.FreeSid(second)

	require.True(t, windows.EqualSid(first, second))
}

func TestDeriveContainerSidRejectsInvalidName(t *testing.T) {
	// Empty and oversized names must fail closed, not hand back a SID.
	_, err := deriveContainerSid("")
	require.Error(t, err)

	_, err = deriveContainerSid(strings.Repeat("x", 213))
	require.Error(t, err)
}

func TestWindowsTierProbeFallsBack(t *testing.T) {
	// The probe is cached per process, so the fallback path is
	// validated by tier-vs-build consistency: hosts below the Windows
	// 11 threshold must never select the AppContainer tier.
	if hostWindowsBuild() < win11MinBuild {
		require.Equal(t, tierRestrictedToken, selectedTier())
		return
	}
	require.Equal(t, tierAppContainer, selectedTier())
}
