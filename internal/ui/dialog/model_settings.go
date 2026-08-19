package dialog

import (
	"errors"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/hackafterdark/phosphor/internal/ui/common"
	"github.com/hackafterdark/phosphor/pkg/config"
)

// ModelSettingsID is the identifier for the model settings dialog.
const ModelSettingsID = "model_settings"

// Dialog sizing for model settings.
const (
	// modelSettingsFieldHeight is label + value + spacing per field.
	modelSettingsFieldHeight       = 3
	modelSettingsMaxInputWidth     = 24
	modelSettingsMaxViewportHeight = 21
)

// modelSettingKind identifies whether a row accepts free text or cycles
// through discrete values.
type modelSettingKind int

const (
	modelSettingText modelSettingKind = iota
	modelSettingDiscrete
)

// modelSetting is a single row in the model settings dialog.
type modelSetting struct {
	key    string
	label  string
	kind   modelSettingKind
	values []string
	idx    int
	input  textinput.Model
}

// value returns the current value of the row.
func (s *modelSetting) value() string {
	switch s.kind {
	case modelSettingDiscrete:
		return s.values[s.idx]
	default:
		return s.input.Value()
	}
}

// ModelSettings represents a dialog for tuning the active model's
// sampling and reasoning parameters.
type ModelSettings struct {
	com      *common.Common
	help     help.Model
	settings []*modelSetting
	focused  int
	viewport viewport.Model
	err      string

	keyMap struct {
		Save,
		Next,
		Previous,
		Cycle,
		Close key.Binding
	}
}

var _ Dialog = (*ModelSettings)(nil)

// NewModelSettings creates a new model settings dialog populated with the
// active model's current values.
func NewModelSettings(com *common.Common) (*ModelSettings, error) {
	cfg := com.Config()
	if cfg == nil {
		return nil, errors.New("configuration not found")
	}

	agentCfg, ok := cfg.Agents[config.AgentSystem]
	if !ok {
		return nil, errors.New("agent configuration not found")
	}

	selected := cfg.Models[agentCfg.Model]
	model := cfg.GetModelByType(agentCfg.Model)

	m := &ModelSettings{com: com}

	m.help = help.New()
	m.help.Styles = com.Styles.DialogHelpStyles()

	m.keyMap.Save = key.NewBinding(
		key.WithKeys("enter"),
		key.WithHelp("enter", "save"),
	)
	m.keyMap.Next = key.NewBinding(
		key.WithKeys("down", "tab"),
		key.WithHelp("↓/tab", "next"),
	)
	m.keyMap.Previous = key.NewBinding(
		key.WithKeys("up", "shift+tab"),
		key.WithHelp("↑/shift+tab", "prev"),
	)
	m.keyMap.Cycle = key.NewBinding(
		key.WithKeys("left", "right"),
		key.WithHelp("←/→", "change"),
	)
	m.keyMap.Close = CloseKey

	// Reasoning effort row. Always shown: catalog models with declared
	// levels offer those exact levels, while everything else (local,
	// discovered, or unknown models) falls back to common presets.
	// Engines that don't support reasoning_effort ignore it.
	var values []string
	if model != nil && len(model.ReasoningLevels) > 0 {
		values = append([]string{"default"}, model.ReasoningLevels...)
	} else {
		values = []string{"default", "low", "medium", "high", "xhigh"}
	}
	idx := 0
	for i, v := range values {
		if i > 0 && v == selected.ReasoningEffort {
			idx = i
			break
		}
	}
	m.settings = append(m.settings, &modelSetting{
		key:    "reasoning_effort",
		label:  "Reasoning Effort",
		kind:   modelSettingDiscrete,
		values: values,
		idx:    idx,
	})

	// Thinking row. Catalog models that reason without declared effort
	// levels get a simple on/off toggle backed by the Think setting.
	// Every other model (local, discovered, or non-reasoning catalog
	// models) gets a tri-state enable_thinking override cycled with
	// left/right; "default" leaves the provider policy kwarg untouched,
	// and engines that don't use the kwarg ignore it.
	if model != nil && model.CanReason && len(model.ReasoningLevels) == 0 {
		idx := 0
		if selected.Think {
			idx = 1
		}
		m.settings = append(m.settings, &modelSetting{
			key:    "thinking",
			label:  "Thinking",
			kind:   modelSettingDiscrete,
			values: []string{"off", "on"},
			idx:    idx,
		})
	} else {
		idx := 0
		switch selected.EnableThinking {
		case "on":
			idx = 1
		case "off":
			idx = 2
		}
		m.settings = append(m.settings, &modelSetting{
			key:    "enable_thinking",
			label:  "Thinking",
			kind:   modelSettingDiscrete,
			values: []string{"default", "on", "off"},
			idx:    idx,
		})
	}

	// Sampling parameter rows.
	maxTokens := ""
	if selected.MaxTokens > 0 {
		maxTokens = strconv.FormatInt(selected.MaxTokens, 10)
	}
	m.settings = append(m.settings,
		newModelSettingText(com, "temperature", "Temperature", "default", formatFloatPtr(selected.Temperature)),
		newModelSettingText(com, "top_p", "Top P", "default", formatFloatPtr(selected.TopP)),
		newModelSettingText(com, "top_k", "Top K", "default", formatIntPtr(selected.TopK)),
		newModelSettingText(com, "max_tokens", "Max Tokens", "default", maxTokens),
		newModelSettingText(com, "max_thinking_tokens", "Max Thinking Tokens", "default", formatIntPtr(selected.MaxThinkingTokens)),
	)

	m.focused = 0
	if len(m.settings) > 0 && m.settings[0].kind == modelSettingText {
		m.settings[0].input.Focus()
	}

	return m, nil
}

// newModelSettingText creates a free-text setting row.
func newModelSettingText(com *common.Common, keyName, label, placeholder, value string) *modelSetting {
	input := textinput.New()
	input.SetVirtualCursor(false)
	input.Prompt = ""
	input.Placeholder = placeholder
	input.SetValue(value)
	input.SetStyles(com.Styles.TextInput)
	return &modelSetting{
		key:   keyName,
		label: label,
		kind:  modelSettingText,
		input: input,
	}
}

// ID implements Dialog.
func (m *ModelSettings) ID() string {
	return ModelSettingsID
}

// focusRow moves focus to the row at the given index, wrapping around.
func (m *ModelSettings) focusRow(newIndex int) {
	n := len(m.settings)
	old := m.settings[m.focused]
	if old.kind == modelSettingText {
		old.input.Blur()
	}
	m.focused = ((newIndex % n) + n) % n
	row := m.settings[m.focused]
	if row.kind == modelSettingText {
		row.input.Focus()
	}
	m.ensureRowVisible(m.focused)
}

// isRowVisible checks if the row at the given index is visible in the viewport.
func (m *ModelSettings) isRowVisible(rowIndex int) bool {
	rowStart := rowIndex * modelSettingsFieldHeight
	rowEnd := rowStart + modelSettingsFieldHeight - 1
	viewportTop := m.viewport.YOffset()
	viewportBottom := viewportTop + m.viewport.Height() - 1

	return rowStart >= viewportTop && rowEnd <= viewportBottom
}

// ensureRowVisible scrolls the viewport to make the row visible.
func (m *ModelSettings) ensureRowVisible(rowIndex int) {
	if m.isRowVisible(rowIndex) {
		return
	}

	rowStart := rowIndex * modelSettingsFieldHeight
	rowEnd := rowStart + modelSettingsFieldHeight - 1
	viewportTop := m.viewport.YOffset()
	viewportHeight := m.viewport.Height()

	// If row is above viewport, scroll up to show it at top.
	if rowStart < viewportTop {
		m.viewport.SetYOffset(rowStart)
		return
	}

	// If row is below viewport, scroll down to show it at bottom.
	if rowEnd > viewportTop+viewportHeight-1 {
		m.viewport.SetYOffset(rowEnd - viewportHeight + 1)
	}
}

// findVisibleRowByOffset returns the row index closest to the viewport offset.
func (m *ModelSettings) findVisibleRowByOffset(fromTop bool) int {
	offset := m.viewport.YOffset()
	if !fromTop {
		offset += m.viewport.Height() - 1
	}

	rowIndex := offset / modelSettingsFieldHeight
	if rowIndex >= len(m.settings) {
		return len(m.settings) - 1
	}
	return rowIndex
}

// cycleValue moves the focused discrete row's value in the given direction.
func (m *ModelSettings) cycleValue(decrease bool) {
	row := m.settings[m.focused]
	if row.kind != modelSettingDiscrete {
		return
	}
	if decrease {
		row.idx = (row.idx - 1 + len(row.values)) % len(row.values)
	} else {
		row.idx = (row.idx + 1) % len(row.values)
	}
}

// save validates the form and returns the save action, or nil when an
// error is shown.
func (m *ModelSettings) save() Action {
	action := ActionModelSettings{}

	for _, row := range m.settings {
		if row.kind == modelSettingDiscrete {
			switch row.key {
			case "reasoning_effort":
				v := ""
				if row.idx > 0 {
					v = row.values[row.idx]
				}
				action.ReasoningEffort = &v
			case "thinking":
				v := row.idx == 1
				action.Thinking = &v
			case "enable_thinking":
				v := row.values[row.idx]
				if v == "default" {
					v = ""
				}
				action.EnableThinking = &v
			}
			continue
		}

		raw := strings.TrimSpace(row.input.Value())
		if raw == "" {
			continue
		}

		switch row.key {
		case "temperature":
			v, err := strconv.ParseFloat(raw, 64)
			if err != nil || v < 0 || v > 2 {
				m.err = "Temperature must be a number between 0 and 2."
				return nil
			}
			action.Temperature = &v
		case "top_p":
			v, err := strconv.ParseFloat(raw, 64)
			if err != nil || v <= 0 || v > 1 {
				m.err = "Top P must be a number between 0 and 1."
				return nil
			}
			action.TopP = &v
		case "top_k":
			v, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || v <= 0 {
				m.err = "Top K must be an integer greater than 0."
				return nil
			}
			action.TopK = &v
		case "max_tokens":
			v, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || v <= 0 {
				m.err = "Max Tokens must be an integer greater than 0."
				return nil
			}
			action.MaxTokens = &v
		case "max_thinking_tokens":
			v, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || v <= 0 {
				m.err = "Max Thinking Tokens must be an integer greater than 0."
				return nil
			}
			action.MaxThinkingTokens = &v
		}
	}

	m.err = ""
	return action
}

// HandleMsg implements [Dialog].
func (m *ModelSettings) HandleMsg(msg tea.Msg) Action {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch {
		case key.Matches(msg, m.keyMap.Close):
			return ActionClose{}
		case key.Matches(msg, m.keyMap.Save):
			return m.save()
		case key.Matches(msg, m.keyMap.Next):
			m.focusRow(m.focused + 1)
		case key.Matches(msg, m.keyMap.Previous):
			m.focusRow(m.focused - 1)
		case key.Matches(msg, m.keyMap.Cycle):
			row := m.settings[m.focused]
			if row.kind == modelSettingDiscrete {
				m.cycleValue(msg.String() == "left")
				break
			}
			var cmd tea.Cmd
			row.input, cmd = row.input.Update(msg)
			if cmd != nil {
				return ActionCmd{cmd}
			}
		default:
			row := m.settings[m.focused]
			if row.kind != modelSettingText {
				return nil
			}
			var cmd tea.Cmd
			row.input, cmd = row.input.Update(msg)
			if cmd != nil {
				return ActionCmd{cmd}
			}
		}
	case common.CoalescedWheelMsg:
		m.viewport, _ = m.viewport.Update(tea.MouseWheelMsg(msg.Mouse))
		// If the focused row scrolled out of view, focus the visible row.
		if !m.isRowVisible(m.focused) {
			m.focusRow(m.findVisibleRowByOffset(msg.DeltaY > 0))
		}
	case tea.PasteMsg:
		row := m.settings[m.focused]
		if row.kind != modelSettingText {
			return nil
		}
		var cmd tea.Cmd
		row.input, cmd = row.input.Update(msg)
		if cmd != nil {
			return ActionCmd{cmd}
		}
	}
	return nil
}

// Cursor returns the cursor position relative to the dialog.
func (m *ModelSettings) Cursor() *tea.Cursor {
	row := m.settings[m.focused]
	if row.kind != modelSettingText {
		return nil
	}
	cursor := InputCursor(m.com.Styles, row.input.Cursor())
	if cursor == nil {
		return nil
	}
	cursor.Y += m.focused*modelSettingsFieldHeight - m.viewport.YOffset() + 1
	return cursor
}

// Draw implements [Dialog].
func (m *ModelSettings) Draw(scr uv.Screen, area uv.Rectangle) *tea.Cursor {
	s := m.com.Styles
	dialogContentStyle := s.Dialog.Arguments.Content
	possibleWidth := area.Dx() - s.Dialog.View.GetHorizontalFrameSize() -
		dialogContentStyle.GetHorizontalFrameSize()

	var fields []string
	for i, row := range m.settings {
		isFocused := i == m.focused

		labelStyle := s.Dialog.Arguments.InputLabelBlurred
		if isFocused {
			labelStyle = s.Dialog.Arguments.InputLabelFocused
		}
		label := labelStyle.Render(row.label)

		var valueLine string
		if row.kind == modelSettingText {
			inputWidth := min(modelSettingsMaxInputWidth, max(8, possibleWidth))
			row.input.SetWidth(inputWidth)
			valueLine = row.input.View()
		} else {
			valueStyle := s.Dialog.Arguments.InputLabelBlurred
			if isFocused {
				valueStyle = s.Dialog.PrimaryText
			}
			valueLine = valueStyle.Render(row.value())
		}

		field := lipgloss.JoinVertical(lipgloss.Left, label, valueLine, "")
		fields = append(fields, field)
	}

	renderedFields := lipgloss.JoinVertical(lipgloss.Left, fields...)

	const scrollbarWidth = 1
	width := lipgloss.Width(renderedFields)
	height := lipgloss.Height(renderedFields)

	// Widen the dialog so the hotkey hints fit on one line, without
	// exceeding the available width. The help style's frame (padding)
	// counts against the content width, so include it in the target.
	helpText := m.help.View(m)
	if possibleWidth > 0 {
		width = min(possibleWidth,
			max(width, lipgloss.Width(helpText)+
				s.Dialog.HelpView.GetHorizontalFrameSize()))
	}

	titleStyle := s.Dialog.Title
	header := common.DialogTitle(s, "Model Settings", width,
		s.Dialog.TitleGradFromColor, s.Dialog.TitleGradToColor)

	helpView := s.Dialog.HelpView.Width(width).Render(helpText)
	if m.err != "" {
		helpView = s.Dialog.OAuth.ErrorText.Width(width).Render(m.err)
	}

	availableHeight := area.Dy() - s.Dialog.View.GetVerticalFrameSize() -
		dialogContentStyle.GetVerticalFrameSize() -
		lipgloss.Height(header) - lipgloss.Height(helpView) - 2
	viewportHeight := min(height, modelSettingsMaxViewportHeight, max(1, availableHeight))

	m.viewport.SetWidth(width)
	m.viewport.SetHeight(viewportHeight)
	m.viewport.SetContent(renderedFields)

	scrollbar := common.Scrollbar(s, viewportHeight, m.viewport.TotalLineCount(), viewportHeight, m.viewport.YOffset())
	content := m.viewport.View()
	if scrollbar != "" {
		content = lipgloss.JoinHorizontal(lipgloss.Top, content, scrollbar)
	}

	view := lipgloss.JoinVertical(
		lipgloss.Left,
		titleStyle.Render(header),
		dialogContentStyle.Render(content),
		helpView,
	)

	dialog := s.Dialog.View.Render(view)

	cur := m.Cursor()
	DrawCenterCursor(scr, area, dialog, cur)
	return cur
}

// ShortHelp implements [help.KeyMap].
func (m *ModelSettings) ShortHelp() []key.Binding {
	bindings := []key.Binding{m.keyMap.Save}
	// Only advertise ←/→ when the focused row can actually cycle; text
	// rows use the arrow keys for cursor movement, and the narrower hint
	// line leaves more room in small terminals.
	if m.focused < len(m.settings) && m.settings[m.focused].kind == modelSettingDiscrete {
		bindings = append(bindings, m.keyMap.Cycle)
	}
	bindings = append(bindings, m.keyMap.Next, m.keyMap.Previous, m.keyMap.Close)
	return bindings
}

// FullHelp implements [help.KeyMap].
func (m *ModelSettings) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{m.keyMap.Save, m.keyMap.Next, m.keyMap.Previous, m.keyMap.Close},
		{m.keyMap.Cycle},
	}
}

// formatFloatPtr renders a *float64 for display, or "" when nil.
func formatFloatPtr(v *float64) string {
	if v == nil {
		return ""
	}
	return strconv.FormatFloat(*v, 'f', -1, 64)
}

// formatIntPtr renders a *int64 for display, or "" when nil.
func formatIntPtr(v *int64) string {
	if v == nil {
		return ""
	}
	return strconv.FormatInt(*v, 10)
}
