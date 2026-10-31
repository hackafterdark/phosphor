package httpapi

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/hackafterdark/phosphor/internal/app"
	"github.com/hackafterdark/phosphor/pkg/config"
	"github.com/hackafterdark/phosphor/pkg/db"
	"github.com/stretchr/testify/require"
)

// TestService_StartAdoptsLocalWorkspace verifies that local mode wires
// the TUI's agent runtime into the API backend: the service starts
// with exactly one workspace, the adopted one, instead of spawning a
// duplicate app.App for the same path.
func TestService_StartAdoptsLocalWorkspace(t *testing.T) {
	hostHome := t.TempDir()
	t.Setenv("HOME", hostHome)
	t.Setenv("XDG_CONFIG_HOME", hostHome+"/.config")
	t.Setenv("XDG_DATA_HOME", hostHome+"/.local/share")
	t.Setenv("XDG_CACHE_HOME", hostHome+"/.cache")
	t.Setenv("PHOSPHOR_SKILLS_DIR", t.TempDir())

	wd := t.TempDir()
	cfgStore, err := config.Init(wd, "", false)
	require.NoError(t, err)

	svc := NewService(cfgStore, "tcp", "127.0.0.1:0", wd, nil)
	svc.SetLocalWorkspace(&app.App{})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, svc.Start(ctx))
	t.Cleanup(func() {
		_ = svc.Stop(context.Background())
		// The service holds a long-lived DB handle for mermaid
		// diagram queries; release it so TempDir cleanup can
		// remove the workspace directory on Windows.
		_ = db.Release(filepath.Join(wd, ".phosphor"))
	})

	wsList := svc.srv.Backend().ListWorkspaces()
	require.Len(t, wsList, 1)
	require.Equal(t, wd, wsList[0].Path)
}
