package model

import (
	"strconv"
	"testing"

	"charm.land/bubbles/v2/textarea"
	"github.com/hackafterdark/phosphor/internal/ui/chat"
	"github.com/hackafterdark/phosphor/internal/ui/common"
	"github.com/hackafterdark/phosphor/pkg/message"
	"github.com/hackafterdark/phosphor/pkg/pubsub"
	"github.com/hackafterdark/phosphor/pkg/session"
)

// followTestItem is a tall message item so the chat list is scrollable.
type followTestItem struct {
	id   string
	text string
}

func (m followTestItem) ID() string           { return m.id }
func (m followTestItem) Render(int) string    { return m.text }
func (m followTestItem) RawRender(int) string { return m.text }
func (m followTestItem) Version() uint64      { return 0 }
func (m followTestItem) Finished() bool       { return true }

var _ chat.MessageItem = followTestItem{}

// WorkingDir keeps the embedded nil workspace interface from panicking when
// DefaultCommon queries the stub.
func (w *testWorkspace) WorkingDir() string { return "." }

func newFollowTestUI(t *testing.T) *UI {
	t.Helper()

	com := common.DefaultCommon(&testWorkspace{})

	ta := textarea.New()
	ta.SetStyles(com.Styles.Editor.Textarea)
	ta.ShowLineNumbers = false
	ta.CharLimit = -1
	ta.SetVirtualCursor(false)
	ta.DynamicHeight = true
	ta.MinHeight = TextareaMinHeight
	ta.MaxHeight = TextareaMaxHeight
	ta.Focus()

	u := &UI{
		com:      com,
		status:   NewStatus(com, nil),
		chat:     NewChat(com),
		textarea: ta,
		state:    uiChat,
		focus:    uiFocusEditor,
		width:    140,
		height:   45,
		session:  &session.Session{ID: "test-session"},
	}
	u.updateLayoutAndSize()

	msgs := make([]chat.MessageItem, 0, 60)
	for i := range 60 {
		msgs = append(msgs, followTestItem{
			id:   "m-" + strconv.Itoa(i),
			text: "message " + strconv.Itoa(i),
		})
	}
	u.chat.SetMessages(msgs...)
	return u
}

// TestScrolledUp_AgentTurnDoesNotScroll verifies that once the user scrolls
// up, new agent output during an active turn keeps the scroll position
// pinned where the user left it (follow mode).
func TestScrolledUp_AgentTurnDoesNotScroll(t *testing.T) {
	t.Parallel()

	u := newFollowTestUI(t)

	// User scrolls up to read earlier text.
	u.chat.ScrollBy(-20)
	if u.chat.Follow() {
		t.Fatal("expected follow mode to be disabled after scrolling up")
	}
	before := u.chat.list.Offset()

	// Agent turn: assistant message with a tool call arrives.
	assistant := message.Message{
		ID:        "a1",
		Role:      message.Assistant,
		SessionID: u.session.ID,
		Parts: []message.ContentPart{
			message.TextContent{Text: "working on it"},
			message.ToolCall{ID: "tc1", Name: "view", Input: `{"file_path":"x"}`, Finished: true},
		},
	}
	u.Update(pubsub.Event[message.Message]{Type: pubsub.CreatedEvent, Payload: assistant})
	if u.chat.list.Offset() != before {
		t.Fatal("expected scroll position to stay put when tool call item is appended")
	}

	// Streaming update to the assistant message.
	assistant.Parts = []message.ContentPart{
		message.TextContent{Text: "working on it, thinking harder and harder"},
		message.ToolCall{ID: "tc1", Name: "view", Input: `{"file_path":"x"}`, Finished: true},
	}
	u.Update(pubsub.Event[message.Message]{Type: pubsub.UpdatedEvent, Payload: assistant})
	if u.chat.list.Offset() != before {
		t.Fatal("expected scroll position to stay put during streaming update")
	}

	// Tool result arrives.
	toolMsg := message.Message{
		ID:        "t1",
		Role:      message.Tool,
		SessionID: u.session.ID,
		Parts: []message.ContentPart{
			message.ToolResult{ToolCallID: "tc1", Name: "view", Content: "tool output"},
		},
	}
	u.Update(pubsub.Event[message.Message]{Type: pubsub.CreatedEvent, Payload: toolMsg})
	if u.chat.list.Offset() != before {
		t.Fatal("expected scroll position to stay put when tool result arrives")
	}

	// End of turn: assistant info item is appended.
	assistant.Parts = []message.ContentPart{
		message.TextContent{Text: "done"},
		message.Finish{Reason: message.FinishReasonEndTurn, Time: 1},
	}
	u.Update(pubsub.Event[message.Message]{Type: pubsub.UpdatedEvent, Payload: assistant})
	if u.chat.list.Offset() != before {
		t.Fatal("expected scroll position to stay put at end of turn")
	}
}

// TestFollowMode_ReEnabledByScrollingDown verifies scrolling back down
// re-anchors the view to the bottom so new output follows again.
func TestFollowMode_ReEnabledByScrollingDown(t *testing.T) {
	t.Parallel()

	u := newFollowTestUI(t)

	u.chat.ScrollBy(-20)
	if u.chat.Follow() {
		t.Fatal("expected follow mode to be disabled after scrolling up")
	}

	// Scrolling down partway must not re-enable follow mode mid-history.
	u.chat.ScrollBy(5)
	if u.chat.Follow() {
		t.Fatal("expected follow mode to stay disabled while scrolled up")
	}

	// User scrolls back down to the bottom.
	u.chat.ScrollToBottom()
	if !u.chat.Follow() {
		t.Fatal("expected follow mode to be re-enabled at bottom")
	}

	assistant := message.Message{
		ID:        "a1",
		Role:      message.Assistant,
		SessionID: u.session.ID,
		Parts: []message.ContentPart{
			message.TextContent{Text: "working on it"},
		},
	}
	u.Update(pubsub.Event[message.Message]{Type: pubsub.CreatedEvent, Payload: assistant})
	if !u.chat.AtBottom() {
		t.Fatal("expected chat to follow new output while at bottom")
	}
}

// TestFollowMode_LargeScrollDownToBottom verifies a big positive delta that
// lands on the bottom re-enables follow mode, and agent output then follows.
func TestFollowMode_LargeScrollDownToBottom(t *testing.T) {
	t.Parallel()

	u := newFollowTestUI(t)

	u.chat.ScrollBy(-20)
	if u.chat.Follow() {
		t.Fatal("expected follow mode to be disabled after scrolling up")
	}

	// One big downward delta that clamps to the bottom region.
	u.chat.ScrollBy(1000)
	if !u.chat.Follow() {
		t.Fatal("expected follow mode to be re-enabled once the view reaches the bottom")
	}

	assistant := message.Message{
		ID:        "a1",
		Role:      message.Assistant,
		SessionID: u.session.ID,
		Parts: []message.ContentPart{
			message.TextContent{Text: "working on it"},
		},
	}
	u.Update(pubsub.Event[message.Message]{Type: pubsub.CreatedEvent, Payload: assistant})
	if !u.chat.AtBottom() {
		t.Fatal("expected chat to follow new output after returning to the bottom")
	}
}
