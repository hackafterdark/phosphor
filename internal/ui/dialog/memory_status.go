package dialog

import (
	"strings"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/hackafterdark/phosphor/internal/ui/common"
)

const (
	// MemoryStatusID is the identifier for the memory status dialog.
	MemoryStatusID         = "memory_status"
	memoryStatusMaxWidth   = 88
	memoryStatusMinWidth   = 56
	memoryStatusMaxHeight  = 26
	memoryStatusMinHeight  = 14
	memoryStatusContentPad = 1
)

// MemoryStatusSection is one labeled block of the stats report: a header
// the dialog styles as its own colored heading, and the value lines under
// it. The owner builds these from the vault; the dialog only decides how
// each block looks.
type MemoryStatusSection struct {
	// Label is the heading text ("Corpus", "Tamper seal", ...). A
	// trailing colon is added if the label does not carry one.
	Label string
	// Lines are the values rendered under the heading, in order, one per
	// row (a soft-wrapped row still counts once for scrolling).
	Lines []string
	// Bulleted renders Lines as a list: one blank row between items, a
	// bullet before each, and wrapped continuations aligned under the
	// item text rather than under the bullet.
	Bulleted bool
}

// MemoryStatusLoadedMsg carries the formatted status report. The owner reads
// the vault on a tea.Cmd and hands the finished sections here; the dialog stays
// presentational and never touches the store itself.
type MemoryStatusLoadedMsg struct {
	// Sections is the corpus accounting broken into headed blocks: the
	// corpus columns, the inject budget, the vault path, the seal
	// accounting, and the active threads. They are displayed verbatim,
	// one blank row apart, inside a scrolling reading pane.
	Sections []MemoryStatusSection
	// Note is a one-line state message (e.g. "Memory is off: ..." or the
	// quarantine hint) rendered as the reading pane's first block, above
	// the sections and set apart from them by a blank row.
	Note string
	Err  error
}

// MemoryStatus is the read-only stats surface for the memory system: the
// full corpus accounting the sidebar widget has no room for, in a frame
// sized to hold it. It exists so a glance at the widget that raises a
// question ("quarantined what?") has somewhere to take the question. The
// sections carry distinct colors so the eye can find each block without
// re-reading the labels, and the reading pane scrolls (↑/↓, pgup/pgdn, the
// mouse wheel) because a long active-threads list will not always fit the
// frame. The stats keys are exits to the surfaces that act on them — r
// walks to the review queue, f to the fsck reconcile — so answering what
// you see never needs a trip back through the editor.
type MemoryStatus struct {
	com  *common.Common
	help help.Model
	body viewport.Model

	loading      bool
	note         string
	sections     []MemoryStatusSection
	err          error
	contentWidth int // width the reading pane's content was last wrapped for

	keyMap struct {
		ScrollUp   key.Binding
		ScrollDown key.Binding
		Review     key.Binding
		Fsck       key.Binding
		Close      key.Binding
	}
}

var (
	_ Dialog      = (*MemoryStatus)(nil)
	_ help.KeyMap = (*MemoryStatus)(nil)
)

// NewMemoryStatus creates the status dialog in its loading state; the owner
// sends a [MemoryStatusLoadedMsg] to fill it.
func NewMemoryStatus(com *common.Common) *MemoryStatus {
	m := &MemoryStatus{com: com, loading: true}

	h := help.New()
	h.Styles = com.Styles.DialogHelpStyles()
	m.help = h

	m.keyMap.ScrollUp = key.NewBinding(
		key.WithKeys("up", "ctrl+p"),
		key.WithHelp("↑/↓", "scroll"),
	)
	m.keyMap.ScrollDown = key.NewBinding(
		key.WithKeys("down", "ctrl+n"),
	)
	m.keyMap.Review = key.NewBinding(
		key.WithKeys("r"),
		key.WithHelp("r", "review queue"),
	)
	m.keyMap.Fsck = key.NewBinding(
		key.WithKeys("f"),
		key.WithHelp("f", "fsck resync"),
	)
	m.keyMap.Close = CloseKey

	// The report is a viewport: it owns the vertical scroll for the arrows
	// and the wheel, which is why the thread list can run past the frame
	// without losing its tail.
	body := viewport.New()
	body.SoftWrap = true
	body.FillHeight = true
	body.MouseWheelEnabled = true
	body.KeyMap = viewport.KeyMap{
		Up:           m.keyMap.ScrollUp,
		Down:         m.keyMap.ScrollDown,
		PageUp:       key.NewBinding(key.WithKeys("pgup")),
		PageDown:     key.NewBinding(key.WithKeys("pgdn")),
		HalfPageUp:   key.NewBinding(key.WithDisabled()),
		HalfPageDown: key.NewBinding(key.WithDisabled()),
		// There is nothing sideways to scroll: every line soft-wraps.
		Left:  key.NewBinding(key.WithDisabled()),
		Right: key.NewBinding(key.WithDisabled()),
	}
	m.body = body

	return m
}

// ID implements Dialog.
func (m *MemoryStatus) ID() string {
	return MemoryStatusID
}

// HandleMsg implements [Dialog]. The stats keys are the dialog's only
// mutations — there is nothing in it to edit — and both leave through the
// slash dispatcher so the destination surface is opened exactly as if the
// user had typed its command.
func (m *MemoryStatus) HandleMsg(msg tea.Msg) Action {
	switch msg := msg.(type) {
	case MemoryStatusLoadedMsg:
		m.loading = false
		m.err = msg.Err
		m.note = msg.Note
		m.sections = msg.Sections
		m.body.SetContent(m.content(m.contentWidth))
		m.body.GotoTop()
		return nil

	case common.CoalescedWheelMsg:
		// The wheel scrolls the report wherever it points.
		if msg.DeltaY != 0 {
			m.body, _ = m.body.Update(tea.MouseWheelMsg(msg.Mouse))
		}
		return nil

	case tea.KeyPressMsg:
		switch {
		case key.Matches(msg, m.keyMap.Close):
			return ActionClose{}
		case m.loading:
			// Nothing to act on until the first report arrives.
			return nil
		case key.Matches(msg, m.keyMap.Review):
			return ActionRunSlashCommand{Line: "/memory review"}
		case key.Matches(msg, m.keyMap.Fsck):
			return ActionRunSlashCommand{Line: "/memory fsck"}
		default:
			// ↑/↓ (and pgup/pgdn) belong to the reading pane.
			m.body, _ = m.body.Update(msg)
		}
	}
	return nil
}

// content lays the sections out as the reading pane's text: each heading
// rendered in its own color, its value lines under it, and one blank row
// between sections so the blocks read as blocks.
func (m *MemoryStatus) content(width int) string {
	t := m.com.Styles
	styles := m.headerStyles()
	blocks := make([]string, 0, len(m.sections)+1)
	if m.note != "" {
		// PrimaryText, not SecondaryText: this note is the one line in the
		// report the reader has to act on (quarantine, memory-off), and
		// the most-subtle tone buried it at the same weight as nothing.
		blocks = append(blocks, t.Dialog.PrimaryText.Render(m.note))
	}
	for _, sec := range m.sections {
		if len(sec.Lines) == 0 {
			continue
		}
		label := sec.Label
		if label != "" && !strings.HasSuffix(label, ":") {
			label += ":"
		}
		var sb strings.Builder
		if style, ok := styles[sec.Label]; ok {
			sb.WriteString(style.Render(label))
		} else {
			sb.WriteString(t.Dialog.TitleText.Bold(true).Render(label))
		}
		if sec.Bulleted {
			// A list reads as items, not as a wall of sentences: one blank
			// row between items, a bullet before each, and the wrap done
			// here with a hanging indent so a multi-line item's tail lines
			// up under its own text rather than under the bullet. The
			// bullet carries the theme's primary color so items stay
			// visually distinct from the plain value rows elsewhere.
			itemWidth := max(10, width-2)
			for _, line := range sec.Lines {
				text := strings.ReplaceAll(line, "\n", " ")
				item := ansi.Wrap(text, itemWidth, "  ")
				sb.WriteString("\n\n" + t.Dialog.TitleText.Render("• ") + t.Dialog.NormalItem.Render(item))
			}
		} else {
			for _, line := range sec.Lines {
				sb.WriteString("\n" + t.Dialog.NormalItem.Render(line))
			}
		}
		blocks = append(blocks, sb.String())
	}
	return strings.Join(blocks, "\n\n")
}

// headerStyles maps each known section to a distinct heading style. The
// colors all come from theme tokens (via the dialog styles that already
// carry them) so any theme's palette is respected; bold is the shared
// voice, the token the varying one.
func (m *MemoryStatus) headerStyles() map[string]lipgloss.Style {
	t := m.com.Styles
	return map[string]lipgloss.Style{
		"Corpus":          t.Dialog.TitleText.Bold(true),
		"Injected window": t.Dialog.TitleAccent,
		"Vault":           t.Dialog.NormalItem.Padding(0, 0).Bold(true),
		"Tamper seal":     t.LSP.WarningDiagnostic.Bold(true),
		"Needs attention": t.Dialog.TitleError.Bold(true),
		"Active threads":  lipgloss.NewStyle().Foreground(t.Dialog.TitleGradFromColor).Bold(true),
	}
}

// ShortHelp implements [help.KeyMap].
func (m *MemoryStatus) ShortHelp() []key.Binding {
	return []key.Binding{
		m.keyMap.ScrollUp,
		m.keyMap.Review,
		m.keyMap.Fsck,
		m.keyMap.Close,
	}
}

// FullHelp implements [help.KeyMap].
func (m *MemoryStatus) FullHelp() [][]key.Binding {
	short := m.ShortHelp()
	mid := len(short) / 2
	return [][]key.Binding{short[:mid], short[mid:]}
}

// measure is the dialog's fixed geometry for a screen, clamped to the
// terminal so a tiny window never draws a frame taller than it can show.
func (m *MemoryStatus) measure(area uv.Rectangle) (width, height, bodyHeight int) {
	t := m.com.Styles
	width = max(min(memoryStatusMinWidth, area.Dx()), min(memoryStatusMaxWidth, area.Dx()))
	height = max(min(memoryStatusMinHeight, area.Dy()), min(memoryStatusMaxHeight, area.Dy()))

	verticalBudget := t.Dialog.View.GetVerticalFrameSize() +
		t.Dialog.Title.GetVerticalFrameSize() + titleContentHeight +
		1 + // the subtitle line, always present
		2 + // one gap row each above and below the body
		t.Dialog.HelpView.GetVerticalFrameSize()
	bodyHeight = max(4, height-verticalBudget)
	return
}

// Draw implements [Dialog].
func (m *MemoryStatus) Draw(scr uv.Screen, area uv.Rectangle) *tea.Cursor {
	width, _, bodyHeight := m.measure(area)
	DrawCenter(scr, area, m.render(width, bodyHeight))
	return nil
}

// render assembles the framed dialog: title, a state subtitle row, the body
// branch, and the key help. Every branch of the body holds the frame to the
// same size so the border never jumps as the report arrives.
func (m *MemoryStatus) render(width, bodyHeight int) string {
	t := m.com.Styles
	innerWidth := width - t.Dialog.View.GetHorizontalFrameSize()

	rc := NewRenderContext(t, width)
	rc.Gap = 1
	rc.SubtitleStyle = t.Dialog.NormalItem
	// The subtitle row stays blank and always renders, so the frame size
	// never moves; the state note lives in the reading pane instead, where
	// it gets a full row of air beneath the header rather than sitting
	// flush under the title.
	rc.Subtitle = " "

	switch {
	case m.err != nil:
		rc.AddPart(padded(t.Dialog.SecondaryText.Render("Memory status failed: "+m.err.Error()), innerWidth, bodyHeight))
	case m.loading:
		rc.AddPart(padded(t.Dialog.SecondaryText.Render("Reading the vault..."), innerWidth, bodyHeight))
	default:
		rc.AddPart(m.renderBody(innerWidth, bodyHeight))
	}

	rc.Title = "Memory"
	rc.Help = m.help.View(m)
	return rc.Render()
}

// renderBody is the scrolling reading pane at the full inner width. The pane
// must wrap two cells narrower than the padded block: a Width(W) style with
// Padding(0,1) has a content box of W-2, so a pane already wrapped at W
// would be re-wrapped by the block and fling a word or two onto its own
// nearly-empty row.
func (m *MemoryStatus) renderBody(innerWidth, bodyHeight int) string {
	contentWidth := max(20, innerWidth-2*memoryStatusContentPad)
	// Bulleted items are wrapped to the pane's width with their hanging
	// indent baked in, so the text is re-wrapped whenever the pane width
	// changes (and only then — the scroll offset survives ordinary draws).
	if contentWidth != m.contentWidth {
		m.body.SetContent(m.content(contentWidth))
		m.contentWidth = contentWidth
	}
	m.body.SetWidth(contentWidth)
	m.body.SetHeight(bodyHeight)
	return lipgloss.NewStyle().
		Width(contentWidth+2*memoryStatusContentPad).
		Padding(0, memoryStatusContentPad).
		Height(bodyHeight).
		MaxHeight(bodyHeight).
		Render(m.body.View())
}
