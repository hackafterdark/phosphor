package chat

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/hackafterdark/phosphor/internal/ui/list"
	"github.com/hackafterdark/phosphor/internal/ui/styles"
)

// LoadMoreMessagesMsg is sent when the load more item is activated.
type LoadMoreMessagesMsg struct{}

// LoadMoreItem renders a divider banner that loads more messages when clicked or selected and activated.
type LoadMoreItem struct {
	*list.Versioned
	sty       *styles.Styles
	remaining int
	loading   bool
	focused   bool
}

var _ MessageItem = (*LoadMoreItem)(nil)
var _ list.Focusable = (*LoadMoreItem)(nil)
var _ list.MouseClickable = (*LoadMoreItem)(nil)
var _ KeyEventHandler = (*LoadMoreItem)(nil)

// NewLoadMoreItem creates a new LoadMoreItem.
func NewLoadMoreItem(sty *styles.Styles, remaining int) *LoadMoreItem {
	return &LoadMoreItem{
		Versioned: list.NewVersioned(),
		sty:       sty,
		remaining: remaining,
	}
}

// Finished implements list.Item.
func (l *LoadMoreItem) Finished() bool {
	return true
}

// ID implements Identifiable.
func (l *LoadMoreItem) ID() string {
	return "load-more"
}

// SetRemaining updates the number of remaining messages.
func (l *LoadMoreItem) SetRemaining(remaining int) {
	if l.remaining == remaining {
		return
	}
	l.remaining = remaining
	l.Bump()
}

// SetFocused implements list.Focusable.
func (l *LoadMoreItem) SetFocused(focused bool) {
	if l.focused == focused {
		return
	}
	l.focused = focused
	l.Bump()
}

// HandleMouseClick implements list.MouseClickable.
func (l *LoadMoreItem) HandleMouseClick(btn ansi.MouseButton, x, y int) bool {
	return !l.loading
}

// HandleKeyEvent implements KeyEventHandler.
func (l *LoadMoreItem) HandleKeyEvent(key tea.KeyMsg) (bool, tea.Cmd) {
	if key.String() == "enter" {
		if cmd := l.Trigger(); cmd != nil {
			return true, cmd
		}
	}
	return false, nil
}

// Trigger triggers the loading state and returns a command to load more messages.
func (l *LoadMoreItem) Trigger() tea.Cmd {
	if l.loading {
		return nil
	}
	l.loading = true
	l.Bump()
	return func() tea.Msg {
		return LoadMoreMessagesMsg{}
	}
}

// RawRender implements list.RawRenderable.
func (l *LoadMoreItem) RawRender(width int) string {
	return l.Render(width)
}

// Render implements list.Item.
func (l *LoadMoreItem) Render(width int) string {
	innerWidth := max(0, width-MessageLeftPaddingTotal)
	prefix := l.sty.Messages.SectionHeader.Render()

	var text string
	if l.loading {
		text = "  ⟳ Loading...  "
	} else {
		text = fmt.Sprintf("  ⏶ Load previous messages (%d remaining)  ", l.remaining)
	}

	textStyle := l.sty.Section.Title
	if l.focused {
		textStyle = textStyle.Bold(true).Foreground(l.sty.Editor.PromptNormalFocused.GetForeground())
	} else {
		textStyle = textStyle.Foreground(l.sty.Help.ShortDesc.GetForeground())
	}
	styledText := textStyle.Render(text)

	textLen := lipgloss.Width(styledText)
	if innerWidth > textLen+6 {
		lineLen := (innerWidth - textLen) / 2
		lineStyle := l.sty.Section.Line
		if l.focused {
			lineStyle = lineStyle.Foreground(l.sty.Editor.PromptNormalFocused.GetForeground())
		}
		line := lineStyle.Render(strings.Repeat("─", lineLen))
		rightLine := line
		if (innerWidth-textLen)%2 != 0 {
			rightLine += lineStyle.Render("─")
		}
		return prefix + line + styledText + rightLine
	}

	return prefix + styledText
}
