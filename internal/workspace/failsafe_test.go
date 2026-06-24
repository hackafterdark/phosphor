package workspace

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestClientWorkspace_AgentRun_Failsafe(t *testing.T) {
	t.Parallel()

	ws := &ClientWorkspace{}
	err := ws.AgentRun(context.Background(), "session-1", "/menu")
	require.Error(t, err)
	require.Contains(t, err.Error(), "blocked: cannot send slash command")
}

func TestAppWorkspace_AgentRun_Failsafe(t *testing.T) {
	t.Parallel()

	ws := &AppWorkspace{}
	err := ws.AgentRun(context.Background(), "session-1", "/menu")
	require.Error(t, err)
	require.Contains(t, err.Error(), "blocked: cannot send slash command")
}
