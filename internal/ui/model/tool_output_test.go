package model

import (
	"testing"

	"github.com/hackafterdark/phosphor/internal/ui/common"
	"github.com/hackafterdark/phosphor/internal/ui/list"
	"github.com/hackafterdark/phosphor/internal/workspace"
	"github.com/hackafterdark/phosphor/pkg/config"
	"github.com/stretchr/testify/require"
)

// toolOutputTestWorkspace is a workspace stub whose config is supplied by the
// test, including the nil-pointer shapes a config file can produce.
type toolOutputTestWorkspace struct {
	workspace.Workspace
	cfg *config.Config
}

func (w *toolOutputTestWorkspace) Config() *config.Config { return w.cfg }

func (w *toolOutputTestWorkspace) SetConfigField(config.Scope, string, any) error {
	return nil
}

// newNilTUIConfig returns a config where options.tui is explicitly null, the
// shape produced by unmarshalling `"tui": null` from a config file.
func newNilTUIConfig() *config.Config {
	return &config.Config{Options: &config.Options{}}
}

func TestApplyToolOutputMode_NilTUIOptions(t *testing.T) {
	cfg := newNilTUIConfig()
	ui := &UI{com: &common.Common{
		Workspace: &toolOutputTestWorkspace{cfg: cfg},
	}}
	ui.chat = &Chat{com: ui.com, list: list.NewList()}

	cmd := ui.applyToolOutputMode("", config.ToolOutputFull)
	require.NotNil(t, cmd)
	// The mode must be applied to the freshly created TUI options rather
	// than panicking on the nil pointer.
	require.NotNil(t, cfg.Options.TUI)
	require.Equal(t, config.ToolOutputFull, cfg.Options.TUI.ToolOutput.ModeFor(""))
}

func TestApplyToolOutputLimit_NilTUIOptions(t *testing.T) {
	cfg := newNilTUIConfig()
	ui := &UI{com: &common.Common{
		Workspace: &toolOutputTestWorkspace{cfg: cfg},
	}}
	ui.chat = &Chat{com: ui.com, list: list.NewList()}

	cmd := ui.applyToolOutputLimit(5)
	require.NotNil(t, cmd)
	require.NotNil(t, cfg.Options.TUI)
	require.Equal(t, 5, cfg.Options.TUI.ToolOutput.LineLimit())
}
