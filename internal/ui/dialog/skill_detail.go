package dialog

import (
	"os"
	"path/filepath"
	"strings"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/hackafterdark/phosphor/internal/ui/common"
	"github.com/hackafterdark/phosphor/pkg/skills"
)

const (
	// SkillDetailID is the identifier for the skill detail dialog.
	SkillDetailID          = "skill_detail"
	skillDetailMinWidth    = 56
	skillDetailMaxWidth    = 140
	skillDetailWidthRatio  = 0.9
	skillDetailMinHeight   = 14
	skillDetailMaxHeight   = 44
	skillDetailHeightRatio = 0.8
	skillDetailContentPad  = 1
)

// SkillDetail is a read-only viewer for one skill's SKILL.md: the full
// file content in a scrolling pane, sized generously so lengthy skills
// stay readable. It is opened from the skills dialog via the details key.
type SkillDetail struct {
	com  *common.Common
	help help.Model
	body viewport.Model

	skill        *skills.Skill
	source       skills.SourceType
	loading      bool
	content      string
	err          error
	contentWidth int

	keyMap struct {
		ScrollUp   key.Binding
		ScrollDown key.Binding
		Close      key.Binding
	}
}

var (
	_ Dialog      = (*SkillDetail)(nil)
	_ help.KeyMap = (*SkillDetail)(nil)
)

// NewSkillDetail creates the detail dialog in its loading state; the
// owner sends a [SkillDetailLoadedMsg] to fill it.
func NewSkillDetail(com *common.Common, skill *skills.Skill, source skills.SourceType) *SkillDetail {
	s := &SkillDetail{com: com, skill: skill, source: source, loading: true}

	h := help.New()
	h.Styles = com.Styles.DialogHelpStyles()
	s.help = h

	s.keyMap.ScrollUp = key.NewBinding(
		key.WithKeys("up", "ctrl+p"),
		key.WithHelp("↑/↓", "scroll"),
	)
	s.keyMap.ScrollDown = key.NewBinding(
		key.WithKeys("down", "ctrl+n"),
	)
	s.keyMap.Close = CloseKey

	body := viewport.New()
	body.SoftWrap = true
	body.FillHeight = true
	body.MouseWheelEnabled = true
	body.KeyMap = viewport.KeyMap{
		Up:           s.keyMap.ScrollUp,
		Down:         s.keyMap.ScrollDown,
		PageUp:       key.NewBinding(key.WithKeys("pgup")),
		PageDown:     key.NewBinding(key.WithKeys("pgdn")),
		HalfPageUp:   key.NewBinding(key.WithDisabled()),
		HalfPageDown: key.NewBinding(key.WithDisabled()),
		// Everything soft-wraps; there is nothing sideways to scroll.
		Left:  key.NewBinding(key.WithDisabled()),
		Right: key.NewBinding(key.WithDisabled()),
	}
	s.body = body

	return s
}

// SkillDetailLoadedMsg carries the skill file content for the detail
// dialog. The owner reads the file on a tea.Cmd so disk or embedded
// reads never block the update loop.
type SkillDetailLoadedMsg struct {
	Content string
	Err     error
}

// SkillFileContent reads the raw SKILL.md for a skill, resolving builtin
// skills through the embedded filesystem.
func SkillFileContent(skill *skills.Skill) (string, error) {
	if skill == nil {
		return "", nil
	}
	if strings.HasPrefix(skill.SkillFilePath, skills.BuiltinPrefix) {
		rel := strings.TrimPrefix(skill.SkillFilePath, skills.BuiltinPrefix)
		data, err := skills.BuiltinFS().ReadFile("builtin/" + filepath.ToSlash(rel))
		return string(data), err
	}
	data, err := os.ReadFile(skill.SkillFilePath)
	return string(data), err
}

// ID implements Dialog.
func (s *SkillDetail) ID() string {
	return SkillDetailID
}

// HandleMsg implements [Dialog].
func (s *SkillDetail) HandleMsg(msg tea.Msg) Action {
	switch msg := msg.(type) {
	case SkillDetailLoadedMsg:
		s.loading = false
		s.err = msg.Err
		s.content = msg.Content
		s.body.SetContent(s.content)
		s.body.GotoTop()
		return nil

	case common.CoalescedWheelMsg:
		if msg.DeltaY != 0 {
			s.body, _ = s.body.Update(tea.MouseWheelMsg(msg.Mouse))
		}
		return nil

	case tea.KeyPressMsg:
		switch {
		case key.Matches(msg, s.keyMap.Close):
			return ActionClose{}
		default:
			// ↑/↓ (and pgup/pgdn) belong to the reading pane.
			s.body, _ = s.body.Update(msg)
		}
	}
	return nil
}

// Cursor returns the cursor for the dialog.
func (s *SkillDetail) Cursor() *tea.Cursor {
	return nil
}

// measure is the dialog's fixed geometry for a screen, clamped to the
// terminal so a tiny window never draws a frame taller than it can show.
func (s *SkillDetail) measure(area uv.Rectangle) (width, height, bodyHeight int) {
	t := s.com.Styles
	width = max(min(skillDetailMinWidth, area.Dx()),
		min(int(float64(area.Dx())*skillDetailWidthRatio), skillDetailMaxWidth))
	height = max(min(skillDetailMinHeight, area.Dy()),
		min(int(float64(area.Dy())*skillDetailHeightRatio), skillDetailMaxHeight))

	verticalBudget := t.Dialog.View.GetVerticalFrameSize() +
		t.Dialog.Title.GetVerticalFrameSize() + titleContentHeight +
		1 + // the subtitle line, always present
		1 + // the title gap row
		2 + // one gap row each above and below the body
		t.Dialog.HelpView.GetVerticalFrameSize()
	bodyHeight = max(1, height-verticalBudget)
	return
}

// Draw implements [Dialog].
func (s *SkillDetail) Draw(scr uv.Screen, area uv.Rectangle) *tea.Cursor {
	width, _, bodyHeight := s.measure(area)
	DrawCenter(scr, area, s.render(width, bodyHeight))
	return nil
}

// render assembles the framed dialog: title, subtitle, the reading pane,
// and the key help.
func (s *SkillDetail) render(width, bodyHeight int) string {
	t := s.com.Styles
	innerWidth := width - t.Dialog.View.GetHorizontalFrameSize()

	rc := NewRenderContext(t, width)
	rc.Gap = 1
	rc.TitleGap = 1
	rc.SubtitleStyle = t.Dialog.NormalItem
	rc.Title = s.title()
	rc.Subtitle = s.subtitle()

	switch {
	case s.loading:
		rc.AddPart(padded(t.Dialog.SecondaryText.Render("Reading skill file..."), innerWidth, bodyHeight))
	case s.err != nil:
		rc.AddPart(padded(t.Dialog.SecondaryText.Render("Failed to read skill file: "+s.err.Error()), innerWidth, bodyHeight))
	case s.content == "":
		rc.AddPart(padded(t.Dialog.SecondaryText.Render("(empty file)"), innerWidth, bodyHeight))
	default:
		rc.AddPart(s.renderBody(innerWidth, bodyHeight))
	}

	rc.Help = s.help.View(s)
	return rc.Render()
}

// title shows the skill name, or the file path if the name is missing.
func (s *SkillDetail) title() string {
	if s.skill == nil {
		return "Skill"
	}
	if s.skill.Name != "" {
		return s.skill.Name
	}
	return filepath.Base(s.skill.SkillFilePath)
}

// subtitle shows where the skill comes from and its description.
func (s *SkillDetail) subtitle() string {
	if s.skill == nil {
		return " "
	}
	source := string(s.source)
	if s.skill.Description != "" {
		return source + " · " + s.skill.Description
	}
	return source
}

// renderBody is the scrolling reading pane holding the raw SKILL.md.
func (s *SkillDetail) renderBody(innerWidth, bodyHeight int) string {
	contentWidth := max(20, innerWidth-2*skillDetailContentPad)
	if contentWidth != s.contentWidth {
		s.body.SetContent(s.content)
		s.contentWidth = contentWidth
	}
	s.body.SetWidth(contentWidth)
	s.body.SetHeight(bodyHeight)
	return lipgloss.NewStyle().
		Width(contentWidth+2*skillDetailContentPad).
		Padding(0, skillDetailContentPad).
		Height(bodyHeight).
		MaxHeight(bodyHeight).
		Render(s.body.View())
}

// ShortHelp implements [help.KeyMap].
func (s *SkillDetail) ShortHelp() []key.Binding {
	return []key.Binding{
		s.keyMap.ScrollUp,
		s.keyMap.Close,
	}
}

// FullHelp implements [help.KeyMap].
func (s *SkillDetail) FullHelp() [][]key.Binding {
	return [][]key.Binding{s.ShortHelp()}
}
