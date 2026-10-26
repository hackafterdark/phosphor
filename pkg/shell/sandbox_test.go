package shell

import (
	"testing"

	"github.com/hackafterdark/phosphor/pkg/sandbox"
	"github.com/stretchr/testify/require"
)

func TestNewSandboxPolicyDisabled(t *testing.T) {
	t.Parallel()

	for _, mode := range []string{"", "off", "bogus"} {
		require.Nil(t, newSandboxPolicy(mode, t.TempDir(), nil, sandbox.ResourceLimits{}), "mode %q must not build a policy", mode)
	}
}

func TestNewSandboxPolicyRequiresWorkspace(t *testing.T) {
	t.Parallel()

	// Hooks and other trusted surfaces run with no workspace and must
	// stay unsandboxed (invariant 5).
	require.Nil(t, newSandboxPolicy("workspace", "", nil, sandbox.ResourceLimits{}))
	require.Nil(t, newSandboxPolicy("strict", "", nil, sandbox.ResourceLimits{}))
}

func TestNewSandboxPolicyDegradedMatchesAvailability(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	ok, _ := sandbox.Available()

	ws := newSandboxPolicy("workspace", workspace, nil, sandbox.ResourceLimits{})
	require.NotNil(t, ws)
	require.Equal(t, !ok, ws.degraded, "workspace mode degrades exactly when the primitives are unavailable")

	strict := newSandboxPolicy("strict", workspace, nil, sandbox.ResourceLimits{})
	require.NotNil(t, strict)
	require.False(t, strict.degraded, "strict mode refuses exec instead of degrading")
}

func TestSandboxPolicyWorkspaceRoot(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	p := newSandboxPolicy("workspace", workspace, nil, sandbox.ResourceLimits{})
	if p == nil {
		t.Skip("platform sandbox unavailable")
	}
	require.Equal(t, workspace, p.spec.WorkspaceRoot)
}

func TestNewSandboxPolicyCarriesLimits(t *testing.T) {
	t.Parallel()

	limits := sandbox.ResourceLimits{MaxProcs: 128, MaxFileMB: 512}
	p := newSandboxPolicy("workspace", t.TempDir(), nil, limits)
	require.NotNil(t, p)
	require.Equal(t, limits, p.spec.Limits, "limits must reach the spec so Confine validates and applies them")
}
