package dialog

import (
	"fmt"

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
	// PruneDaysID is the identifier for the prune days selection dialog.
	PruneDaysID          = "prune_days"
	pruneDialogMaxWidth  = 50
	pruneDialogMaxHeight = 14
)

// PruneDays represents a dialog for selecting the number of days to prune.
type PruneDays struct {
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

// PruneDaysItem represents a prune days list item.
type PruneDaysItem struct {
	*list.Versioned
	days    int
	title   string
	t       *styles.Styles
	m       fuzzy.Match
	cache   map[int]string
	focused bool
}

// Finished implements list.Item. PruneDays items are render-stable
// outside of explicit SetFocused / SetMatch.
func (p *PruneDaysItem) Finished() bool {
	return true
}

var (
	_ Dialog   = (*PruneDays)(nil)
	_ ListItem = (*PruneDaysItem)(nil)
)

// NewPruneDays creates a new prune days selection dialog.
func NewPruneDays(com *common.Common) (*PruneDays, error) {
	p := &PruneDays{com: com}

	help := help.New()
	help.Styles = com.Styles.DialogHelpStyles()
	p.help = help

	p.list = list.NewFilterableList()
	p.list.Focus()

	p.input = textinput.New()
	p.input.SetVirtualCursor(false)
	p.input.Placeholder = "Type to filter"
	p.input.SetStyles(com.Styles.TextInput)
	p.input.Focus()

	p.keyMap.Select = key.NewBinding(
		key.WithKeys("enter", "ctrl+y"),
		key.WithHelp("enter", "confirm"),
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

	// Set the days options: 7, 14, 30, 60, 90
	daysOptions := []int{7, 14, 30, 60, 90}
	items := make([]list.FilterableItem, 0, len(daysOptions))
	for _, days := range daysOptions {
		title := formatDays(days)
		item := &PruneDaysItem{
			Versioned: list.NewVersioned(),
			days:      days,
			title:     title,
			t:         com.Styles,
		}
		items = append(items, item)
	}

	p.list.SetItems(items...)
	p.list.SetSelected(2) // Default to 30 days
	p.list.ScrollToSelected()

	return p, nil
}

// ID implements Dialog.
func (p *PruneDays) ID() string {
	return PruneDaysID
}

// HandleMsg implements [Dialog].
func (p *PruneDays) HandleMsg(msg tea.Msg) Action {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch {
		case key.Matches(msg, p.keyMap.Close):
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
			selectedItem := p.list.SelectedItem()
			if selectedItem == nil {
				break
			}
			pruneItem, ok := selectedItem.(*PruneDaysItem)
			if !ok {
				break
			}
			return ActionPruneDays{Days: pruneItem.days}
		default:
			var cmd tea.Cmd
			p.input, cmd = p.input.Update(msg)
			value := p.input.Value()
			p.list.SetFilter(value)
			p.list.ScrollToTop()
			p.list.SetSelected(0)
			return ActionCmd{cmd}
		}
	}
	return nil
}

// Cursor returns the cursor position relative to the dialog.
func (p *PruneDays) Cursor() *tea.Cursor {
	return InputCursor(p.com.Styles, p.input.Cursor())
}

// Draw implements [Dialog].
func (p *PruneDays) Draw(scr uv.Screen, area uv.Rectangle) *tea.Cursor {
	t := p.com.Styles
	width := max(0, min(pruneDialogMaxWidth, area.Dx()))
	height := max(0, min(pruneDialogMaxHeight, area.Dy()))
	innerWidth := width - t.Dialog.View.GetHorizontalFrameSize()
	heightOffset := t.Dialog.Title.GetVerticalFrameSize() + titleContentHeight +
		t.Dialog.InputPrompt.GetVerticalFrameSize() + inputContentHeight +
		t.Dialog.HelpView.GetVerticalFrameSize() +
		t.Dialog.View.GetVerticalFrameSize()

	p.input.SetWidth(innerWidth - t.Dialog.InputPrompt.GetHorizontalFrameSize() - 1)
	p.list.SetSize(innerWidth, height-heightOffset)
	p.help.SetWidth(innerWidth)

	rc := NewRenderContext(t, width)
	rc.Title = "Prune Old Sessions"
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
func (p *PruneDays) ShortHelp() []key.Binding {
	return []key.Binding{
		p.keyMap.UpDown,
		p.keyMap.Select,
		p.keyMap.Close,
	}
}

// FullHelp implements [help.KeyMap].
func (p *PruneDays) FullHelp() [][]key.Binding {
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

// Filter returns the filter value for the prune days item.
func (p *PruneDaysItem) Filter() string {
	return p.title
}

// ID returns the unique identifier for the prune days item.
func (p *PruneDaysItem) ID() string {
	return p.title
}

// SetFocused sets the focus state of the prune days item.
func (p *PruneDaysItem) SetFocused(focused bool) {
	if p.focused == focused {
		return
	}
	p.cache = nil
	p.focused = focused
	if p.Versioned != nil {
		p.Bump()
	}
}

// SetMatch sets the fuzzy match for the prune days item.
func (p *PruneDaysItem) SetMatch(m fuzzy.Match) {
	if sameFuzzyMatch(p.m, m) {
		return
	}
	p.cache = nil
	p.m = m
	if p.Versioned != nil {
		p.Bump()
	}
}

// Render returns the string representation of the prune days item.
func (p *PruneDaysItem) Render(width int) string {
	styles := ListItemStyles{
		ItemBlurred:     p.t.Dialog.NormalItem,
		ItemFocused:     p.t.Dialog.SelectedItem,
		InfoTextBlurred: p.t.Dialog.ListItem.InfoBlurred,
		InfoTextFocused: p.t.Dialog.ListItem.InfoFocused,
	}
	return renderItem(styles, p.title, "", p.focused, width, p.cache, &p.m)
}

func formatDays(days int) string {
	if days == 1 {
		return "1 day"
	}
	return fmt.Sprintf("%d days", days)
}
