package dialog

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/hackafterdark/phosphor/internal/ui/common"
	"github.com/hackafterdark/phosphor/internal/ui/list"
	"github.com/hackafterdark/phosphor/internal/ui/styles"
	"github.com/hackafterdark/phosphor/internal/workspace"
	"github.com/hackafterdark/phosphor/pkg/config"
	"github.com/hackafterdark/phosphor/pkg/skills"
	"github.com/sahilm/fuzzy"
)

// SkillsID is the identifier for the skills dialog.
const SkillsID = "skills"

const (
	skillsDialogMaxWidth  = 70
	skillsDialogMaxHeight = 20
)

// ActionToggleSkill is sent when a skill is toggled in the skills dialog.
type ActionToggleSkill struct {
	Name    string
	Disable bool
}

// SkillItem represents a single skill entry in the skills dialog.
type SkillItem struct {
	*list.Versioned
	Name        string
	Description string
	Source      skills.SourceType
	Disabled    bool
	t           *styles.Styles
	focused     bool
	m           fuzzy.Match
}

var (
	_ Dialog              = (*Skills)(nil)
	_ list.FilterableItem = (*SkillItem)(nil)
)

// Skills represents a dialog for viewing and toggling agent skills.
type Skills struct {
	com  *common.Common
	help help.Model
	list *list.FilterableList

	keyMap struct {
		Toggle   key.Binding
		Next     key.Binding
		Previous key.Binding
		Close    key.Binding
	}

	width int
}

// NewSkills creates a new skills dialog.
func NewSkills(com *common.Common) *Skills {
	s := &Skills{
		com: com,
	}

	h := help.New()
	h.Styles = com.Styles.DialogHelpStyles()
	s.help = h

	s.list = list.NewFilterableList()
	s.list.Focus()

	s.keyMap.Toggle = key.NewBinding(
		key.WithKeys("enter", "space", "ctrl+y"),
		key.WithHelp("enter/space", "toggle"),
	)
	s.keyMap.Next = key.NewBinding(
		key.WithKeys("down", "ctrl+n"),
		key.WithHelp("↓", "next item"),
	)
	s.keyMap.Previous = key.NewBinding(
		key.WithKeys("up", "ctrl+p"),
		key.WithHelp("↑", "previous item"),
	)
	s.keyMap.Close = CloseKey

	s.setItems()
	return s
}

// ID implements Dialog.
func (s *Skills) ID() string {
	return SkillsID
}

// HandleMsg implements [Dialog].
func (s *Skills) HandleMsg(msg tea.Msg) Action {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch {
		case key.Matches(msg, s.keyMap.Close):
			return ActionClose{}
		case key.Matches(msg, s.keyMap.Previous):
			s.list.Focus()
			if s.list.IsSelectedFirst() {
				s.list.SelectLast()
				s.list.ScrollToBottom()
				break
			}
			s.list.SelectPrev()
			s.list.ScrollToSelected()
		case key.Matches(msg, s.keyMap.Next):
			s.list.Focus()
			if s.list.IsSelectedLast() {
				s.list.SelectFirst()
				s.list.ScrollToTop()
				break
			}
			s.list.SelectNext()
			s.list.ScrollToSelected()
		case key.Matches(msg, s.keyMap.Toggle):
			selectedItem := s.list.SelectedItem()
			if selectedItem == nil {
				break
			}
			skillItem, ok := selectedItem.(*SkillItem)
			if !ok {
				break
			}
			skillItem.Disabled = !skillItem.Disabled
			skillItem.Bump()
			return ActionToggleSkill{
				Name:    skillItem.Name,
				Disable: skillItem.Disabled,
			}
		}
	}
	return nil
}

// Cursor returns the cursor for the dialog.
func (s *Skills) Cursor() *tea.Cursor {
	return nil
}

// Draw implements [Dialog].
func (s *Skills) Draw(scr uv.Screen, area uv.Rectangle) *tea.Cursor {
	t := s.com.Styles
	width := max(0, min(skillsDialogMaxWidth, area.Dx()))
	height := max(0, min(skillsDialogMaxHeight, area.Dy()))
	innerWidth := width - t.Dialog.View.GetHorizontalFrameSize()
	heightOffset := t.Dialog.Title.GetVerticalFrameSize() + titleContentHeight +
		t.Dialog.HelpView.GetVerticalFrameSize() +
		t.Dialog.View.GetVerticalFrameSize()

	s.list.SetSize(innerWidth, height-heightOffset)
	s.help.SetWidth(innerWidth)

	rc := NewRenderContext(t, width)
	rc.Title = "Skills"
	rc.Subtitle = s.subtitle()
	rc.Gap = 1

	visibleCount := len(s.list.FilteredItems())
	if s.list.Height() >= visibleCount {
		s.list.ScrollToTop()
	} else {
		s.list.ScrollToSelected()
	}

	listView := t.Dialog.List.Height(s.list.Height()).Render(s.list.Render())
	rc.AddPart(listView)
	rc.Help = s.help.View(s)

	view := rc.Render()

	cur := s.Cursor()
	DrawCenterCursor(scr, area, view, cur)
	return cur
}

// subtitle summarizes the enabled/disabled state of the listed skills.
func (s *Skills) subtitle() string {
	enabled := 0
	for _, item := range s.list.FilteredItems() {
		if skillItem, ok := item.(*SkillItem); ok && !skillItem.Disabled {
			enabled++
		}
	}
	total := len(s.list.FilteredItems())
	return fmt.Sprintf("%d/%d enabled", enabled, total)
}

// ShortHelp implements [help.KeyMap].
func (s *Skills) ShortHelp() []key.Binding {
	return []key.Binding{
		s.keyMap.Toggle,
		s.keyMap.Close,
	}
}

// FullHelp implements [help.KeyMap].
func (s *Skills) FullHelp() [][]key.Binding {
	m := [][]key.Binding{}
	slice := []key.Binding{
		s.keyMap.Toggle,
		s.keyMap.Next,
		s.keyMap.Previous,
		s.keyMap.Close,
	}
	for i := 0; i < len(slice); i += 4 {
		end := min(i+4, len(slice))
		m = append(m, slice[i:end])
	}
	return m
}

// setItems populates the list with every discovered skill, disabled or
// not, annotated with its source and current enabled state.
func (s *Skills) setItems() {
	disabled := s.disabledSet()

	all := skills.Deduplicate(append(
		slices.Clone(cachedBuiltinSkillsForDialog()),
		s.discoveredSkills()...,
	))
	slices.SortStableFunc(all, func(a, b *skills.Skill) int {
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})

	items := make([]list.FilterableItem, 0, len(all))
	for _, skill := range all {
		items = append(items, &SkillItem{
			Versioned:   list.NewVersioned(),
			Name:        skill.Name,
			Description: skill.Description,
			Source:      s.sourceFor(skill),
			Disabled:    disabled[skill.Name],
			t:           s.com.Styles,
		})
	}

	s.list.SetItems(items...)
	s.list.SetSelected(0)
	s.list.ScrollToTop()
}

// disabledSet returns the set of currently disabled skill names from the
// live config.
func (s *Skills) disabledSet() map[string]bool {
	result := make(map[string]bool)
	cfg := s.com.Config()
	if cfg == nil || cfg.Options == nil {
		return result
	}
	for _, name := range cfg.Options.DisabledSkills {
		result[name] = true
	}
	return result
}

// discoveredSkills returns the user and project skills found under the
// configured skills paths.
func (s *Skills) discoveredSkills() []*skills.Skill {
	cfg := s.com.Config()
	if cfg == nil || cfg.Options == nil || len(cfg.Options.SkillsPaths) == 0 {
		return nil
	}
	var resolver func(string) (string, error)
	if r := s.com.Workspace.Resolver(); r != nil {
		resolver = r.ResolveValue
	}
	expanded := skills.DiscoveryConfig{
		SkillsPaths: cfg.Options.SkillsPaths,
		Resolver:    resolver,
	}.ResolvePaths()
	return skills.Discover(expanded)
}

// sourceFor labels where a skill comes from: system for builtins,
// project for skills inside the workspace, user otherwise.
func (s *Skills) sourceFor(skill *skills.Skill) skills.SourceType {
	if skill.Builtin {
		return skills.SourceSystem
	}
	workingDir := s.com.Workspace.WorkingDir()
	if workingDir == "" {
		return skills.SourceUser
	}
	absFile, err := filepath.Abs(skill.SkillFilePath)
	if err != nil {
		return skills.SourceUser
	}
	absWD, err := filepath.Abs(workingDir)
	if err != nil {
		return skills.SourceUser
	}
	rel, err := filepath.Rel(filepath.Clean(absWD), filepath.Clean(absFile))
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return skills.SourceUser
	}
	return skills.SourceProject
}

// cachedBuiltinSkillsForDialog returns the embedded builtin skills.
func cachedBuiltinSkillsForDialog() []*skills.Skill {
	return skills.DiscoverBuiltin()
}

// Filter implements [list.FilterableItem].
func (s *SkillItem) Filter() string {
	return s.Name
}

// Finished implements [list.FilterableItem].
func (s *SkillItem) Finished() bool {
	return true
}

// SetFocused implements [list.FilterableItem].
func (s *SkillItem) SetFocused(focused bool) {
	if s.focused == focused {
		return
	}
	s.focused = focused
	s.Bump()
}

// SetMatch implements [list.FilterableItem].
func (s *SkillItem) SetMatch(m fuzzy.Match) {
	if sameFuzzyMatch(s.m, m) {
		return
	}
	s.m = m
	s.Bump()
}

// Render implements [list.FilterableItem].
func (s *SkillItem) Render(width int) string {
	info := string(s.Source)
	if s.Disabled {
		info += " · disabled"
	}
	if s.Description != "" {
		info += " · " + s.Description
	}

	itemStyles := ListItemStyles{
		ItemBlurred:     s.t.Dialog.NormalItem,
		ItemFocused:     s.t.Dialog.SelectedItem,
		InfoTextBlurred: s.t.Dialog.Sessions.InfoBlurred,
		InfoTextFocused: s.t.Dialog.Sessions.InfoFocused,
	}
	if s.Disabled {
		itemStyles.InfoTextBlurred = s.t.Dialog.SecondaryText
		itemStyles.InfoTextFocused = s.t.Dialog.SecondaryText
	}

	return renderItem(itemStyles, s.Name, info, s.focused, width, nil, &s.m)
}

// ToggleSkill adds or removes a skill name from the disabled-skills list
// and persists the result to the workspace config.
func ToggleSkill(ws workspace.Workspace, name string, disable bool) error {
	var current []string
	if cfg := ws.Config(); cfg != nil && cfg.Options != nil {
		current = slices.Clone(cfg.Options.DisabledSkills)
	}

	var updated []string
	if disable {
		if slices.Contains(current, name) {
			return nil
		}
		updated = append(current, name)
	} else {
		idx := slices.Index(current, name)
		if idx == -1 {
			return nil
		}
		updated = slices.Delete(slices.Clone(current), idx, idx+1)
	}

	if len(updated) == 0 {
		return ws.RemoveConfigField(config.ScopeWorkspace, "options.disabled_skills")
	}
	return ws.SetConfigField(config.ScopeWorkspace, "options.disabled_skills", updated)
}
