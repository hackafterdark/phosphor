package shell

import (
	"testing"

	"github.com/hackafterdark/phosphor/pkg/sandbox"
	"github.com/stretchr/testify/require"
)

func TestNewSandboxPolicyDisabled(t *testing.T) {
	t.Parallel()

	for _, mode := range []string{"", "off", "bogus"} {
		require.Nil(t, newSandboxPolicy(mode, t.TempDir(), nil), "mode %q must not build a policy", mode)
	}
}

func TestNewSandboxPolicyRequiresWorkspace(t *testing.T) {
	t.Parallel()

	// Hooks and other trusted surfaces run with no workspace and must
	// stay unsandboxed (invariant 5).
	require.Nil(t, newSandboxPolicy("workspace", "", nil))
	require.Nil(t, newSandboxPolicy("strict", "", nil))
}

func TestNewSandboxPolicyDegradedMatchesAvailability(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	ok, _ := sandbox.Available()

	ws := newSandboxPolicy("workspace", workspace, nil)
	require.NotNil(t, ws)
	require.Equal(t, !ok, ws.degraded, "workspace mode degrades exactly when the primitives are unavailable")

	strict := newSandboxPolicy("strict", workspace, nil)
	require.NotNil(t, strict)
	require.False(t, strict.degraded, "strict mode refuses exec instead of degrading")
}

func TestSandboxPolicyWorkspaceRoot(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	p := newSandboxPolicy("workspace", workspace, nil)
	if p == nil {
		t.Skip("platform sandbox unavailable")
	}
	require.Equal(t, workspace, p.spec.WorkspaceRoot)
}
