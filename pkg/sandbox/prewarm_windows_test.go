//go:build windows

package sandbox

import (
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows"
)

func TestPreWarmAppliesGrants(t *testing.T) {
	dir := t.TempDir()
	sid, err := containerSidFor(dir)
	require.NoError(t, err)
	defer windows.FreeSid(sid)

	// The workspace-root grant tolerates hosts that refuse DACL edits
	// on scratch directories (mirrors grantRoot's tolerance contract).
	if err := grantAccess(dir, sid, accessFor(false)); err != nil {
		t.Skipf("host cannot grant ACEs on scratch workspaces: %v", err)
	}

	spec := Spec{WorkspaceRoot: dir}
	require.NoError(t, preWarmGrants(dir, spec))
	require.True(t, hasContainerAce(t, dir, sid),
		"container ACE missing after pre-warm")
}

func TestPreWarmSkipsWhenDisabled(t *testing.T) {
	dir := t.TempDir()
	require.False(t, PreWarm("off", dir, ResourceLimits{}),
		"pre-warm must not start with sandboxing off")
	require.False(t, PreWarm("workspace", "", ResourceLimits{}),
		"pre-warm must not start without a workspace")
}
