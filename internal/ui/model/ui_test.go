package model

import (
	"context"
	"testing"

	"charm.land/catwalk/pkg/catwalk"
	"charm.land/lipgloss/v2"
	"github.com/hackafterdark/phosphor/internal/config"
	"github.com/hackafterdark/phosphor/internal/csync"
	"github.com/hackafterdark/phosphor/internal/message"
	"github.com/hackafterdark/phosphor/internal/session"
	"github.com/hackafterdark/phosphor/internal/ui/common"
	"github.com/hackafterdark/phosphor/internal/ui/completions"
	"github.com/hackafterdark/phosphor/internal/ui/dialog"
	uistyles "github.com/hackafterdark/phosphor/internal/ui/styles"
	"github.com/hackafterdark/phosphor/internal/workspace"
	"github.com/stretchr/testify/require"
)

func TestCurrentModelSupportsImages(t *testing.T) {
	t.Parallel()

	t.Run("returns false when config is nil", func(t *testing.T) {
		t.Parallel()

		ui := newTestUIWithConfig(t, nil)
		require.False(t, ui.currentModelSupportsImages())
	})

	t.Run("returns false when coder agent is missing", func(t *testing.T) {
		t.Parallel()

		cfg := &config.Config{
			Providers: csync.NewMap[string, config.ProviderConfig](),
			Agents:    map[string]config.Agent{},
		}
		ui := newTestUIWithConfig(t, cfg)
		require.False(t, ui.currentModelSupportsImages())
	})

	t.Run("returns false when model is not found", func(t *testing.T) {
		t.Parallel()

		cfg := &config.Config{
			Providers: csync.NewMap[string, config.ProviderConfig](),
			Agents: map[string]config.Agent{
				config.AgentCoder: {Model: config.SelectedModelTypeLarge},
			},
		}
		ui := newTestUIWithConfig(t, cfg)
		require.False(t, ui.currentModelSupportsImages())
	})

	t.Run("returns true when current model supports images", func(t *testing.T) {
		t.Parallel()

		providers := csync.NewMap[string, config.ProviderConfig]()
		providers.Set("test-provider", config.ProviderConfig{
			ID: "test-provider",
			Models: []catwalk.Model{
				{ID: "test-model", SupportsImages: true},
			},
		})

		cfg := &config.Config{
			Models: map[config.SelectedModelType]config.SelectedModel{
				config.SelectedModelTypeLarge: {
					Provider: "test-provider",
					Model:    "test-model",
				},
			},
			Providers: providers,
			Agents: map[string]config.Agent{
				config.AgentCoder: {Model: config.SelectedModelTypeLarge},
			},
		}

		ui := newTestUIWithConfig(t, cfg)
		require.True(t, ui.currentModelSupportsImages())
	})
}

func TestUI_HandleSlashCommand_SanitizesState(t *testing.T) {
	t.Parallel()

	tw := &testWorkspace{}
	st := uistyles.CharmtonePantera()
	ui := &UI{
		com: &common.Common{
			Workspace: tw,
			Styles:    &st,
		},
	}
	ui.registerSlashCommands()

	ui.dialog = dialog.NewOverlay()
	ui.slashMode = true
	ui.completionsOpen = true
	ui.completions = completions.New(lipgloss.NewStyle(), lipgloss.NewStyle(), lipgloss.NewStyle())

	// Call handleSlashCommand with a valid slash command.
	ui.handleSlashCommand("/menu")

	// Verify that slashMode is now false, and completions are closed (completionsOpen is false).
	require.False(t, ui.slashMode)
	require.False(t, ui.completionsOpen)
}

func TestUI_HandleSlashCommand_StatsOpensDialog(t *testing.T) {
	t.Parallel()

	tw := &testWorkspace{}
	st := uistyles.CharmtonePantera()
	ui := &UI{
		com: &common.Common{
			Workspace: tw,
			Styles:    &st,
		},
	}
	ui.registerSlashCommands()

	ui.dialog = dialog.NewOverlay()
	ui.slashMode = true
	ui.completionsOpen = true
	ui.completions = completions.New(lipgloss.NewStyle(), lipgloss.NewStyle(), lipgloss.NewStyle())

	// Call handleSlashCommand with /stats.
	cmd := ui.handleSlashCommand("/stats")
	require.NotNil(t, cmd)

	// Verify that the dialog overlay now contains the usage stats dialog.
	require.True(t, ui.dialog.ContainsDialog(dialog.UsageID))
	require.False(t, ui.slashMode)
	require.False(t, ui.completionsOpen)
}

func newTestUIWithConfig(t *testing.T, cfg *config.Config) *UI {
	t.Helper()

	return &UI{
		com: &common.Common{
			Workspace: &testWorkspace{cfg: cfg},
		},
	}
}

// testWorkspace is a minimal [workspace.Workspace] stub for unit tests.
type testWorkspace struct {
	workspace.Workspace
	cfg          *config.Config
	agentIsReady bool
	runCalled    bool
	lastPrompt   string
}

func (w *testWorkspace) Config() *config.Config {
	return w.cfg
}

func (w *testWorkspace) AgentIsReady() bool {
	return w.agentIsReady
}

func (w *testWorkspace) AgentRun(ctx context.Context, sessionID, prompt string, attachments ...message.Attachment) error {
	w.runCalled = true
	w.lastPrompt = prompt
	return nil
}

func TestUI_HandleSlashCommand_LearnSubmitsMessage(t *testing.T) {
	t.Parallel()

	tw := &testWorkspace{
		agentIsReady: true,
	}
	st := uistyles.CharmtonePantera()
	ui := &UI{
		com: &common.Common{
			Workspace: tw,
			Styles:    &st,
		},
	}
	ui.registerSlashCommands()

	// Setup active session so sendMessage doesn't try to call CreateSession.
	ui.session = &session.Session{ID: "test-session-123"}

	ui.dialog = dialog.NewOverlay()
	ui.slashMode = true
	ui.completionsOpen = true
	ui.completions = completions.New(lipgloss.NewStyle(), lipgloss.NewStyle(), lipgloss.NewStyle())

	// Call handleSlashCommand with /learn and a URL.
	cmd := ui.handleSlashCommand("/learn https://example.com/docs")
	require.NotNil(t, cmd)

	// Verify state sanitization.
	require.False(t, ui.slashMode)
	require.False(t, ui.completionsOpen)

	// Call handleSlashCommand with /learn and no arguments (should warn).
	ui.slashMode = true
	cmdWarn := ui.handleSlashCommand("/learn")
	require.NotNil(t, cmdWarn)
}
