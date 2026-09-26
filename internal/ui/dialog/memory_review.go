package dialog

import (
	"fmt"
	"strings"
	"time"

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
	// MemoryReviewID is the identifier for the memory review dialog.
	MemoryReviewID        = "memory_review"
	memoryReviewMaxWidth  = 110
	memoryReviewMaxHeight = 40
	memoryReviewMinWidth  = 60
	memoryReviewMinHeight = 18
	// memoryReviewResultFlash is how long a decision's confirmation banner
	// stays before its own timer retires it. It never edits the count
	// line: reviewers read the counts as the truth of what is left.
	memoryReviewResultFlash = 4 * time.Second
	// memoryReviewBannerRows is the height the flash block always holds:
	// the frame reserves it empty, so a decision echo arriving or leaving
	// resizes nothing, and a long confirmation gets room for two rows.
	memoryReviewBannerRows = 2
	// memoryReviewContentPad insets the reading pane so its text lines up
	// with the title's own horizontal padding instead of sitting flush to
	// the border.
	memoryReviewContentPad = 1
)

// ReviewDecision is one answer a reviewer can give to a waiting entry.
type ReviewDecision string

const (
	ReviewConfirm ReviewDecision = "confirm"
	ReviewIgnore  ReviewDecision = "ignore"
	ReviewRetire  ReviewDecision = "retire"
)

// MemoryReviewItem is the presentation form of one thing waiting on a human:
// an unconfirmed draft or a queued proposal. The model owns the store and
// fills these in, so the dialog stays presentational and never touches the
// vault itself.
type MemoryReviewItem struct {
	// Kind is "draft" or "proposal"; it decides which decision keys apply.
	Kind string
	// ID is the handle the decision op takes: the entry id or the proposal
	// number as text.
	ID string
	// Label is the headline at the top of the reading pane: id and type
	// for drafts, number, op and bucket for proposals. It is allowed to
	// run long and soft-wrap.
	Label string
	// Summary is the one-line preview shown under the headline.
	Summary string
	// Why is the reason the entry waits: provenance or lane.
	Why string
	// Detail is the full reading shown in the reading pane.
	Detail string
}

// ActionReviewMemory is returned when the reviewer decides the selected item.
type ActionReviewMemory struct {
	Item     MemoryReviewItem
	Decision ReviewDecision
}

// ActionRefreshMemoryReview asks the owner to re-run the listing, so the
// dialog stays presentational even for its own refresh key.
type ActionRefreshMemoryReview struct{}

// MemoryReviewLoadedMsg carries a fresh snapshot of the waiting rooms. The
// model sends it from its load command; the dialog only stores what arrives.
type MemoryReviewLoadedMsg struct {
	Items []MemoryReviewItem
	// Note is a one-line state message (e.g. "Memory is off: nothing is
	// waiting on you.") that rides in the subtitle until the next load
	// replaces it.
	Note string
	// Result is the echo of the decision that just landed (e.g.
	// "retire: 2026-draft."). It shows as a temporary banner that its own
	// timer hides, so it never stands in for the pending counts a
	// reviewer is scanning.
	Result string
	Err    error
}

// memoryReviewResultExpiredMsg is the dialog's own timer firing: the flash
// carrying that token retires. The token lets a flash ignore the expiry of
// an older one that is still in flight.
type memoryReviewResultExpiredMsg struct {
	token uint64
}

// memoryReviewResultExpireCmd arms the one-shot that hides a flash after
// memoryReviewResultFlash. It rides out as an [ActionCmd] the way the
// workspace-index dialog arms its own polling, so the clock needs no owner
// above the dialog.
func memoryReviewResultExpireCmd(token uint64) tea.Cmd {
	return tea.Tick(memoryReviewResultFlash, func(time.Time) tea.Msg {
		return memoryReviewResultExpiredMsg{token: token}
	})
}

// MemoryReview is the human decision surface for memory: the waiting entries
// render one at a time as a single reading pane, and the decision keys answer
// the cursor's entry in place. The queue is walked with the side arrows
// rather than shown as a column — the entry identities are long file-style
// names and a column of them only ever won width from the reading. The dialog
// is a fixed size for a given terminal so the frame never jumps as the
// reading changes length; ↑/↓ and the mouse wheel scroll, ←/→ walk the queue.
type MemoryReview struct {
	com    *common.Common
	help   help.Model
	detail viewport.Model

	items  []MemoryReviewItem
	cursor int

	loading bool
	note    string
	err     error

	// result is the decision echo currently flashing; resultToken names it
	// so a stale timer cannot clear a fresher flash.
	result      string
	resultToken uint64

	keyMap struct {
		Prev       key.Binding
		Next       key.Binding
		ScrollUp   key.Binding
		ScrollDown key.Binding
		Confirm    key.Binding
		Ignore     key.Binding
		Retire     key.Binding
		Refresh    key.Binding
		Close      key.Binding
	}
}

var (
	_ Dialog      = (*MemoryReview)(nil)
	_ help.KeyMap = (*MemoryReview)(nil)
)

// NewMemoryReview creates the review dialog in its loading state; the owner
// sends a [MemoryReviewLoadedMsg] to fill it.
func NewMemoryReview(com *common.Common) *MemoryReview {
	m := &MemoryReview{com: com, loading: true}

	h := help.New()
	h.Styles = com.Styles.DialogHelpStyles()
	m.help = h

	m.keyMap.ScrollUp = key.NewBinding(
		key.WithKeys("up", "ctrl+p"),
		key.WithHelp("↑/↓", "scroll reading"),
	)
	m.keyMap.ScrollDown = key.NewBinding(
		key.WithKeys("down", "ctrl+n"),
	)
	m.keyMap.Prev = key.NewBinding(
		key.WithKeys("left", "ctrl+b"),
		key.WithHelp("←/→", "prev/next entry"),
	)
	m.keyMap.Next = key.NewBinding(
		key.WithKeys("right", "ctrl+f"),
	)
	m.keyMap.Confirm = key.NewBinding(
		key.WithKeys("a"),
		key.WithHelp("a", "approve"),
	)
	m.keyMap.Ignore = key.NewBinding(
		key.WithKeys("i"),
		key.WithHelp("i", "ignore"),
	)
	m.keyMap.Retire = key.NewBinding(
		key.WithKeys("x"),
		key.WithHelp("x", "retire"),
	)
	m.keyMap.Refresh = key.NewBinding(
		key.WithKeys("r"),
		key.WithHelp("r", "refresh"),
	)
	m.keyMap.Close = CloseKey

	// The reading pane is a viewport: it owns the vertical scroll for the
	// arrows and the wheel, which is why the dialog's own key map spends the
	// arrows on scrolling rather than on the cursor.
	detail := viewport.New()
	detail.SoftWrap = true
	detail.FillHeight = true
	detail.MouseWheelEnabled = true
	detail.KeyMap = viewport.KeyMap{
		Up:           m.keyMap.ScrollUp,
		Down:         m.keyMap.ScrollDown,
		PageUp:       key.NewBinding(key.WithKeys("pgup")),
		PageDown:     key.NewBinding(key.WithKeys("pgdn")),
		HalfPageUp:   key.NewBinding(key.WithDisabled()),
		HalfPageDown: key.NewBinding(key.WithDisabled()),
		// Horizontal movement is the queue's, not the viewport's: the
		// reading soft-wraps, so there is nothing sideways to scroll.
		Left:  key.NewBinding(key.WithDisabled()),
		Right: key.NewBinding(key.WithDisabled()),
	}
	m.detail = detail

	return m
}

// ID implements Dialog.
func (m *MemoryReview) ID() string {
	return MemoryReviewID
}

// HandleMsg implements [Dialog].
func (m *MemoryReview) HandleMsg(msg tea.Msg) Action {
	switch msg := msg.(type) {
	case MemoryReviewLoadedMsg:
		m.loading = false
		m.err = msg.Err
		m.note = msg.Note
		m.items = msg.Items
		// The cursor keeps its place across a re-listing so a decision
		// does not fling the reviewer back to the top, but a queue that
		// shrank has to leave it inside the new bounds.
		if m.cursor >= len(m.items) {
			m.cursor = 0
		}
		m.resetReading()
		if msg.Result != "" {
			m.result = msg.Result
			m.resultToken++
			return ActionCmd{Cmd: memoryReviewResultExpireCmd(m.resultToken)}
		}
		return nil

	case memoryReviewResultExpiredMsg:
		if msg.token == m.resultToken {
			m.result = ""
		}
		return nil

	case common.CoalescedWheelMsg:
		// The wheel scrolls the reading wherever it points.
		if msg.DeltaY != 0 {
			m.detail, _ = m.detail.Update(tea.MouseWheelMsg(msg.Mouse))
		}
		return nil

	case tea.KeyPressMsg:
		switch {
		case key.Matches(msg, m.keyMap.Close):
			return ActionClose{}
		case m.loading:
			// Nothing to act on until the first listing arrives.
			return nil
		case key.Matches(msg, m.keyMap.Refresh):
			return ActionRefreshMemoryReview{}
		case key.Matches(msg, m.keyMap.Confirm):
			return m.decide(ReviewConfirm)
		case key.Matches(msg, m.keyMap.Ignore):
			return m.decide(ReviewIgnore)
		case key.Matches(msg, m.keyMap.Retire):
			return m.decide(ReviewRetire)
		case key.Matches(msg, m.keyMap.Prev):
			m.step(true)
		case key.Matches(msg, m.keyMap.Next):
			m.step(false)
		default:
			// ↑/↓ (and pgup/pgdn) belong to the reading pane.
			m.detail, _ = m.detail.Update(msg)
		}
	}
	return nil
}

// step walks the cursor with wrap and hands the reading pane the new entry,
// scrolled back to the top so the reviewer starts each entry at its start.
func (m *MemoryReview) step(prev bool) {
	if len(m.items) == 0 {
		return
	}
	if prev {
		if m.cursor == 0 {
			m.cursor = len(m.items) - 1
		} else {
			m.cursor--
		}
	} else {
		if m.cursor == len(m.items)-1 {
			m.cursor = 0
		} else {
			m.cursor++
		}
	}
	m.resetReading()
}

// resetReading loads the cursor's entry into the reading pane at the top.
// The identity is long enough to wrap, so it rides as the pane's first line
// rather than as a row in a queue column that never had the width for it.
func (m *MemoryReview) resetReading() {
	t := m.com.Styles
	item, ok := m.selected()
	if !ok {
		m.detail.SetContent("")
		return
	}
	var sb strings.Builder
	sb.WriteString(t.Dialog.SelectedItem.Render(item.Label))
	if item.Summary != "" && item.Summary != item.Label {
		sb.WriteString("\n" + t.Dialog.SecondaryText.Render(item.Summary))
	}
	if item.Why != "" {
		sb.WriteString("\n" + t.Dialog.SecondaryText.Render("why: "+item.Why))
	}
	if item.Detail != "" {
		sb.WriteString("\n\n" + item.Detail)
	}
	m.detail.SetContent(sb.String())
	m.detail.GotoTop()
}

// selected returns the cursor's item, if the queue has one.
func (m *MemoryReview) selected() (MemoryReviewItem, bool) {
	if m.cursor < 0 || m.cursor >= len(m.items) {
		return MemoryReviewItem{}, false
	}
	return m.items[m.cursor], true
}

// decide answers the selected item. The dialog does not optimistically
// remove it: the owner re-sends the listing once the vault has moved, so a
// decision that was refused upstream still shows up rather than vanishing.
func (m *MemoryReview) decide(decision ReviewDecision) Action {
	item, ok := m.selected()
	if !ok {
		return nil
	}
	return ActionReviewMemory{Item: item, Decision: decision}
}

// ShortHelp implements [help.KeyMap].
func (m *MemoryReview) ShortHelp() []key.Binding {
	return []key.Binding{
		m.keyMap.ScrollUp,
		m.keyMap.Prev,
		m.keyMap.Confirm,
		m.keyMap.Ignore,
		m.keyMap.Retire,
		m.keyMap.Refresh,
		m.keyMap.Close,
	}
}

// FullHelp implements [help.KeyMap].
func (m *MemoryReview) FullHelp() [][]key.Binding {
	short := m.ShortHelp()
	mid := len(short) / 2
	return [][]key.Binding{short[:mid], short[mid:]}
}

// titleStyle is the dialog title with a row of air beneath it so the title
// does not sit flush on top of the subtitle line.
func (m *MemoryReview) titleStyle() lipgloss.Style {
	return m.com.Styles.Dialog.Title.PaddingBottom(1)
}

// measure is the dialog's fixed geometry for a screen. It depends on nothing
// but the terminal size and the theme, so a queue of zero entries renders at
// the same size as a queue of twenty-five and the frame never jumps when the
// content changes.
func (m *MemoryReview) measure(area uv.Rectangle) (width, height, bodyHeight int) {
	t := m.com.Styles
	width = max(min(memoryReviewMinWidth, area.Dx()), min(memoryReviewMaxWidth, area.Dx()))
	height = max(min(memoryReviewMinHeight, area.Dy()), min(memoryReviewMaxHeight, area.Dy()))

	verticalBudget := t.Dialog.View.GetVerticalFrameSize() +
		m.titleStyle().GetVerticalFrameSize() + titleContentHeight +
		1 + // the subtitle line, always present
		5 + // the always-reserved flash block: its two rows plus the three
		// blank rows the dialog's gap leaves around it — one each under
		// the subtitle, under the body, and over the help
		t.Dialog.HelpView.GetVerticalFrameSize()
	bodyHeight = max(5, height-verticalBudget)
	return
}

// Draw implements [Dialog].
func (m *MemoryReview) Draw(scr uv.Screen, area uv.Rectangle) *tea.Cursor {
	width, _, bodyHeight := m.measure(area)
	DrawCenter(scr, area, m.render(width, bodyHeight))
	return nil
}

// render assembles the framed dialog: title, count line, one body branch,
// the always-reserved flash row, and the key help. The banner row is there
// even when empty so the flash arriving or leaving cannot resize the frame.
func (m *MemoryReview) render(width, bodyHeight int) string {
	t := m.com.Styles
	innerWidth := width - t.Dialog.View.GetHorizontalFrameSize()

	rc := NewRenderContext(t, width)
	rc.TitleStyle = m.titleStyle()
	rc.Title = "Memory Review"
	// The count line carries what the reviewer still owes, so it rides in
	// the base foreground rather than the most-subtle tone the default
	// subtitle style uses; the gap breathes under it and above the help.
	rc.SubtitleStyle = t.Dialog.NormalItem
	rc.Gap = 1
	// The subtitle always renders, even as a blank, so a missing count line
	// cannot shrink the frame by a row.
	rc.Subtitle = blankIf(m.subtitle(), " ")

	switch {
	case m.err != nil:
		rc.AddPart(padded(t.Dialog.SecondaryText.Render("Memory review failed: "+m.err.Error()), innerWidth, bodyHeight))
	case m.loading:
		rc.AddPart(padded(t.Dialog.SecondaryText.Render("Loading the review queue..."), innerWidth, bodyHeight))
	case len(m.items) == 0:
		rc.AddPart(padded(t.Dialog.SecondaryText.Render(
			"Nothing is waiting on you: no unconfirmed drafts and no queued proposals."), innerWidth, bodyHeight))
	default:
		rc.AddPart(m.renderBody(innerWidth, bodyHeight))
	}
	rc.AddPart(m.banner(innerWidth))

	rc.Help = m.help.View(m)
	return rc.Render()
}

// renderBody is the single reading pane at the full inner width. The one
// column of padding on each side is what lets the text sit at the same inset
// as the title instead of a pixel off the border. The pane must wrap two
// cells narrower than that block: a Width(W) style with Padding(0,1) has a
// content box of W-2, so a pane already wrapped at W would be re-wrapped by
// the block and fling a word or two onto its own nearly-empty row.
func (m *MemoryReview) renderBody(innerWidth, bodyHeight int) string {
	contentWidth := max(20, innerWidth-2*memoryReviewContentPad)
	m.detail.SetWidth(contentWidth)
	m.detail.SetHeight(bodyHeight)
	return lipgloss.NewStyle().
		Width(contentWidth+2*memoryReviewContentPad).
		Padding(0, memoryReviewContentPad).
		Height(bodyHeight).
		MaxHeight(bodyHeight).
		Render(m.detail.View())
}

// padded renders a block at an exact width and height so every branch of
// the body holds the frame to the same size.
func padded(s string, width, height int) string {
	return lipgloss.NewStyle().Width(width).Height(height).MaxHeight(height).Render(s)
}

// banner is the temporary confirmation of the decision that just landed.
// The block is a fixed memoryReviewBannerRows tall, whether or not a flash
// is up, so the frame never jumps on the flash's way in or out; text that
// would run past those rows is cut with an ellipsis rather than push the
// border down. It lines up with the title's inset like the reading does.
func (m *MemoryReview) banner(innerWidth int) string {
	text := ""
	if m.result != "" {
		room := memoryReviewBannerRows * max(1, innerWidth-2*memoryReviewContentPad)
		text = ansi.Truncate(m.result, room, "…")
	}
	return m.com.Styles.Dialog.PrimaryText.
		Width(innerWidth).
		Height(memoryReviewBannerRows).
		MaxHeight(memoryReviewBannerRows).
		Render(text)
}

// subtitle is the count line, or the state note when one is set: a note like
// "Memory is off" is the truth of the queue, so it outranks counts that were
// counted against a vault that is not there. The decision flash is not part
// of this line: the counts stay readable while it shows.
func (m *MemoryReview) subtitle() string {
	if m.note != "" {
		return m.note
	}
	if m.loading || m.err != nil || len(m.items) == 0 {
		return ""
	}
	drafts, proposals := 0, 0
	for _, it := range m.items {
		if it.Kind == "proposal" {
			proposals++
		} else {
			drafts++
		}
	}
	return fmt.Sprintf("Waiting on you: %d %s, %d %s.",
		drafts, reviewPlural(drafts, "draft", "drafts"),
		proposals, reviewPlural(proposals, "proposal", "proposals"))
}

func reviewPlural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func blankIf(s, blank string) string {
	if s == "" {
		return blank
	}
	return s
}
