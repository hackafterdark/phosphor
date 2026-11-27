package dialog

import (
	"sort"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/hackafterdark/phosphor/internal/ui/common"
	"github.com/hackafterdark/phosphor/internal/ui/list"
	"github.com/hackafterdark/phosphor/internal/ui/styles"
	"github.com/hackafterdark/phosphor/pkg/config"
	"github.com/sahilm/fuzzy"
)

const (
	// PreferencesID is the identifier for the display preferences dialog.
	PreferencesID              = "preferences"
	preferencesDialogMaxWidth  = 60
	preferencesDialogMaxHeight = 16
)

// preferenceKind identifies the behavior of a preference row.
type preferenceKind int

const (
	// preferenceThinking toggles the assistant thinking display.
	preferenceThinking preferenceKind = iota
	// preferenceCompactMode forces the compact layout regardless of window size.
	preferenceCompactMode
	// preferenceDefaultOutput cycles the default tool output mode.
	preferenceDefaultOutput
	// preferenceToolOutput cycles the per-tool tool output mode.
	preferenceToolOutput
	// preferenceLineLimit cycles the short-mode line cap for tool output.
	preferenceLineLimit
)

// Preferences represents a dialog for display preferences: thinking
// visibility and per-tool result output modes.
type Preferences struct {
	com   *common.Common
	help  help.Model
	list  *list.FilterableList
	input textinput.Model

	// addingTool is set when the typed text should be treated as a new
	// tool name for a per-tool output override instead of a filter term.
	addingTool bool

	keyMap struct {
		Select   key.Binding
		Next     key.Binding
		Previous key.Binding
		UpDown   key.Binding
		Close    key.Binding
	}
}

// PreferenceItem represents a single preference row.
type PreferenceItem struct {
	*list.Versioned
	kind    preferenceKind
	tool    string
	value   string
	t       *styles.Styles
	m       fuzzy.Match
	cache   map[int]string
	focused bool
}

// Finished implements list.Item. Preference items are render-stable outside
// of explicit SetFocused / SetMatch / SetValue.
func (p *PreferenceItem) Finished() bool {
	return true
}

var (
	_ Dialog   = (*Preferences)(nil)
	_ ListItem = (*PreferenceItem)(nil)
)

// NewPreferences creates a new display preferences dialog.
func NewPreferences(com *common.Common) *Preferences {
	p := &Preferences{com: com}

	h := help.New()
	h.Styles = com.Styles.DialogHelpStyles()
	p.help = h

	p.list = list.NewFilterableList()
	p.list.Focus()

	p.input = textinput.New()
	p.input.SetVirtualCursor(false)
	p.input.Placeholder = "Type to filter"
	p.input.SetStyles(com.Styles.TextInput)
	p.input.Focus()

	p.keyMap.Select = key.NewBinding(
		key.WithKeys("enter", "ctrl+y"),
		key.WithHelp("enter", "cycle value"),
	)
	p.keyMap.Next = key.NewBinding(
		key.WithKeys("down", "ctrl+n"),
		key.WithHelp("↓", "next item"),
	)
	p.keyMap.Previous = key.NewBinding(
		key.WithKeys("up", "ctrl+p"),
		key.WithHelp("↑", "previous item"),
	)
	p.keyMap.UpDown = key.NewBinding(
		key.WithKeys("up", "down"),
		key.WithHelp("↑/↓", "choose"),
	)
	p.keyMap.Close = CloseKey

	p.setItems()
	return p
}

// ID implements Dialog.
func (p *Preferences) ID() string {
	return PreferencesID
}

// HandleMsg implements [Dialog].
func (p *Preferences) HandleMsg(msg tea.Msg) Action {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch {
		case key.Matches(msg, p.keyMap.Close):
			if p.addingTool {
				p.cancelAdd()
				return nil
			}
			return ActionClose{}
		case key.Matches(msg, p.keyMap.Previous):
			p.list.Focus()
			if p.list.IsSelectedFirst() {
				p.list.SelectLast()
				p.list.ScrollToBottom()
				break
			}
			p.list.SelectPrev()
			p.list.ScrollToSelected()
		case key.Matches(msg, p.keyMap.Next):
			p.list.Focus()
			if p.list.IsSelectedLast() {
				p.list.SelectFirst()
				p.list.ScrollToTop()
				break
			}
			p.list.SelectNext()
			p.list.ScrollToSelected()
		case key.Matches(msg, p.keyMap.Select):
			if p.addingTool {
				return p.commitAdd()
			}
			selectedItem := p.list.SelectedItem()
			if selectedItem == nil {
				break
			}
			item, ok := selectedItem.(*PreferenceItem)
			if !ok {
				break
			}
			return p.cycle(item)
		default:
			var cmd tea.Cmd
			p.input, cmd = p.input.Update(msg)
			value := p.input.Value()
			if p.addingTool {
				return ActionCmd{cmd}
			}
			p.list.SetFilter(value)
			p.list.ScrollToTop()
			p.list.SetSelected(0)
			// With no matches and typed text, offer the text as a new
			// per-tool output override instead of showing an empty list.
			// The prefix the user already typed is part of the tool name
			// they are adding, so keep the input value in place.
			if value != "" && len(p.list.FilteredItems()) == 0 {
				p.addingTool = true
				p.input.Placeholder = "tool name to override"
			}
			return ActionCmd{cmd}
		}
	}
	return nil
}

// cycle applies the next value for the row in place and returns the action
// that persists it. The dialog stays open so values can be compared side by
// side with the chat behind it.
func (p *Preferences) cycle(item *PreferenceItem) Action {
	switch item.kind {
	case preferenceThinking:
		visible := item.value != "shown"
		item.value = toggleState(visible)
		item.Bump()
		return ActionSetThinkingVisible{Visible: visible}
	case preferenceCompactMode:
		enabled := item.value != "on"
		item.value = onOffState(enabled)
		item.Bump()
		return ActionSetCompactMode{Enabled: enabled}
	case preferenceDefaultOutput:
		mode := nextToolOutputMode(item.value)
		item.value = string(mode)
		item.Bump()
		return ActionSetToolOutputMode{Tool: "", Mode: mode}
	case preferenceLineLimit:
		limit := nextToolOutputLimit(item.value)
		item.value = strconv.Itoa(limit)
		item.Bump()
		return ActionSetToolOutputLimit{Limit: limit}
	case preferenceToolOutput:
		mode := nextToolOutputMode(item.value)
		item.value = string(mode)
		item.Bump()
		return ActionSetToolOutputMode{Tool: item.tool, Mode: mode}
	}
	return nil
}

// cancelAdd returns the input from tool-name entry back to filter mode.
func (p *Preferences) cancelAdd() {
	p.addingTool = false
	p.input.SetValue("")
	p.input.Placeholder = "Type to filter"
	p.list.SetFilter("")
	p.setItems()
}

// commitAdd registers the typed tool name with a per-tool output override
// and returns to filter mode.
func (p *Preferences) commitAdd() Action {
	tool := strings.TrimSpace(p.input.Value())
	if tool == "" {
		p.cancelAdd()
		return nil
	}
	mode := p.currentDefaultMode()
	p.addingTool = false
	p.input.SetValue("")
	p.input.Placeholder = "Type to filter"
	p.list.SetFilter("")
	p.setItems()
	return ActionSetToolOutputMode{Tool: tool, Mode: mode}
}

// currentDefaultMode reads the configured default tool output mode.
func (p *Preferences) currentDefaultMode() config.ToolOutputMode {
	cfg := p.com.Config()
	if cfg == nil || cfg.Options == nil || cfg.Options.TUI == nil {
		return config.DefaultToolOutputMode
	}
	return cfg.Options.TUI.ToolOutput.ModeFor("")
}

// Cursor returns the cursor position relative to the dialog.
func (p *Preferences) Cursor() *tea.Cursor {
	return InputCursor(p.com.Styles, p.input.Cursor())
}

// Draw implements [Dialog].
func (p *Preferences) Draw(scr uv.Screen, area uv.Rectangle) *tea.Cursor {
	t := p.com.Styles
	width := max(0, min(preferencesDialogMaxWidth, area.Dx()))
	height := max(0, min(preferencesDialogMaxHeight, area.Dy()))
	innerWidth := width - t.Dialog.View.GetHorizontalFrameSize()
	heightOffset := t.Dialog.Title.GetVerticalFrameSize() + titleContentHeight +
		t.Dialog.InputPrompt.GetVerticalFrameSize() + inputContentHeight +
		t.Dialog.HelpView.GetVerticalFrameSize() +
		t.Dialog.View.GetVerticalFrameSize()

	p.input.SetWidth(innerWidth - t.Dialog.InputPrompt.GetHorizontalFrameSize() - 1)
	p.list.SetSize(innerWidth, height-heightOffset)
	p.help.SetWidth(innerWidth)

	rc := NewRenderContext(t, width)
	rc.Title = "Display Preferences"
	inputView := t.Dialog.InputPrompt.Render(p.input.View())
	rc.AddPart(inputView)

	visibleCount := len(p.list.FilteredItems())
	if p.list.Height() >= visibleCount {
		p.list.ScrollToTop()
	} else {
		p.list.ScrollToSelected()
	}

	listView := t.Dialog.List.Height(p.list.Height()).Render(p.list.Render())
	rc.AddPart(listView)
	rc.Help = p.help.View(p)

	view := rc.Render()

	cur := p.Cursor()
	DrawCenterCursor(scr, area, view, cur)
	return cur
}

// ShortHelp implements [help.KeyMap].
func (p *Preferences) ShortHelp() []key.Binding {
	return []key.Binding{
		p.keyMap.UpDown,
		p.keyMap.Select,
		p.keyMap.Close,
	}
}

// FullHelp implements [help.KeyMap].
func (p *Preferences) FullHelp() [][]key.Binding {
	m := [][]key.Binding{}
	slice := []key.Binding{
		p.keyMap.Select,
		p.keyMap.Next,
		p.keyMap.Previous,
		p.keyMap.Close,
	}
	for i := 0; i < len(slice); i += 4 {
		end := min(i+4, len(slice))
		m = append(m, slice[i:end])
	}
	return m
}

func (p *Preferences) setItems() {
	// Remember where the user was so callers that rebuild rows after a
	// config change (Refresh) don't jump the selection. The filter term
	// only survives for that path; cancelAdd/commitAdd clear it
	// deliberately before rebuilding.
	prevIdx := p.list.Selected()
	prevFilter := p.input.Value()
	if p.addingTool {
		prevFilter = ""
	}

	cfg := p.com.Config()
	thinkingVisible := true
	compactMode := false
	toolOutput := &config.ToolOutput{}
	if cfg != nil && cfg.Options != nil && cfg.Options.TUI != nil {
		thinkingVisible = cfg.Options.TUI.ThinkingVisible()
		compactMode = cfg.Options.TUI.CompactMode
		if cfg.Options.TUI.ToolOutput != nil {
			toolOutput = cfg.Options.TUI.ToolOutput
		}
	}

	items := make([]list.FilterableItem, 0, 4+len(toolOutput.PerTool))
	items = append(items, &PreferenceItem{
		Versioned: list.NewVersioned(),
		kind:      preferenceThinking,
		value:     toggleState(thinkingVisible),
		t:         p.com.Styles,
	})
	items = append(items, &PreferenceItem{
		Versioned: list.NewVersioned(),
		kind:      preferenceCompactMode,
		value:     onOffState(compactMode),
		t:         p.com.Styles,
	})
	items = append(items, &PreferenceItem{
		Versioned: list.NewVersioned(),
		kind:      preferenceDefaultOutput,
		value:     string(toolOutput.ModeFor("")),
		t:         p.com.Styles,
	})
	items = append(items, &PreferenceItem{
		Versioned: list.NewVersioned(),
		kind:      preferenceLineLimit,
		value:     strconv.Itoa(defaultIfZero(toolOutput.LineLimit(), defaultToolOutputLimit)),
		t:         p.com.Styles,
	})

	tools := make([]string, 0, len(toolOutput.PerTool))
	for tool := range toolOutput.PerTool {
		tools = append(tools, tool)
	}
	sort.Strings(tools)
	for _, tool := range tools {
		items = append(items, &PreferenceItem{
			Versioned: list.NewVersioned(),
			kind:      preferenceToolOutput,
			tool:      tool,
			value:     string(toolOutput.ModeFor(tool)),
			t:         p.com.Styles,
		})
	}

	p.list.SetItems(items...)
	if prevIdx >= 0 && prevIdx < len(items) {
		p.list.SetSelected(prevIdx)
		p.list.ScrollToSelected()
	} else {
		p.list.SetSelected(0)
		p.list.ScrollToSelected()
	}
	if prevFilter != "" && !p.addingTool {
		p.list.SetFilter(prevFilter)
		p.list.ScrollToTop()
		p.list.SetSelected(0)
	}
}

// Refresh rebuilds the preference rows from the current configuration,
// keeping the user's selection. It is called after a preference change that
// can add rows (e.g. a new per-tool output override) so the dialog shows the
// new row without reopening.
func (p *Preferences) Refresh() {
	p.setItems()
}

// Filter returns the filter value for the preference row.
func (p *PreferenceItem) Filter() string {
	switch p.kind {
	case preferenceThinking:
		return "thinking"
	case preferenceCompactMode:
		return "compact mode layout sidebar"
	case preferenceDefaultOutput:
		return "tool output default"
	case preferenceLineLimit:
		return "tool output short line limit"
	default:
		return "tool output " + p.tool
	}
}

// ID returns the unique identifier for the preference row.
func (p *PreferenceItem) ID() string {
	switch p.kind {
	case preferenceThinking:
		return "thinking"
	case preferenceCompactMode:
		return "compact_mode"
	case preferenceDefaultOutput:
		return "tool_output_default"
	case preferenceLineLimit:
		return "tool_output_limit"
	default:
		return "tool_output_" + p.tool
	}
}

// SetFocused sets the focus state of the preference row.
func (p *PreferenceItem) SetFocused(focused bool) {
	if p.focused == focused {
		return
	}
	p.cache = nil
	p.focused = focused
	if p.Versioned != nil {
		p.Bump()
	}
}

// SetMatch sets the fuzzy match for the preference row.
func (p *PreferenceItem) SetMatch(m fuzzy.Match) {
	if sameFuzzyMatch(p.m, m) {
		return
	}
	p.cache = nil
	p.m = m
	if p.Versioned != nil {
		p.Bump()
	}
}

// Render returns the string representation of the preference row.
func (p *PreferenceItem) Render(width int) string {
	var title string
	switch p.kind {
	case preferenceThinking:
		title = "Thinking"
	case preferenceCompactMode:
		title = "Compact layout"
	case preferenceDefaultOutput:
		title = "Tool output (default)"
	case preferenceLineLimit:
		title = "Short output line limit"
	default:
		title = "Tool output (" + p.tool + ")"
	}
	info := p.value
	st := ListItemStyles{
		ItemBlurred:     p.t.Dialog.NormalItem,
		ItemFocused:     p.t.Dialog.SelectedItem,
		InfoTextBlurred: p.t.Dialog.ListItem.InfoBlurred,
		InfoTextFocused: p.t.Dialog.ListItem.InfoFocused,
	}
	return renderItem(st, title, info, p.focused, width, p.cache, &p.m)
}

// toggleState maps a bool to its display value.
func toggleState(on bool) string {
	if on {
		return "shown"
	}
	return "hidden"
}

// onOffState maps a bool to its on/off display value.
func onOffState(on bool) string {
	if on {
		return "on"
	}
	return "off"
}

// nextToolOutputMode cycles through the display modes for a tool output row.
func nextToolOutputMode(current string) config.ToolOutputMode {
	modes := config.ToolOutputDisplayModes
	for i, m := range modes {
		if string(m) == current {
			return modes[(i+1)%len(modes)]
		}
	}
	return config.DefaultToolOutputMode
}

// toolOutputLimitPresets lists the selectable short-mode line caps. The
// default cap (10) is the UI fallback used when nothing is configured.
var toolOutputLimitPresets = []int{10, 20, 30, 50, 100}

// defaultToolOutputLimit mirrors the UI default cap used when no limit is
// configured.
const defaultToolOutputLimit = 10

// defaultIfZero returns fallback when v is zero.
func defaultIfZero(v, fallback int) int {
	if v == 0 {
		return fallback
	}
	return v
}

// nextToolOutputLimit cycles through the selectable short-mode line caps.
func nextToolOutputLimit(current string) int {
	currentN, err := strconv.Atoi(current)
	if err != nil {
		currentN = toolOutputLimitPresets[0]
	}
	for i, limit := range toolOutputLimitPresets {
		if limit == currentN {
			return toolOutputLimitPresets[(i+1)%len(toolOutputLimitPresets)]
		}
	}
	return toolOutputLimitPresets[0]
}
