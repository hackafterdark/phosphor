// Package dialog provides the codebase index management dialog.
package dialog

import (
	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/hackafterdark/phosphor/internal/ui/common"
	"github.com/hackafterdark/phosphor/internal/ui/list"
	"github.com/hackafterdark/phosphor/internal/ui/styles"
)

// CodebaseIndexID is the identifier for the codebase index management dialog.
const CodebaseIndexID = "codebase_index"

// CodebaseIndexItem is a list item for codebase index actions.
type CodebaseIndexItem struct {
	*list.Versioned
	Title   string
	ID      string
	filter  string
	Action  Action
	t       *styles.Styles
	toggled bool // true when this is the auto-update item and auto-update is ON
	focused bool
}

// Render returns the rendered representation of the item.
func (i *CodebaseIndexItem) Render(width int) string {
	title := i.Title
	if i.ID == "auto_update" && i.toggled {
		title = "Auto-Update: ON"
	} else if i.ID == "auto_update" {
		title = "Auto-Update: OFF"
	}

	if i.focused {
		return i.t.Dialog.SelectedItem.Render(title)
	}
	return i.t.Dialog.NormalItem.Render(title)
}

// Version returns the version of the item.
func (i *CodebaseIndexItem) Version() uint64 {
	return i.Versioned.Version()
}

// Finished reports that the item is in a terminal state.
func (i *CodebaseIndexItem) Finished() bool {
	return true
}

// Filter returns the filter string for the item.
func (i *CodebaseIndexItem) Filter() string {
	return i.filter
}

// SetFocused updates the focus state and bumps the version.
func (i *CodebaseIndexItem) SetFocused(focused bool) {
	if i.focused == focused {
		return
	}
	i.focused = focused
	i.Bump()
}

// CodebaseIndexDialog manages codebase indexing options.
type CodebaseIndexDialog struct {
	com  *common.Common
	list *list.List

	keyMap struct {
		Select,
		Up,
		Down,
		Close key.Binding
	}
	help help.Model
}

// NewCodebaseIndexDialog creates a new codebase index management dialog.
func NewCodebaseIndexDialog(com *common.Common) *CodebaseIndexDialog {
	d := &CodebaseIndexDialog{com: com}
	d.list = list.NewList()

	autoUpdate := false
	if com.Config().WorkspaceSearch != nil && com.Config().WorkspaceSearch.VectorEmbeddings != nil {
		autoUpdate = com.Config().WorkspaceSearch.VectorEmbeddings.AutoIndex
	}
	d.list.SetItems(
		&CodebaseIndexItem{Versioned: list.NewVersioned(), Title: "Index Codebase", ID: "index", filter: "index codebase", Action: ActionIndexCodebase{}, t: com.Styles},
		&CodebaseIndexItem{Versioned: list.NewVersioned(), Title: "Clear Index", ID: "clear", filter: "clear index", Action: ActionClearCodebaseIndex{}, t: com.Styles},
		&CodebaseIndexItem{Versioned: list.NewVersioned(), Title: "Toggle Auto-Update", ID: "auto_update", filter: "auto update", Action: ActionToggleCodebaseAutoUpdate{}, t: com.Styles, toggled: autoUpdate},
	)
	d.list.RegisterRenderCallback(func(idx, selectedIdx int, item list.Item) list.Item {
		if ci, ok := item.(*CodebaseIndexItem); ok {
			ci.SetFocused(idx == selectedIdx)
		}
		return item
	})
	d.list.SetSelected(0)

	d.keyMap.Select = key.NewBinding(
		key.WithKeys("enter", "ctrl+y"),
		key.WithHelp("enter", "select"),
	)
	d.keyMap.Up = key.NewBinding(
		key.WithKeys("up"),
		key.WithHelp("↑", "prev"),
	)
	d.keyMap.Down = key.NewBinding(
		key.WithKeys("down"),
		key.WithHelp("↓", "next"),
	)
	d.keyMap.Close = CloseKey

	help := help.New()
	help.Styles = com.Styles.DialogHelpStyles()
	d.help = help

	return d
}

// ID returns the dialog identifier.
func (d *CodebaseIndexDialog) ID() string {
	return CodebaseIndexID
}

// HandleMsg processes messages and returns actions.
func (d *CodebaseIndexDialog) HandleMsg(msg tea.Msg) Action {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch {
		case key.Matches(msg, d.keyMap.Close):
			return ActionClose{}
		case key.Matches(msg, d.keyMap.Up):
			d.list.Focus()
			if d.list.IsSelectedFirst() {
				d.list.SelectLast()
			} else {
				d.list.SelectPrev()
			}
			d.list.ScrollToSelected()
		case key.Matches(msg, d.keyMap.Down):
			d.list.Focus()
			if d.list.IsSelectedLast() {
				d.list.SelectFirst()
			} else {
				d.list.SelectNext()
			}
			d.list.ScrollToSelected()
		case key.Matches(msg, d.keyMap.Select):
			if item, ok := d.list.SelectedItem().(*CodebaseIndexItem); ok && item != nil {
				return item.Action
			}
		}
	}
	return nil
}

// Draw renders the dialog.
func (d *CodebaseIndexDialog) Draw(scr uv.Screen, area uv.Rectangle) *tea.Cursor {
	t := d.com.Styles
	width := max(0, min(55, area.Dx()-t.Dialog.View.GetHorizontalBorderSize()))
	height := max(0, min(10, area.Dy()-t.Dialog.View.GetVerticalBorderSize()))
	d.list.SetSize(max(0, width-t.Dialog.View.GetHorizontalFrameSize()), height)

	rc := NewRenderContext(t, width)
	rc.Title = "Codebase Index"
	rc.AddPart(d.list.Render())

	DrawCenter(scr, area, rc.Render())
	return nil
}

// ActionClearCodebaseIndex clears the codebase index.
type ActionClearCodebaseIndex struct{}

// ActionToggleCodebaseAutoUpdate toggles automatic index updates.
type ActionToggleCodebaseAutoUpdate struct{}
