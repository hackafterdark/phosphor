package dialog

import (
	"os"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/hackafterdark/phosphor/internal/ui/common"
	"github.com/hackafterdark/phosphor/internal/ui/styles"
	"github.com/hackafterdark/phosphor/pkg/config"
	"github.com/stretchr/testify/require"
)

func TestThemesDialog_ListsAndSelectsTheme(t *testing.T) {
	wsDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(wsDir, ".phosphor", "themes"), 0o755))
	themeYAML := []byte("primary: \"#ff0000\"\n")
	require.NoError(t, os.WriteFile(filepath.Join(wsDir, ".phosphor", "themes", "vampire.yaml"), themeYAML, 0o644))

	ws := &mockWorkspace{
		cfg: &config.Config{Options: &config.Options{TUI: &config.TUIOptions{Theme: "vampire"}}},
		dir: wsDir,
	}
	sty := styles.CharmtonePantera()
	com := &common.Common{Styles: &sty, Workspace: ws}

	d := NewThemes(com)

	// The current theme is selected by default.
	current, ok := d.list.SelectedItem().(*ThemeItem)
	require.True(t, ok)
	require.Equal(t, "vampire", current.name)
	require.True(t, current.current)

	// Built-ins are listed even without theme files.
	found := false
	for _, item := range d.list.FilteredItems() {
		if ti, ok := item.(*ThemeItem); ok && ti.name == "pantera" {
			found = true
		}
	}
	require.True(t, found, "built-in themes must always be selectable")

	// Selecting a theme emits the action with its name.
	for i, item := range d.list.FilteredItems() {
		if ti, ok := item.(*ThemeItem); ok && ti.name == "pantera" {
			d.list.SetSelected(i)
			break
		}
	}
	action := d.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEnter})
	set, ok := action.(ActionSetTheme)
	require.True(t, ok, "selecting a theme must emit ActionSetTheme")
	require.Equal(t, "pantera", set.Name)
}

func TestListThemes_WorkspacePriority(t *testing.T) {
	wsDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(wsDir, ".phosphor", "themes"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(wsDir, ".phosphor", "themes", "vampire.yaml"), []byte("{}"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(wsDir, ".phosphor", "themes", "slay.json"), []byte("{}"), 0o644))

	themes := styles.ListThemes(wsDir)
	byName := map[string]string{}
	for _, th := range themes {
		byName[th.Name] = th.Source
	}
	require.Equal(t, "workspace", byName["vampire"])
	require.Equal(t, "workspace", byName["slay"])
	require.Equal(t, "built-in", byName["pantera"])
	require.Equal(t, "built-in", byName["obsidiana"])
}
