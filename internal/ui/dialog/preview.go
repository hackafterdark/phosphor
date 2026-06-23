package dialog

import (
	"fmt"
	"os"
	"strings"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/hackafterdark/phosphor/internal/message"
	"github.com/hackafterdark/phosphor/internal/stringext"
	"github.com/hackafterdark/phosphor/internal/ui/common"
)

// Preview dialog sizing constants.
const (
	// PreviewID is the identifier for the attachment preview dialog.
	PreviewID = "preview"
	// previewDialogMaxWidth is the maximum width for the preview dialog.
	previewDialogMaxWidth = 80
	// previewDialogMaxHeight is the maximum height for the preview dialog.
	previewDialogMaxHeight = 30
)

// Preview represents a dialog for previewing attachment content.
type Preview struct {
	com    *common.Common
	attach message.Attachment

	// Paste navigation state.
	pasteAttachments []message.Attachment
	pasteIdx         int
	fullscreen       bool
	lastCursor       *tea.Cursor

	textarea textarea.Model

	help   help.Model
	keyMap struct {
		Close            key.Binding
		ToggleFullscreen key.Binding
		ScrollUp         key.Binding
		ScrollDown       key.Binding
		PastePrev        key.Binding
		PasteNext        key.Binding
		PasteNav         key.Binding
	}
}

var _ Dialog = (*Preview)(nil)

// NewPreview creates a new attachment preview dialog.
func NewPreview(com *common.Common, attach message.Attachment, pasteAttachments []message.Attachment) *Preview {
	h := help.New()
	h.Styles = com.Styles.DialogHelpStyles()

	pasteIdx := 0
	for i, at := range pasteAttachments {
		if at.FilePath == attach.FilePath {
			pasteIdx = i
			break
		}
	}

	p := &Preview{
		com:              com,
		attach:           attach,
		pasteAttachments: pasteAttachments,
		pasteIdx:         pasteIdx,
		help:             h,
	}

	p.keyMap.Close = CloseKey
	p.keyMap.ToggleFullscreen = key.NewBinding(
		key.WithKeys("alt+f"),
		key.WithHelp("alt+f", "toggle fullscreen"),
	)
	p.keyMap.ScrollUp = key.NewBinding(
		key.WithKeys("shift+up"),
		key.WithHelp("shift+up", "scroll up"),
	)
	p.keyMap.ScrollDown = key.NewBinding(
		key.WithKeys("shift+down"),
		key.WithHelp("shift+down", "scroll down"),
	)
	p.keyMap.PastePrev = key.NewBinding(
		key.WithKeys("shift+pgup"),
		key.WithHelp("shift+pgup", "prev paste"),
	)
	p.keyMap.PasteNext = key.NewBinding(
		key.WithKeys("shift+pgdown"),
		key.WithHelp("shift+pgdn", "next paste"),
	)
	p.keyMap.PasteNav = key.NewBinding(
		key.WithKeys("shift+pgup", "shift+pgdown"),
		key.WithHelp("shift+pgup/pgdn", "prev/next paste"),
	)

	ta := textarea.New()

	// Configure background color for all textarea elements to match the dialog.
	bgCol := com.Styles.Dialog.ContentPanel.GetBackground()
	taStyles := com.Styles.Editor.Textarea
	taStyles.Focused.Base = taStyles.Focused.Base.Background(bgCol)
	taStyles.Focused.Text = taStyles.Focused.Text.Background(bgCol)
	taStyles.Focused.CursorLine = taStyles.Focused.CursorLine.Background(bgCol)
	taStyles.Focused.Placeholder = taStyles.Focused.Placeholder.Background(bgCol)
	taStyles.Blurred.Base = taStyles.Blurred.Base.Background(bgCol)
	taStyles.Blurred.Text = taStyles.Blurred.Text.Background(bgCol)
	taStyles.Blurred.CursorLine = taStyles.Blurred.CursorLine.Background(bgCol)
	taStyles.Blurred.Placeholder = taStyles.Blurred.Placeholder.Background(bgCol)

	ta.SetStyles(taStyles)
	ta.Prompt = ""
	ta.ShowLineNumbers = false
	ta.CharLimit = 1000000 // 1MB text limit
	ta.Focus()
	ta.SetVirtualCursor(false)
	p.textarea = ta
	p.initTextareaContent()

	return p
}

func (p *Preview) initTextareaContent() {
	plainText := stringext.NormalizeSpace(strings.ReplaceAll(ansi.Strip(string(p.attach.Content)), "\x00", ""))
	p.textarea.SetValue(plainText)
	p.textarea.MoveToBegin()
}

func (p *Preview) saveCurrentContent() {
	newVal := p.textarea.Value()
	p.attach.Content = []byte(newVal)
	if p.pasteIdx >= 0 && p.pasteIdx < len(p.pasteAttachments) {
		p.pasteAttachments[p.pasteIdx].Content = p.attach.Content
	}
	if p.attach.FilePath != "" {
		_ = os.WriteFile(p.attach.FilePath, p.attach.Content, 0o600)
	}
}

// ID implements Dialog.
func (p *Preview) ID() string {
	return PreviewID
}

// HandleMsg implements Dialog.
func (p *Preview) HandleMsg(msg tea.Msg) Action {
	switch msg := msg.(type) {
	case message.Attachment:
		p.saveCurrentContent()
		if strings.HasPrefix(msg.FileName, "paste_") && strings.HasSuffix(msg.FileName, ".txt") {
			p.pasteAttachments = append(p.pasteAttachments, msg)
			p.pasteIdx = len(p.pasteAttachments) - 1
			p.attach = msg
			p.initTextareaContent()
		}
	case tea.KeyPressMsg:
		switch {
		case key.Matches(msg, p.keyMap.Close):
			p.saveCurrentContent()
			return ActionClose{}
		case key.Matches(msg, p.keyMap.ToggleFullscreen):
			p.fullscreen = !p.fullscreen
		case key.Matches(msg, p.keyMap.ScrollUp):
			p.textarea.CursorUp()
		case key.Matches(msg, p.keyMap.ScrollDown):
			p.textarea.CursorDown()
		case key.Matches(msg, p.keyMap.PasteNext):
			p.saveCurrentContent()
			p.pasteIdx = min(p.pasteIdx+1, len(p.pasteAttachments)-1)
			p.attach = p.pasteAttachments[p.pasteIdx]
			p.initTextareaContent()
		case key.Matches(msg, p.keyMap.PastePrev):
			p.saveCurrentContent()
			p.pasteIdx = max(p.pasteIdx-1, 0)
			p.attach = p.pasteAttachments[p.pasteIdx]
			p.initTextareaContent()
		default:
			var cmd tea.Cmd
			p.textarea, cmd = p.textarea.Update(msg)

			newVal := p.textarea.Value()
			if newVal != string(p.attach.Content) {
				p.attach.Content = []byte(newVal)
				if p.pasteIdx >= 0 && p.pasteIdx < len(p.pasteAttachments) {
					p.pasteAttachments[p.pasteIdx].Content = p.attach.Content
				}
				if p.attach.FilePath != "" {
					_ = os.WriteFile(p.attach.FilePath, p.attach.Content, 0o600)
				}
				return ActionCmd{
					Cmd: tea.Batch(
						cmd,
						func() tea.Msg {
							return ActionUpdateAttachment{
								FilePath: p.attach.FilePath,
								Content:  p.attach.Content,
							}
						},
					),
				}
			}
			return ActionCmd{Cmd: cmd}
		}
	case tea.MouseWheelMsg:
		switch msg.Button {
		case tea.MouseWheelUp:
			for i := 0; i < 3; i++ {
				p.textarea.CursorUp()
			}
		case tea.MouseWheelDown:
			for i := 0; i < 3; i++ {
				p.textarea.CursorDown()
			}
		}
	default:
		var cmd tea.Cmd
		p.textarea, cmd = p.textarea.Update(msg)
		return ActionCmd{Cmd: cmd}
	}
	return nil
}

// Cursor returns the cursor position relative to the dialog.
func (p *Preview) Cursor() *tea.Cursor {
	return p.lastCursor
}

// PasteTitle returns the dialog title with paste index info.
func (p *Preview) pasteTitle() string {
	if len(p.pasteAttachments) <= 1 {
		return "Preview: " + p.attach.FileName
	}
	return fmt.Sprintf("Preview: %s (%d/%d)", p.attach.FileName, p.pasteIdx+1, len(p.pasteAttachments))
}

// ShortHelp implements [help.KeyMap].
func (p *Preview) ShortHelp() []key.Binding {
	bindings := []key.Binding{p.keyMap.Close, p.keyMap.ToggleFullscreen, p.keyMap.ScrollUp, p.keyMap.ScrollDown}
	if len(p.pasteAttachments) > 1 {
		bindings = append(bindings, p.keyMap.PasteNav)
	}
	return bindings
}

// FullHelp implements [help.KeyMap].
func (p *Preview) FullHelp() [][]key.Binding {
	return [][]key.Binding{p.ShortHelp()}
}

// visualLineCount calculates the total wrapped visual lines of the text area.
func (p *Preview) visualLineCount(width int) int {
	text := p.textarea.Value()
	wrapped := wrapText(text, width)
	if wrapped == "" {
		return 0
	}
	return strings.Count(wrapped, "\n") + 1
}

// Draw implements Dialog.
func (p *Preview) Draw(scr uv.Screen, area uv.Rectangle) *tea.Cursor {
	t := p.com.Styles
	forceFullscreen := area.Dx() <= minWindowWidth || area.Dy() <= minWindowHeight

	var width, height int
	if forceFullscreen || p.fullscreen {
		width = area.Dx()
		height = area.Dy()
	} else {
		width = max(0, min(previewDialogMaxWidth, area.Dx()))
		height = max(0, min(previewDialogMaxHeight, area.Dy()))
	}

	dialogStyle := t.Dialog.View.Width(width).Padding(0, 1)

	contentWidth := p.calculateContentWidth(width)
	titleView := p.renderTitle(contentWidth)
	helpView := p.help.View(p)

	// Calculate available height for content.
	titleHeight := lipgloss.Height(titleView)
	helpHeight := lipgloss.Height(helpView)
	frameHeight := dialogStyle.GetVerticalFrameSize() + layoutSpacingLines

	// Calculate available height for content.
	availableHeight := height - titleHeight - helpHeight - frameHeight
	if availableHeight < 1 {
		availableHeight = 1
	}

	// Calculate padding inside the viewport.
	const horizontalPadding = 4 // 2 left, 2 right
	var verticalPadding int
	if availableHeight > 2 {
		verticalPadding = 2 // 1 top, 1 bottom
	}

	maxVpWidth := max(1, contentWidth-horizontalPadding)
	vpHeight := max(1, availableHeight-verticalPadding)

	// Determine if scrollbar is needed by wrapping text first.
	needsScrollbar := p.visualLineCount(maxVpWidth) > vpHeight
	viewportWidth := contentWidth
	if needsScrollbar {
		viewportWidth = contentWidth - 1 // Reserve space for scrollbar.
	}

	vpWidth := max(1, viewportWidth-horizontalPadding)

	p.textarea.SetWidth(vpWidth)
	p.textarea.SetHeight(vpHeight)

	viewLines := strings.Split(p.textarea.View(), "\n")
	bgStyle := t.Dialog.ContentPanel.Padding(0)

	var formattedLines []string

	// Top padding line.
	if verticalPadding > 0 {
		topPaddingLine := bgStyle.Render(strings.Repeat(" ", viewportWidth))
		formattedLines = append(formattedLines, topPaddingLine)
	}

	for _, line := range viewLines {
		lineVal := strings.ReplaceAll(line, "\r", "")
		currentWidth := ansi.StringWidth(lineVal)
		targetTextWidth := viewportWidth - 2

		var rightPadding string
		if currentWidth < targetTextWidth {
			rightPadding = strings.Repeat(" ", targetTextWidth-currentWidth)
		} else if currentWidth > targetTextWidth {
			lineVal = ansi.Truncate(lineVal, targetTextWidth, "")
		}

		assembledLine := bgStyle.Render("  ") + lineVal + bgStyle.Render(rightPadding)
		formattedLines = append(formattedLines, assembledLine)
	}

	// Bottom padding line.
	if verticalPadding > 0 {
		bottomPaddingLine := bgStyle.Render(strings.Repeat(" ", viewportWidth))
		formattedLines = append(formattedLines, bottomPaddingLine)
	}

	content := strings.Join(formattedLines, "\n")
	var scrollbar string
	if needsScrollbar {
		scrollbar = common.Scrollbar(t, availableHeight, p.visualLineCount(vpWidth), vpHeight, p.textarea.ScrollYOffset())
	}

	// Join content with scrollbar if present.
	if scrollbar != "" {
		content = lipgloss.JoinHorizontal(lipgloss.Top, content, scrollbar)
	}

	parts := []string{titleView, "", content, "", helpView}
	innerContent := lipgloss.JoinVertical(lipgloss.Left, parts...)

	p.lastCursor = p.cursor(titleHeight, verticalPadding)
	DrawCenterCursor(scr, area, dialogStyle.Render(innerContent), p.lastCursor)
	return p.lastCursor
}

// Cursor computes the terminal cursor position relative to the dialog's
// top-left.
func (p *Preview) cursor(titleHeight, verticalPadding int) *tea.Cursor {
	taCur := p.textarea.Cursor()
	if taCur == nil {
		return nil
	}

	t := p.com.Styles
	dialogStyle := t.Dialog.View.Padding(0, 1)

	// Horizontal: dialog border + padding/margin + left text padding (2 spaces).
	taCur.X += dialogStyle.GetBorderLeftSize() +
		dialogStyle.GetPaddingLeft() +
		dialogStyle.GetMarginLeft() +
		2

	topPadding := 0
	if verticalPadding > 0 {
		topPadding = 1
	}

	// Vertical: dialog border + padding/margin + title height + spacer line
	// (1) + top vertical padding.
	taCur.Y += dialogStyle.GetBorderTopSize() +
		dialogStyle.GetPaddingTop() +
		dialogStyle.GetMarginTop() +
		titleHeight +
		1 +
		topPadding

	return taCur
}

// CalculateContentWidth computes the usable content width (dialog border + horizontal padding).
func (p *Preview) calculateContentWidth(width int) int {
	t := p.com.Styles
	dialogStyle := t.Dialog.View.Padding(0, 1)
	return width - dialogStyle.GetHorizontalFrameSize()
}

// renderTitle renders the dialog title with gradient.
func (p *Preview) renderTitle(contentWidth int) string {
	t := p.com.Styles
	title := common.DialogTitle(t, p.pasteTitle(),
		contentWidth-t.Dialog.Title.GetHorizontalFrameSize(),
		t.Dialog.TitleGradFromColor, t.Dialog.TitleGradToColor)
	return t.Dialog.Title.Render(title)
}

// WrapText wraps the text to a maximum width, preserving leading indentation.
func wrapText(text string, maxWidth int) string {
	if maxWidth <= 0 {
		return text
	}
	lines := strings.Split(text, "\n")
	var wrappedLines []string
	for _, line := range lines {
		// Extract leading whitespace.
		var indent string
		for _, r := range line {
			if r == ' ' || r == '\t' {
				indent += string(r)
			} else {
				break
			}
		}

		trimmed := line[len(indent):]
		if trimmed == "" {
			wrappedLines = append(wrappedLines, indent)
			continue
		}

		words := strings.Fields(trimmed)
		if len(words) == 0 {
			wrappedLines = append(wrappedLines, indent)
			continue
		}

		indentWidth := ansi.StringWidth(indent)
		// If indent is too wide, don't use it for wrapped lines.
		if indentWidth >= maxWidth {
			indent = ""
			indentWidth = 0
		}

		var currentLine strings.Builder
		currentLine.WriteString(indent)

		for i, word := range words {
			wordWidth := ansi.StringWidth(word)
			currentWidth := ansi.StringWidth(currentLine.String())

			if i == 0 {
				// The first word itself.
				currentLine.WriteString(word)
			} else {
				if currentWidth+1+wordWidth <= maxWidth {
					currentLine.WriteByte(' ')
					currentLine.WriteString(word)
				} else {
					wrappedLines = append(wrappedLines, currentLine.String())
					currentLine.Reset()
					currentLine.WriteString(indent)
					currentLine.WriteString(word)
				}
			}
		}
		if currentLine.Len() > len(indent) {
			wrappedLines = append(wrappedLines, currentLine.String())
		}
	}
	return strings.Join(wrappedLines, "\n")
}
