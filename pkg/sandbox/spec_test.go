package sandbox

import (
	"os/exec"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestForBashMode(t *testing.T) {
	t.Parallel()

	require.Nil(t, ForBashMode("", "/ws"))
	require.Nil(t, ForBashMode("off", "/ws"))
	require.Nil(t, ForBashMode("workspace", ""))

	ws := t.TempDir()
	spec := ForBashMode("workspace", ws)
	require.NotNil(t, spec)
	require.Equal(t, ws, spec.WorkspaceRoot)
	require.NotEmpty(t, spec.ReadOnlyRoots)
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

	spec := ForBashMode("workspace", t.TempDir())
	cmd := exec.Command("true")
	degraded, err := ManagedConfine(cmd, "off", spec)
	require.NoError(t, err)
	require.False(t, degraded)
}

func TestManagedConfineHostBehavior(t *testing.T) {
	t.Parallel()

	spec := ForBashMode("workspace", t.TempDir())

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
