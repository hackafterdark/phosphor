package agent

import (
	"testing"

	"github.com/hackafterdark/phosphor/pkg/csync"
	"github.com/hackafterdark/phosphor/pkg/message"
	"github.com/stretchr/testify/require"
)

func TestTurnText(t *testing.T) {
	t.Parallel()

	t.Run("prefers the assistant text and trims it", func(t *testing.T) {
		m := &message.Message{
			Role:  message.Assistant,
			Parts: []message.ContentPart{message.TextContent{Text: "  hello there  "}},
		}
		require.Equal(t, "hello there", turnText(m, nil))
	})

	t.Run("blank assistant text with no result is empty", func(t *testing.T) {
		m := &message.Message{
			Role:  message.Assistant,
			Parts: []message.ContentPart{message.TextContent{Text: "   "}},
		}
		require.Equal(t, "", turnText(m, nil))
	})

	t.Run("nil everything is empty", func(t *testing.T) {
		require.Equal(t, "", turnText(nil, nil))
	})
}

func TestTurnToolNamesNilResult(t *testing.T) {
	t.Parallel()
	require.Nil(t, turnToolNames(nil))
}

func TestQueueAndTakeNote(t *testing.T) {
	t.Parallel()

	a := &sessionAgent{pendingNotes: csync.NewMap[string, string]()}

	a.queueNote("sess-1", "first")
	a.queueNote("sess-1", "second")
	require.Equal(t, "first\nsecond", a.takeNote("sess-1"))

	// takeNote is consuming: a second read returns nothing.
	require.Equal(t, "", a.takeNote("sess-1"))
}

func TestQueueNoteIgnoresBlank(t *testing.T) {
	t.Parallel()

	a := &sessionAgent{pendingNotes: csync.NewMap[string, string]()}
	a.queueNote("sess-1", "   ")

	_, ok := a.pendingNotes.Get("sess-1")
	require.False(t, ok)
}
