package dialog

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/hackafterdark/phosphor/internal/ui/common"
	"github.com/hackafterdark/phosphor/internal/ui/list"
	uis "github.com/hackafterdark/phosphor/internal/ui/styles"
	"github.com/hackafterdark/phosphor/internal/workspace"
	"github.com/hackafterdark/phosphor/pkg/config"
	"github.com/stretchr/testify/require"
)

type skillsTestWorkspace struct {
	workspace.Workspace
	cfg *config.Config

	setFields []configFieldCall
	removed   []configFieldCall
}

type configFieldCall struct {
	scope config.Scope
	key   string
	value any
}

func (w *skillsTestWorkspace) Config() *config.Config {
	return w.cfg
}

func (w *skillsTestWorkspace) Resolver() config.VariableResolver {
	return nil
}

func (w *skillsTestWorkspace) WorkingDir() string {
	return ""
}

func (w *skillsTestWorkspace) SetConfigField(scope config.Scope, key string, value any) error {
	w.setFields = append(w.setFields, configFieldCall{scope: scope, key: key, value: value})
	return nil
}

func (w *skillsTestWorkspace) RemoveConfigField(scope config.Scope, key string) error {
	w.removed = append(w.removed, configFieldCall{scope: scope, key: key})
	return nil
}

func newSkillsTestDialog(t *testing.T, ws *skillsTestWorkspace) *Skills {
	t.Helper()
	st := uis.CharmtonePantera()
	return NewSkills(&common.Common{Workspace: ws, Styles: &st})
}

func TestSkillsDialogListsBuiltinSkills(t *testing.T) {
	t.Parallel()

	ws := &skillsTestWorkspace{cfg: &config.Config{}}
	d := newSkillsTestDialog(t, ws)
	require.Equal(t, SkillsID, d.ID())

	items := d.list.FilteredItems()
	require.NotEmpty(t, items)

	for _, item := range items {
		skillItem, ok := item.(*SkillItem)
		require.True(t, ok)
		require.False(t, skillItem.Disabled)
	}
}

func TestSkillsDialogShowsDisabledSkills(t *testing.T) {
	t.Parallel()

	ws := &skillsTestWorkspace{cfg: &config.Config{
		Options: &config.Options{DisabledSkills: []string{"jq"}},
	}}
	d := newSkillsTestDialog(t, ws)

	var found bool
	for _, item := range d.list.FilteredItems() {
		skillItem, ok := item.(*SkillItem)
		require.True(t, ok)
		if skillItem.Name == "jq" {
			found = true
			require.True(t, skillItem.Disabled)
		}
	}
	require.True(t, found, "disabled skill must still be listed")
}

func TestSkillsDialogToggleEmitsAction(t *testing.T) {
	t.Parallel()

	ws := &skillsTestWorkspace{cfg: &config.Config{}}
	d := newSkillsTestDialog(t, ws)

	// Select the first item and toggle it.
	d.list.SetSelected(0)
	selected := d.list.SelectedItem().(*SkillItem)
	name := selected.Name

	action := d.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEnter})
	toggle, ok := action.(ActionToggleSkill)
	require.True(t, ok)
	require.Equal(t, name, toggle.Name)
	require.True(t, toggle.Disable)
	require.True(t, selected.Disabled)

	// Toggling again re-enables it.
	action = d.HandleMsg(tea.KeyPressMsg{Code: tea.KeySpace})
	toggle, ok = action.(ActionToggleSkill)
	require.True(t, ok)
	require.False(t, toggle.Disable)
	require.False(t, selected.Disabled)
}

func TestSkillsDialogClosesOnEscape(t *testing.T) {
	t.Parallel()

	ws := &skillsTestWorkspace{cfg: &config.Config{}}
	d := newSkillsTestDialog(t, ws)
	require.Equal(t, ActionClose{}, d.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEscape}))
}

func TestToggleSkillPersistsDisabledList(t *testing.T) {
	t.Parallel()

	ws := &skillsTestWorkspace{cfg: &config.Config{}}
	require.NoError(t, ToggleSkill(ws, "jq", true))
	require.Len(t, ws.setFields, 1)
	require.Equal(t, "options.disabled_skills", ws.setFields[0].key)
	require.Equal(t, []string{"jq"}, ws.setFields[0].value)
}

func TestToggleSkillAppendsToExistingList(t *testing.T) {
	t.Parallel()

	ws := &skillsTestWorkspace{cfg: &config.Config{
		Options: &config.Options{DisabledSkills: []string{"other"}},
	}}
	require.NoError(t, ToggleSkill(ws, "jq", true))
	require.Len(t, ws.setFields, 1)
	require.Equal(t, []string{"other", "jq"}, ws.setFields[0].value)
}

func TestToggleSkillRemovesEmptyList(t *testing.T) {
	t.Parallel()

	ws := &skillsTestWorkspace{cfg: &config.Config{
		Options: &config.Options{DisabledSkills: []string{"jq"}},
	}}
	require.NoError(t, ToggleSkill(ws, "jq", false))
	require.Empty(t, ws.setFields)
	require.Len(t, ws.removed, 1)
	require.Equal(t, "options.disabled_skills", ws.removed[0].key)
}

func TestToggleSkillNoOpWhenUnchanged(t *testing.T) {
	t.Parallel()

	ws := &skillsTestWorkspace{cfg: &config.Config{}}
	require.NoError(t, ToggleSkill(ws, "jq", false))
	require.Empty(t, ws.setFields)
	require.Empty(t, ws.removed)
}

// Ensure the item type satisfies the filterable list contract.
var _ list.FilterableItem = (*SkillItem)(nil)
