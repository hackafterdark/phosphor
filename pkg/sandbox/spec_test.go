package sandbox

import (
	"os/exec"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestForBashMode(t *testing.T) {
	t.Parallel()

	require.Nil(t, ForBashMode("", "/ws"))
	require.Nil(t, ForBashMode("off", "/ws"))
	require.Nil(t, ForBashMode("workspace", ""))

	ws := t.TempDir()
	spec := ForBashModeLimits("workspace", ws, ResourceLimits{})
	require.NotNil(t, spec)
	require.Equal(t, ws, spec.WorkspaceRoot)
	require.NotEmpty(t, spec.ReadOnlyRoots)
}

func TestResourceLimitsValidate(t *testing.T) {
	t.Parallel()

	require.NoError(t, ResourceLimits{}.Validate(), "zero limits mean no limit")
	require.NoError(t, ResourceLimits{MaxProcs: minMaxProcs, MaxFileMB: maxMaxFileMB}.Validate())
	require.NoError(t, ResourceLimits{MaxProcs: maxMaxProcs}.Validate())

	require.Error(t, ResourceLimits{MaxProcs: minMaxProcs - 1}.Validate(),
		"max_procs below the floor must refuse exec (invariant 1)")
	require.Error(t, ResourceLimits{MaxProcs: maxMaxProcs + 1}.Validate())
	require.Error(t, ResourceLimits{MaxFileMB: maxMaxFileMB + 1}.Validate())
}

func TestConfineRejectsInvalidLimits(t *testing.T) {
	t.Parallel()

	// Out-of-range limits refuse the exec in both enforced modes,
	// regardless of host availability — validation happens before the
	// platform isolator is consulted so a misconfig can never leave a
	// half-restricted process.
	cmd := exec.Command("true")
	_, err := Confine(cmd, "workspace", Spec{
		WorkspaceRoot: t.TempDir(),
		Limits:        ResourceLimits{MaxProcs: 1},
	})
	require.Error(t, err)

	_, err = Confine(cmd, "strict", Spec{
		WorkspaceRoot: t.TempDir(),
		Limits:        ResourceLimits{MaxProcs: 1},
	})
	require.Error(t, err)
}

func TestWindowsConfineRejectsLimits(t *testing.T) {
	t.Parallel()

	if runtime.GOOS != "windows" {
		t.Skip("windows-only posture check")
	}
	cmd := exec.Command("true")
	cmd.Path = "true.exe"
	// Current() is the windows isolator on this host; rlimits must be
	// refused rather than silently promised.
	err := Current().Confine(cmd, Spec{
		WorkspaceRoot: t.TempDir(),
		Limits:        ResourceLimits{MaxProcs: 128},
	})
	require.Error(t, err, "windows v1 cannot enforce posix rlimits and must refuse rather than promise them")
}

func TestManagedConfineNilSpec(t *testing.T) {
	t.Parallel()

	cmd := exec.Command("true")
	degraded, err := ManagedConfine(cmd, "strict", nil)
	require.NoError(t, err)
	require.False(t, degraded)
}

func TestManagedConfineOffMode(t *testing.T) {
	t.Parallel()

	spec := ForBashModeLimits("workspace", t.TempDir(), ResourceLimits{})
	cmd := exec.Command("true")
	degraded, err := ManagedConfine(cmd, "workspace", spec)
	require.NoError(t, err)
	ok, _ := Available()
	require.Equal(t, !ok, degraded, "workspace mode degrades exactly when the primitives are unavailable")
}

func TestManagedConfineHostBehavior(t *testing.T) {
	t.Parallel()

	spec := ForBashModeLimits("workspace", t.TempDir(), ResourceLimits{})

	// Workspace mode never fails: it either confines or degrades with a
	// visible flag (invariant 1).
	cmd := exec.Command("true")
	degraded, err := ManagedConfine(cmd, "workspace", spec)
	require.NoError(t, err)
	if ok, _ := Available(); !ok {
		require.True(t, degraded, "unavailable host must report degraded")
	}

	// Strict mode either confines or refuses exec outright.
	cmd = exec.Command("true")
	degraded, err = ManagedConfine(cmd, "strict", spec)
	if ok, _ := Available(); !ok {
		require.Error(t, err, "strict mode must refuse exec when unavailable")
		var unavailable *SandboxUnavailableError
		require.ErrorAs(t, err, &unavailable)
	} else {
		require.NoError(t, err)
		require.False(t, degraded)
	}
}
