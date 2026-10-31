package backend_test

import (
	"testing"

	"github.com/hackafterdark/phosphor/internal/app"
	"github.com/hackafterdark/phosphor/internal/backend"
	"github.com/hackafterdark/phosphor/pkg/config"
	"github.com/stretchr/testify/require"
)

// isolateConfigEnv redirects config.Init's filesystem reads away from
// the host (global config, XDG dirs) so tests stay deterministic.
func isolateConfigEnv(t *testing.T) {
	t.Helper()
	hostHome := t.TempDir()
	t.Setenv("HOME", hostHome)
	t.Setenv("XDG_CONFIG_HOME", hostHome+"/.config")
	t.Setenv("XDG_DATA_HOME", hostHome+"/.local/share")
	t.Setenv("XDG_CACHE_HOME", hostHome+"/.cache")
	t.Setenv("PHOSPHOR_SKILLS_DIR", t.TempDir())
}

// TestBackend_AdoptWorkspace covers the local-mode deduplication path:
// the OpenAI API attaches to the TUI's existing agent runtime instead
// of creating a duplicate app.App for the same path.
func TestBackend_AdoptWorkspace(t *testing.T) {
	isolateConfigEnv(t)

	wd := t.TempDir()
	srvCfg, err := config.Init(wd, "", false)
	require.NoError(t, err)
	b := backend.New(t.Context(), srvCfg, nil)

	ws1, proto1, err := b.AdoptWorkspace(&app.App{}, srvCfg, wd)
	require.NoError(t, err)
	require.Equal(t, wd, proto1.Path)

	// Adopting the same path again returns the existing workspace.
	ws2, _, err := b.AdoptWorkspace(&app.App{}, srvCfg, wd)
	require.NoError(t, err)
	require.Equal(t, ws1.ID, ws2.ID)
	require.Len(t, b.ListWorkspaces(), 1)

	// The backend must not drive the adopted app through its own
	// shutdown: the zero-value app.App would panic if app.Shutdown()
	// were reachable through ShutdownAll.
	b.ShutdownAll()
}

func TestBackend_AdoptWorkspaceGuards(t *testing.T) {
	isolateConfigEnv(t)

	wd := t.TempDir()
	srvCfg, err := config.Init(wd, "", false)
	require.NoError(t, err)
	b := backend.New(t.Context(), srvCfg, nil)

	_, _, err = b.AdoptWorkspace(nil, srvCfg, wd)
	require.Error(t, err)

	_, _, err = b.AdoptWorkspace(&app.App{}, nil, wd)
	require.Error(t, err)
}

// TestBackend_AdoptedWorkspaceSurvivesClaimChurn pins the local-mode
// invariant: client claims on an adopted workspace may come and go,
// but teardown must never remove the host's workspace or fire the
// server shutdown hook while the host app is still running.
func TestBackend_AdoptedWorkspaceSurvivesClaimChurn(t *testing.T) {
	isolateConfigEnv(t)

	wd := t.TempDir()
	srvCfg, err := config.Init(wd, "", false)
	require.NoError(t, err)

	shutdownCalled := false
	b := backend.New(t.Context(), srvCfg, func() { shutdownCalled = true })

	ws, _, err := b.AdoptWorkspace(&app.App{}, srvCfg, wd)
	require.NoError(t, err)

	// client_id must be a UUID: the OpenAI API derives it per session.
	const clientID = "00000000-0000-0000-0000-000000000001"
	require.NoError(t, b.AttachClient(ws.ID, clientID))
	b.DetachClient(ws.ID, clientID)

	require.Len(t, b.ListWorkspaces(), 1)
	require.False(t, shutdownCalled,
		"dropping the last client claim must not shut down the server")
}
