package dialog

import (
	"image"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/hackafterdark/phosphor/internal/ui/common"
	"github.com/hackafterdark/phosphor/internal/ui/styles"
	"github.com/stretchr/testify/require"
)

// selectedID is the cursor's entry id, empty when the queue has no cursor.
func (m *MemoryReview) selectedID() string {
	item, ok := m.selected()
	if !ok {
		return ""
	}
	return item.ID
}

func newTestMemoryReview(t *testing.T) *MemoryReview {
	t.Helper()
	s := styles.CharmtonePantera()
	return NewMemoryReview(&common.Common{Styles: &s})
}

func loadedReview(t *testing.T) *MemoryReview {
	t.Helper()
	m := newTestMemoryReview(t)
	m.HandleMsg(MemoryReviewLoadedMsg{Items: []MemoryReviewItem{
		{Kind: "draft", ID: "d1", Label: "d1 [decision]", Summary: "ship it", Why: "unconfirmed inference", Detail: "the long body"},
		{Kind: "proposal", ID: "7", Label: "#7 add [bucket]", Summary: "queued thing"},
	}})
	// Keys and the wheel scroll the reading pane against a real height; the
	// test asserts scroll without depending on a Draw pass.
	m.detail.SetWidth(50)
	m.detail.SetHeight(4)
	return m
}

// longReading is a body that overflows the four-line test pane, so a scroll
// has somewhere to go.
func longReading() string {
	return strings.Repeat("line\n", 30)
}

func TestMemoryReview_LoadingAbsorbsKeysUntilTheQueueArrives(t *testing.T) {
	t.Parallel()

	m := newTestMemoryReview(t)
	require.True(t, m.loading)

	// Deciding on a queue that has not arrived yet must not act on nothing;
	// only closing is honest while the read is still in flight.
	require.Nil(t, m.HandleMsg(tea.KeyPressMsg{Code: 'a'}))
	require.Equal(t, ActionClose{}, m.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEscape}))
}

func TestMemoryReview_DecisionKeysAnswerTheSelectedItem(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		code     rune
		decision ReviewDecision
	}{
		{'a', ReviewConfirm},
		{'i', ReviewIgnore},
		{'x', ReviewRetire},
	} {
		m := loadedReview(t)
		action := m.HandleMsg(tea.KeyPressMsg{Code: tc.code})
		decide, ok := action.(ActionReviewMemory)
		require.True(t, ok, "the %q key must produce a decision", tc.code)
		require.Equal(t, tc.decision, decide.Decision)
		require.Equal(t, "d1", decide.Item.ID, "the cursor's item is the one answered")

		// The dialog does not remove it optimistically: a refused decision
		// has to come back from the owner's re-listing, not vanish.
		require.Equal(t, 2, len(m.items))
	}
}

func TestMemoryReview_RefreshAsksTheOwnerToReRead(t *testing.T) {
	t.Parallel()

	m := loadedReview(t)
	require.Equal(t, ActionRefreshMemoryReview{}, m.HandleMsg(tea.KeyPressMsg{Code: 'r'}))
}

func TestMemoryReview_ArrowsScrollTheReadingNotTheCursor(t *testing.T) {
	t.Parallel()

	m := loadedReview(t)
	m.HandleMsg(MemoryReviewLoadedMsg{Items: []MemoryReviewItem{
		{Kind: "draft", ID: "d1", Label: "d1 [decision]", Detail: longReading()},
		{Kind: "proposal", ID: "7", Label: "#7 add [bucket]", Detail: longReading()},
	}})

	item, _ := m.selected()
	require.Equal(t, "d1", item.ID)

	m.HandleMsg(tea.KeyPressMsg{Code: tea.KeyDown})
	require.Equal(t, "d1", m.selectedID(), "the down arrow scrolls, it never walks the queue")
	require.Greater(t, m.detail.YOffset(), 0, "the down arrow moved the reading window")

	m.HandleMsg(tea.KeyPressMsg{Code: tea.KeyUp})
	m.HandleMsg(tea.KeyPressMsg{Code: tea.KeyUp})
	require.Equal(t, 0, m.detail.YOffset(), "the up arrow scrolls back up")
}

func TestMemoryReview_SideArrowsWalkTheQueueAndRewindTheReading(t *testing.T) {
	t.Parallel()

	m := loadedReview(t)
	m.HandleMsg(MemoryReviewLoadedMsg{Items: []MemoryReviewItem{
		{Kind: "draft", ID: "d1", Label: "d1 [decision]", Detail: longReading()},
		{Kind: "proposal", ID: "7", Label: "#7 add [bucket]", Detail: longReading()},
	}})

	m.HandleMsg(tea.KeyPressMsg{Code: tea.KeyDown})
	require.Greater(t, m.detail.YOffset(), 0)

	m.HandleMsg(tea.KeyPressMsg{Code: tea.KeyRight})
	require.Equal(t, "7", m.selectedID())
	require.Equal(t, 0, m.detail.YOffset(), "a new entry starts at its top, not at the old one's scroll")
	require.Contains(t, m.detail.GetContent(), "#7 add [bucket]", "the reading pane shows the walked-to entry")

	// From the last row the right arrow wraps, so the cursor can never get
	// stranded at the end of a short queue.
	m.HandleMsg(tea.KeyPressMsg{Code: tea.KeyRight})
	require.Equal(t, "d1", m.selectedID())
	m.HandleMsg(tea.KeyPressMsg{Code: tea.KeyLeft})
	require.Equal(t, "7", m.selectedID())
}

func TestMemoryReview_WheelScrollsTheReading(t *testing.T) {
	t.Parallel()

	m := loadedReview(t)
	m.HandleMsg(MemoryReviewLoadedMsg{Items: []MemoryReviewItem{
		{Kind: "draft", ID: "d1", Label: "d1 [decision]", Detail: longReading()},
	}})

	m.HandleMsg(common.CoalescedWheelMsg{Mouse: tea.Mouse{Button: tea.MouseWheelDown}, DeltaY: 3})
	require.Greater(t, m.detail.YOffset(), 0, "the wheel scrolls the reading wherever it points")

	m.HandleMsg(common.CoalescedWheelMsg{Mouse: tea.Mouse{Button: tea.MouseWheelUp}, DeltaY: -3})
	m.HandleMsg(common.CoalescedWheelMsg{Mouse: tea.Mouse{Button: tea.MouseWheelUp}, DeltaY: -3})
	require.Equal(t, 0, m.detail.YOffset())
}

func TestMemoryReview_EmptyQueueHasNothingToDecide(t *testing.T) {
	t.Parallel()

	m := newTestMemoryReview(t)
	m.HandleMsg(MemoryReviewLoadedMsg{})
	require.False(t, m.loading)
	require.Nil(t, m.HandleMsg(tea.KeyPressMsg{Code: 'a'}),
		"approving an empty queue is a no-op, not a decision on nothing")
	require.Empty(t, m.detail.GetContent())
}

func TestMemoryReview_SubtitleCountsAndStateNotesNeverDecisionEchoes(t *testing.T) {
	t.Parallel()

	m := loadedReview(t)
	require.Equal(t, "Waiting on you: 1 draft, 1 proposal.", m.subtitle())

	// A decision echo is a flash, not the subtitle: the counts stay up so
	// the reviewer keeps reading what is left, not what was just done.
	// (A real decision re-listing carries the shrunken queue with it.)
	m.HandleMsg(MemoryReviewLoadedMsg{
		Items: []MemoryReviewItem{
			{Kind: "draft", ID: "d1", Label: "d1 [decision]"},
			{Kind: "proposal", ID: "7", Label: "#7 add [bucket]"},
		},
		Result: "retire: d1.",
	})
	require.Equal(t, "Waiting on you: 1 draft, 1 proposal.", m.subtitle())

	off := newTestMemoryReview(t)
	off.HandleMsg(MemoryReviewLoadedMsg{Note: "Memory is off: nothing is waiting on you."})
	require.Equal(t, "Memory is off: nothing is waiting on you.", off.subtitle(),
		"a state note is the truth of the queue and must own the subtitle")
}

func TestMemoryReview_DecisionResultFlashesThenItsTimerHidesIt(t *testing.T) {
	t.Parallel()

	m := loadedReview(t)
	loaded := MemoryReviewLoadedMsg{
		Items:  []MemoryReviewItem{{Kind: "draft", ID: "d1", Label: "d1 [decision]"}},
		Result: "retire: d1.",
	}
	action := m.HandleMsg(loaded)
	cmd, ok := action.(ActionCmd)
	require.True(t, ok, "the flash arms its own expiry through the owner")
	require.NotNil(t, cmd.Cmd)
	require.Contains(t, m.banner(60), "retire: d1.")

	// The flash carries a token so the in-flight expiry of an older one
	// cannot wipe it early.
	stale := memoryReviewResultExpiredMsg{token: m.resultToken - 1}
	m.HandleMsg(stale)
	require.Contains(t, m.banner(60), "retire: d1.", "a stale timer must not hide the current flash")

	fresh := memoryReviewResultExpiredMsg{token: m.resultToken}
	m.HandleMsg(fresh)
	require.Empty(t, strings.TrimSpace(ansi.Strip(m.banner(60))),
		"the flash is gone once its own timer fires, though its rows stay reserved")
}

func TestMemoryReview_ReadingHeaderCarriesIdentitySummaryAndWhy(t *testing.T) {
	t.Parallel()

	m := loadedReview(t)
	content := m.detail.GetContent()
	require.Contains(t, content, "d1 [decision]",
		"the identity headlines the reading pane now that no queue column shows it")
	require.Contains(t, content, "ship it")
	require.Contains(t, content, "unconfirmed inference",
		"the why rides under the headline instead of in a truncated row")
	require.Contains(t, content, "the long body",
		"the reading pane carries the body under the header")
}

func TestMemoryReview_LoadMsgReplacesTheQueueAndClampsTheCursor(t *testing.T) {
	t.Parallel()

	m := loadedReview(t)
	m.HandleMsg(tea.KeyPressMsg{Code: tea.KeyRight})
	m.HandleMsg(MemoryReviewLoadedMsg{Items: []MemoryReviewItem{{Kind: "draft", ID: "only"}}})
	require.Equal(t, "only", m.selectedID(), "the cursor lands inside the new queue rather than past its end")
}

func TestMemoryReview_SizeIsFixedForAScreenWhateverTheContent(t *testing.T) {
	t.Parallel()

	area := image.Rect(0, 0, 200, 50)

	empty := newTestMemoryReview(t)
	empty.HandleMsg(MemoryReviewLoadedMsg{})
	ew, eh, eb := empty.measure(area)

	full := loadedReview(t)
	fw, fh, fb := full.measure(area)

	long := loadedReview(t)
	long.HandleMsg(MemoryReviewLoadedMsg{Items: []MemoryReviewItem{
		{Kind: "draft", ID: "d1", Label: "d1 [decision]", Detail: longReading()},
	}})
	lw, lh, lb := long.measure(area)

	// The frame is a function of the terminal, not of what the queue holds
	// or how long an entry runs, so nothing about reviewing can make it jump.
	require.Equal(t, [3]int{ew, eh, eb}, [3]int{fw, fh, fb})
	require.Equal(t, [3]int{ew, eh, eb}, [3]int{lw, lh, lb})
	require.Greater(t, ew, memoryReviewMinWidth-1, "it fills a good part of a wide screen")
	require.Greater(t, eh, memoryReviewMinHeight-1)
}

func TestMemoryReview_BodyIsNotRewrappedByItsOwnPaddingBlock(t *testing.T) {
	t.Parallel()

	m := loadedReview(t)
	// Four-column words make the viewport's wrapped lines end at 39 columns
	// in a 40-column pane: one wider than the padding block's content box,
	// which is exactly the width that made the block re-wrap a line and
	// strand its last word on an otherwise-empty row.
	m.HandleMsg(MemoryReviewLoadedMsg{Items: []MemoryReviewItem{
		{Kind: "draft", ID: "d1", Label: "d1", Detail: strings.Repeat("word ", 20)},
	}})

	for _, line := range strings.Split(ansi.Strip(m.renderBody(42, 6)), "\n") {
		require.NotEqual(t, "word", strings.TrimSpace(line),
			"a pane-wrapped line must ride through the padding block, not be re-wrapped by it")
	}
}

func TestMemoryReview_FlashNeitherResizesNorOverflowsTheFrame(t *testing.T) {
	t.Parallel()

	area := image.Rect(0, 0, 200, 50)
	m := loadedReview(t)
	width, _, bodyHeight := m.measure(area)

	before := strings.Split(m.render(width, bodyHeight), "\n")

	m.HandleMsg(MemoryReviewLoadedMsg{
		Items:  []MemoryReviewItem{{Kind: "draft", ID: "d1", Label: "d1 [decision]"}},
		Result: strings.Repeat("word ", 40),
	})
	after := strings.Split(m.render(width, bodyHeight), "\n")

	require.Contains(t, strings.Join(after, "\n"), "word",
		"the flash must actually be on the frame while it is up")
	require.Equal(t, len(before), len(after),
		"the row budget must reserve the flash's row, so it neither grows nor shrinks the frame")
	for _, line := range after {
		require.LessOrEqual(t, lipgloss.Width(line), width,
			"no rendered row may run past the frame, flash included")
	}
}
