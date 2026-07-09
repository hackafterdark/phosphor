package tui_test

import (
	"context"
	"database/sql"
	"log/slog"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/hackafterdark/phosphor/internal/config"
	"github.com/hackafterdark/phosphor/internal/csync"
	"github.com/hackafterdark/phosphor/internal/platform/tui"
	"github.com/hackafterdark/phosphor/internal/workspace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockWorkspace struct {
	workspace.Workspace
}

func (m *mockWorkspace) Config() *config.Config {
	return &config.Config{
		Options: &config.Options{
			TUI: &config.TUIOptions{},
		},
		Providers: csync.NewMap[string, config.ProviderConfig](),
	}
}

func (m *mockWorkspace) ProjectNeedsInitialization() (bool, error) {
	return false, nil
}

func (m *mockWorkspace) Subscribe(program *tea.Program) {}

func (m *mockWorkspace) WorkingDir() string {
	return "/test"
}

func (m *mockWorkspace) Shutdown() {}

func TestTuiService(t *testing.T) {
	t.Parallel()

	var ws mockWorkspace
	var dbConn *sql.DB

	logger := slog.Default()
	svc := tui.NewService(&ws, dbConn, "test-session", false, logger)

	assert.Equal(t, "tui", svc.Name())
	assert.Equal(t, "Bubble Tea terminal interface", svc.Describe())

	// Start the service.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := svc.Start(ctx)
	require.NoError(t, err)

	// Stop the service.
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer stopCancel()

	err = svc.Stop(stopCtx)
	// Stop might return context.Canceled or nil depending on when s.program exits.
	// Since there is no real terminal attached, we just want to verify it handles Stop without panicking.
	_ = err
}
