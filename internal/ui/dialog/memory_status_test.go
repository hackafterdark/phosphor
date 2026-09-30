package dialog

import (
	"fmt"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/hackafterdark/phosphor/internal/ui/common"
	uis "github.com/hackafterdark/phosphor/internal/ui/styles"
	"github.com/stretchr/testify/require"
)

func newMemoryStatusTestDialog(t *testing.T) *MemoryStatus {
	t.Helper()
	st := uis.CharmtonePantera()
	return NewMemoryStatus(&common.Common{Styles: &st})
}

func memoryStatusTestSections() []MemoryStatusSection {
	threads := make([]string, 0, 12)
	for i := range 12 {
		threads = append(threads, fmt.Sprintf("thread-%d keeps running long enough to need the scroll", i))
	}
	return []MemoryStatusSection{
		{Label: "Corpus", Lines: []string{"total        2", "active       2"}},
		{Label: "Active threads", Lines: threads, Bulleted: true},
	}
}

func TestMemoryStatusClosesOnEscape(t *testing.T) {
	t.Parallel()

	m := newMemoryStatusTestDialog(t)
	require.Equal(t, MemoryStatusID, m.ID())

	// Escape is the close key even mid-load: a report that never arrives
	// must never trap the reader in an empty frame.
	require.Equal(t, ActionClose{}, m.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEscape}))
}

func TestMemoryStatusKeysWaitForTheReport(t *testing.T) {
	t.Parallel()

	m := newMemoryStatusTestDialog(t)

	// While loading there is nothing to act on; only the close key passes.
	require.Nil(t, m.HandleMsg(tea.KeyPressMsg{Code: 'r'}))
	require.Nil(t, m.HandleMsg(tea.KeyPressMsg{Code: 'f'}))

	m.HandleMsg(MemoryStatusLoadedMsg{Sections: memoryStatusTestSections()})

	// The stats keys exit through the slash dispatcher, so the review
	// queue and the fsck run open exactly as if the user had typed them.
	require.Equal(t, ActionRunSlashCommand{Line: "/memory review"},
		m.HandleMsg(tea.KeyPressMsg{Code: 'r'}))
	require.Equal(t, ActionRunSlashCommand{Line: "/memory fsck"},
		m.HandleMsg(tea.KeyPressMsg{Code: 'f'}))
	require.Equal(t, ActionClose{}, m.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEscape}))
}

func TestMemoryStatusScrollsPastTheFrame(t *testing.T) {
	t.Parallel()

	m := newMemoryStatusTestDialog(t)
	m.HandleMsg(MemoryStatusLoadedMsg{Sections: memoryStatusTestSections()})

	// Prime the pane with the frame geometry Draw would give it, then
	// scroll: a report longer than the frame must move, and the wheel too.
	m.contentWidth = 50
	m.body.SetWidth(50)
	m.body.SetHeight(6)
	m.HandleMsg(MemoryStatusLoadedMsg{Sections: memoryStatusTestSections()})
	require.Equal(t, 0, m.body.YOffset())

	m.HandleMsg(tea.KeyPressMsg{Code: tea.KeyDown})
	m.HandleMsg(tea.KeyPressMsg{Code: tea.KeyDown})
	require.Greater(t, m.body.YOffset(), 0, "the down arrow scrolls the report")

	m.HandleMsg(tea.KeyPressMsg{Code: tea.KeyUp})
	m.HandleMsg(tea.KeyPressMsg{Code: tea.KeyUp})
	require.Equal(t, 0, m.body.YOffset(), "the up arrow scrolls back to the top")

	m.HandleMsg(common.CoalescedWheelMsg{Mouse: tea.Mouse{Button: tea.MouseWheelDown}, DeltaY: 3})
	require.Greater(t, m.body.YOffset(), 0, "the wheel scrolls the report wherever it points")
}

func TestMemoryStatusContentSeparatesSections(t *testing.T) {
	t.Parallel()

	m := newMemoryStatusTestDialog(t)
	m.HandleMsg(MemoryStatusLoadedMsg{Sections: memoryStatusTestSections()})

	content := m.content(50)
	require.Contains(t, content, "Corpus:")
	require.Contains(t, content, "Active threads:")
	// One blank row between blocks is what makes them read as sections
	// (the headers carry ANSI styling, so check for the gap itself).
	require.Contains(t, content, "\n\n")
	// Each thread is its own bulleted item, not one run-on comma list
	// (items carry ANSI styling, so check the pieces separately).
	require.Contains(t, content, "• ")
	require.Contains(t, content, "thread-11")
	require.NotContains(t, content, "thread-0 keeps running long enough to need the scroll")
}

func TestMemoryStatusReportsOffThroughTheNote(t *testing.T) {
	t.Parallel()

	m := newMemoryStatusTestDialog(t)
	m.HandleMsg(MemoryStatusLoadedMsg{Note: "Memory is off: nothing to report."})

	// Rendering with no sections keeps the note as the subtitle and the
	// frame intact; a load error surfaces in the body region rather than
	// panic.
	out := m.render(60, 8)
	require.NotEmpty(t, out)
	require.Contains(t, out, "Memory is off")
	m.HandleMsg(MemoryStatusLoadedMsg{Err: errStatusForTest})
	require.NotEmpty(t, m.render(60, 8))
}

var errStatusForTest = &statusError{}

type statusError struct{}

func (*statusError) Error() string { return "boom" }
