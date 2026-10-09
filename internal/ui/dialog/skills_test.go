package dialog

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/hackafterdark/phosphor/internal/ui/common"
	"github.com/hackafterdark/phosphor/internal/ui/list"
	uis "github.com/hackafterdark/phosphor/internal/ui/styles"
	"github.com/hackafterdark/phosphor/internal/workspace"
	"github.com/hackafterdark/phosphor/pkg/config"
	"github.com/hackafterdark/phosphor/pkg/skills"
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

func TestSkillItemRenderFitsWidth(t *testing.T) {
	t.Parallel()

	st := uis.CharmtonePantera()
	item := &SkillItem{
		Versioned:   list.NewVersioned(),
		Name:        "long-desc-skill",
		Description: "Line one of the description.\n" + strings.Repeat("x", 900) + " trailing text",
		Source:      skills.SourceUser,
		t:           &st,
	}

	out := item.Render(60)
	// The row must stay on a single line no matter how long the
	// description is; the item style may add its own padding on top.
	require.Equal(t, 1, lipgloss.Height(out), "rendered item must be a single line")
	require.Contains(t, out, "long-desc-skill")
	require.Contains(t, out, "…", "long description must be truncated")
	require.NotContains(t, out, strings.Repeat("x", 900))
}

func TestSkillsDialogDetailsOpensDetail(t *testing.T) {
	t.Parallel()

	ws := &skillsTestWorkspace{cfg: &config.Config{}}
	d := newSkillsTestDialog(t, ws)

	d.list.SetSelected(0)
	action := d.HandleMsg(tea.KeyPressMsg{Code: 'd', Text: "d"})
	detail, ok := action.(ActionShowSkillDetail)
	require.True(t, ok)
	require.NotNil(t, detail.Skill)

	// The detail dialog loads the skill file and keeps its ID.
	st := uis.CharmtonePantera()
	sd := NewSkillDetail(&common.Common{Workspace: ws, Styles: &st}, detail.Skill, detail.Source)
	require.Equal(t, SkillDetailID, sd.ID())
}

func TestSkillDetailScrollAndClose(t *testing.T) {
	t.Parallel()

	ws := &skillsTestWorkspace{cfg: &config.Config{}}
	st := uis.CharmtonePantera()
	com := &common.Common{Workspace: ws, Styles: &st}

	// A builtin skill is guaranteed to exist and to have content.
	builtin := skills.DiscoverBuiltin()
	require.NotEmpty(t, builtin)
	sd := NewSkillDetail(com, builtin[0], skills.SourceSystem)
	content, err := SkillFileContent(builtin[0])
	require.NoError(t, err)
	require.NotEmpty(t, content)
	require.Nil(t, sd.HandleMsg(SkillDetailLoadedMsg{Content: content}))
	require.False(t, sd.loading)

	// The pane is sized to 4 rows so scrolling is observable; the long
	// content guarantees there is somewhere to scroll to.
	longContent := strings.Repeat("scroll me\n", 50)
	require.Nil(t, sd.HandleMsg(SkillDetailLoadedMsg{Content: longContent}))
	// The viewport only measures its content when it is rendered, so the
	// scroll keys need the pane to have painted once at its size.
	sd.body.SetContent(longContent)
	sd.body.SetWidth(38)
	sd.body.SetHeight(4)
	sd.body.View()
	require.Equal(t, 0, sd.body.YOffset())

	// Scrolling keys belong to the reading pane and produce no action.
	// Down scrolls by one line: the reading pane must follow the key.
	require.Nil(t, sd.HandleMsg(tea.KeyPressMsg{Code: tea.KeyDown}))
	require.Greater(t, sd.body.YOffset(), 0)

	// Escape closes.
	require.Equal(t, ActionClose{}, sd.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEscape}))
}
