package dialog

import (
	"sort"
	"strings"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/hackafterdark/phosphor/internal/ui/common"
	"github.com/hackafterdark/phosphor/internal/ui/list"
	"github.com/hackafterdark/phosphor/internal/ui/styles"
	"github.com/sahilm/fuzzy"
)

const (
	// ThemesID is the identifier for the theme selector dialog.
	ThemesID              = "themes"
	themesDialogMaxWidth  = 60
	themesDialogMaxHeight = 16
)

// Themes represents a dialog for selecting the active theme. The list
// is filterable so long theme collections stay navigable.
type Themes struct {
	com   *common.Common
	help  help.Model
	list  *list.FilterableList
	input textinput.Model

	keyMap struct {
		Select   key.Binding
		Next     key.Binding
		Previous key.Binding
		UpDown   key.Binding
		Close    key.Binding
	}
}

// ThemeItem represents a single theme row.
type ThemeItem struct {
	*list.Versioned
	name    string
	source  string
	current bool
	t       *styles.Styles
	m       fuzzy.Match
	cache   map[int]string
	focused bool
}

// Finished implements list.Item. Theme rows are render-stable outside
// of explicit SetFocused / SetMatch.
func (t *ThemeItem) Finished() bool {
	return true
}

var (
	_ Dialog   = (*Themes)(nil)
	_ ListItem = (*ThemeItem)(nil)
)

// NewThemes creates a new theme selector dialog.
func NewThemes(com *common.Common) *Themes {
	t := &Themes{com: com}

	h := help.New()
	h.Styles = com.Styles.DialogHelpStyles()
	t.help = h

	t.list = list.NewFilterableList()
	t.list.Focus()

	t.input = textinput.New()
	t.input.SetVirtualCursor(false)
	t.input.Placeholder = "Type to filter"
	t.input.SetStyles(com.Styles.TextInput)
	t.input.Focus()

	t.keyMap.Select = key.NewBinding(
		key.WithKeys("enter", "ctrl+y"),
		key.WithHelp("enter", "select theme"),
	)
	t.keyMap.Next = key.NewBinding(
		key.WithKeys("down", "ctrl+n"),
		key.WithHelp("↓", "next theme"),
	)
	t.keyMap.Previous = key.NewBinding(
		key.WithKeys("up", "ctrl+p"),
		key.WithHelp("↑", "previous theme"),
	)
	t.keyMap.UpDown = key.NewBinding(
		key.WithKeys("up", "down"),
		key.WithHelp("↑/↓", "choose"),
	)
	t.keyMap.Close = CloseKey

	t.setItems()
	return t
}

// ID implements Dialog.
func (t *Themes) ID() string {
	return ThemesID
}

// HandleMsg implements [Dialog].
func (t *Themes) HandleMsg(msg tea.Msg) Action {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch {
		case key.Matches(msg, t.keyMap.Close):
			return ActionClose{}
		case key.Matches(msg, t.keyMap.Previous):
			t.list.Focus()
			if t.list.IsSelectedFirst() {
				t.list.SelectLast()
				t.list.ScrollToBottom()
				break
			}
			t.list.SelectPrev()
			t.list.ScrollToSelected()
		case key.Matches(msg, t.keyMap.Next):
			t.list.Focus()
			if t.list.IsSelectedLast() {
				t.list.SelectFirst()
				t.list.ScrollToTop()
				break
			}
			t.list.SelectNext()
			t.list.ScrollToSelected()
		case key.Matches(msg, t.keyMap.Select):
			selectedItem := t.list.SelectedItem()
			if selectedItem == nil {
				break
			}
			item, ok := selectedItem.(*ThemeItem)
			if !ok {
				break
			}
			return ActionSetTheme{Name: item.name}
		default:
			var cmd tea.Cmd
			t.input, cmd = t.input.Update(msg)
			t.list.SetFilter(t.input.Value())
			t.list.ScrollToTop()
			t.list.SetSelected(0)
			return ActionCmd{cmd}
		}
	}
	return nil
}

// Cursor returns the cursor position relative to the dialog.
func (t *Themes) Cursor() *tea.Cursor {
	return InputCursor(t.com.Styles, t.input.Cursor())
}

// Draw implements [Dialog].
func (t *Themes) Draw(scr uv.Screen, area uv.Rectangle) *tea.Cursor {
	st := t.com.Styles
	width := max(0, min(themesDialogMaxWidth, area.Dx()))
	height := max(0, min(themesDialogMaxHeight, area.Dy()))
	innerWidth := width - st.Dialog.View.GetHorizontalFrameSize()
	heightOffset := st.Dialog.Title.GetVerticalFrameSize() + titleContentHeight +
		st.Dialog.InputPrompt.GetVerticalFrameSize() + inputContentHeight +
		st.Dialog.HelpView.GetVerticalFrameSize() +
		st.Dialog.View.GetVerticalFrameSize()

	t.input.SetWidth(innerWidth - st.Dialog.InputPrompt.GetHorizontalFrameSize() - 1)
	t.list.SetSize(innerWidth, height-heightOffset)
	t.help.SetWidth(innerWidth)

	rc := NewRenderContext(st, width)
	rc.Title = "Themes"
	inputView := st.Dialog.InputPrompt.Render(t.input.View())
	rc.AddPart(inputView)

	visibleCount := len(t.list.FilteredItems())
	if t.list.Height() >= visibleCount {
		t.list.ScrollToTop()
	} else {
		t.list.ScrollToSelected()
	}

	listView := st.Dialog.List.Height(t.list.Height()).Render(t.list.Render())
	rc.AddPart(listView)
	rc.Help = t.help.View(t)

	view := rc.Render()

	cur := t.Cursor()
	DrawCenterCursor(scr, area, view, cur)
	return cur
}

// ShortHelp implements [help.KeyMap].
func (t *Themes) ShortHelp() []key.Binding {
	return []key.Binding{
		t.keyMap.UpDown,
		t.keyMap.Select,
		t.keyMap.Close,
	}
}

// FullHelp implements [help.KeyMap].
func (t *Themes) FullHelp() [][]key.Binding {
	m := [][]key.Binding{}
	slice := []key.Binding{
		t.keyMap.Select,
		t.keyMap.Next,
		t.keyMap.Previous,
		t.keyMap.Close,
	}
	for i := 0; i < len(slice); i += 4 {
		end := min(i+4, len(slice))
		m = append(m, slice[i:end])
	}
	return m
}

// currentTheme returns the configured theme name, or "" when the
// provider-based default theme is active.
func (t *Themes) currentTheme() string {
	cfg := t.com.Config()
	if cfg == nil || cfg.Options == nil || cfg.Options.TUI == nil {
		return ""
	}
	return cfg.Options.TUI.Theme
}

func (t *Themes) setItems() {
	current := t.currentTheme()

	items := make([]list.FilterableItem, 0, 8)
	// Explicit return to the provider-based default theme.
	items = append(items, &ThemeItem{
		Versioned: list.NewVersioned(),
		name:      "",
		source:    "default",
		current:   current == "",
		t:         t.com.Styles,
	})

	workingDir := ""
	if t.com.Workspace != nil {
		workingDir = t.com.Workspace.WorkingDir()
	}
	available := styles.ListThemes(workingDir)
	sort.SliceStable(available, func(i, j int) bool {
		if available[i].Source != available[j].Source {
			return available[i].Source < available[j].Source
		}
		return available[i].Name < available[j].Name
	})
	for _, theme := range available {
		items = append(items, &ThemeItem{
			Versioned: list.NewVersioned(),
			name:      theme.Name,
			source:    theme.Source,
			current:   strings.EqualFold(theme.Name, current),
			t:         t.com.Styles,
		})
	}

	presetIdx := 0
	for i, item := range items {
		if ti, ok := item.(*ThemeItem); ok && ti.current {
			presetIdx = i
			break
		}
	}
	t.list.SetItems(items...)
	t.list.SetSelected(presetIdx)
	t.list.ScrollToSelected()
}

// Filter returns the filter value for the theme row.
func (t *ThemeItem) Filter() string {
	return t.name + " " + t.source
}

// ID returns the unique identifier for the theme row.
func (t *ThemeItem) ID() string {
	if t.name == "" {
		return "theme_default"
	}
	return "theme_" + t.name
}

// SetFocused sets the focus state of the theme row.
func (t *ThemeItem) SetFocused(focused bool) {
	if t.focused == focused {
		return
	}
	t.cache = nil
	t.focused = focused
	if t.Versioned != nil {
		t.Bump()
	}
}

// SetMatch sets the fuzzy match for the theme row.
func (t *ThemeItem) SetMatch(m fuzzy.Match) {
	if sameFuzzyMatch(t.m, m) {
		return
	}
	t.cache = nil
	t.m = m
	if t.Versioned != nil {
		t.Bump()
	}
}

// Render returns the string representation of the theme row.
func (t *ThemeItem) Render(width int) string {
	title := t.name
	if title == "" {
		title = "(default)"
	}
	if t.current {
		title += " (current)"
	}
	info := t.source
	st := ListItemStyles{
		ItemBlurred:     t.t.Dialog.NormalItem,
		ItemFocused:     t.t.Dialog.SelectedItem,
		InfoTextBlurred: t.t.Dialog.ListItem.InfoBlurred,
		InfoTextFocused: t.t.Dialog.ListItem.InfoFocused,
	}
	return renderItem(st, title, info, t.focused, width, t.cache, &t.m)
}
