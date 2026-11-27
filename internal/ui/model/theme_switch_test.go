package model

import (
	"testing"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"github.com/hackafterdark/phosphor/internal/ui/attachments"
	"github.com/hackafterdark/phosphor/internal/ui/completions"
	"github.com/hackafterdark/phosphor/internal/ui/dialog"
	"github.com/hackafterdark/phosphor/internal/workspace"
	"github.com/hackafterdark/phosphor/pkg/config"
	"github.com/stretchr/testify/require"
)

// themeSwitchWorkspace is a workspace stub that accepts config writes
// without touching disk.
type themeSwitchWorkspace struct {
	workspace.Workspace
	cfg *config.Config
}

func (w *themeSwitchWorkspace) Config() *config.Config { return w.cfg }

func (w *themeSwitchWorkspace) WorkingDir() string { return "" }

func (w *themeSwitchWorkspace) SetConfigField(config.Scope, string, any) error {
	return nil
}

// TestActionSetTheme_AppliesLive walks the real dialog-action path: opening
// the themes dialog and dispatching ActionSetTheme must persist the choice
// and swap the live styles without a restart.
func TestActionSetTheme_AppliesLive(t *testing.T) {
	u := newTestUI()
	u.com.Workspace = &themeSwitchWorkspace{
		cfg: &config.Config{Options: &config.Options{}},
	}
	u.dialog = dialog.NewOverlay()
	u.header = newHeader(u.com)
	u.completions = completions.New(
		u.com.Styles.Completions.Normal,
		u.com.Styles.Completions.Focused,
		u.com.Styles.Completions.Match,
	)
	u.attachments = attachments.New(
		attachments.NewRenderer(
			u.com.Styles.Attachments.Normal,
			u.com.Styles.Attachments.Deleting,
			u.com.Styles.Attachments.Image,
			u.com.Styles.Attachments.Text,
			u.com.Styles.Attachments.Skill,
		),
		attachments.Keymap{},
	)
	u.todoSpinner = spinner.New()

	u.openDialog(dialog.ThemesID)
	require.True(t, u.dialog.ContainsDialog(dialog.ThemesID))

	before := u.com.Styles.Header.LogoGradFromColor

	// Filter the list down to the bundled synthwave theme and select it,
	// exercising the same routing the live TUI uses (keypress -> dialog
	// action -> model handler).
	for _, r := range "synthwave" {
		u.handleDialogMsg(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	u.handleDialogMsg(tea.KeyPressMsg{Code: tea.KeyEnter})

	require.Equal(t, "synthwave", u.com.Config().Options.TUI.Theme)
	require.NotEqual(t, before, u.com.Styles.Header.LogoGradFromColor,
		"live styles must change when a theme is selected")
	require.False(t, u.dialog.ContainsDialog(dialog.ThemesID),
		"themes dialog must close after selecting a theme")
}
