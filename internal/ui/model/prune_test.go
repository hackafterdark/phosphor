package model

import (
	"testing"

	"github.com/hackafterdark/phosphor/internal/config"
	"github.com/hackafterdark/phosphor/internal/message"
	"github.com/hackafterdark/phosphor/internal/ui/chat"
	"github.com/hackafterdark/phosphor/internal/ui/styles"
	"github.com/stretchr/testify/require"
)

func TestUI_PruneHistoryIfNeeded(t *testing.T) {
	// Initialize UI with mock configuration
	historyLimit := 3
	historyBatch := 2
	cfg := &config.Config{
		Options: &config.Options{
			TUI: &config.TUIOptions{
				HistoryLimit:     &historyLimit,
				HistoryBatchSize: &historyBatch,
			},
		},
	}

	ui := newTestUIWithConfig(t, cfg)
	ui.chat = NewChat(ui.com)

	// Inject styles to avoid nil dereference when rendering items
	s := styles.CharmtonePantera()
	ui.com.Styles = &s

	// Simulate adding messages to m.allSessionMessages and m.chat
	// Max trigger threshold is historyLimit * 2 = 6.
	// Let's add 7 messages.
	for i := 1; i <= 7; i++ {
		msgID := string(rune('a' + i))
		msg := message.Message{
			ID:   msgID,
			Role: message.User,
		}
		ui.allSessionMessages = append(ui.allSessionMessages, msg)
		ui.loadedMessagesCount++

		// Extract message items and append
		items := chat.ExtractMessageItems(ui.com.Styles, &msg, nil)
		ui.chat.AppendMessages(items...)
	}

	require.Equal(t, 7, ui.loadedMessagesCount)
	require.Equal(t, 7, ui.chat.Len())

	// Call pruneHistoryIfNeeded. Since loadedMessagesCount (7) > historyLimit * 2 (6),
	// it should prune down to historyLimit (3).
	ui.pruneHistoryIfNeeded()

	// After pruning, loadedMessagesCount should be 3
	require.Equal(t, 3, ui.loadedMessagesCount)

	// A LoadMoreItem should have been prepended to the top, so total items in chat is 4
	require.Equal(t, 4, ui.chat.Len())

	// Index 0 should be the LoadMoreItem
	_, ok := ui.chat.ItemAt(0).(*chat.LoadMoreItem)
	require.True(t, ok, "Expected first item to be LoadMoreItem")
}

func TestUI_PruneHistoryWithToolMessages(t *testing.T) {
	// Initialize UI with mock configuration
	historyLimit := 2
	historyBatch := 1
	cfg := &config.Config{
		Options: &config.Options{
			TUI: &config.TUIOptions{
				HistoryLimit:     &historyLimit,
				HistoryBatchSize: &historyBatch,
			},
		},
	}

	ui := newTestUIWithConfig(t, cfg)
	ui.chat = NewChat(ui.com)

	// Inject styles to avoid nil dereference when rendering items
	s := styles.CharmtonePantera()
	ui.com.Styles = &s

	// We will add:
	// 1. User Message (A) - Top level
	// 2. Assistant Message (B) - Top level
	// 3. Tool Message (C) - Non top level
	// 4. User Message (D) - Top level
	// 5. Assistant Message (E) - Top level
	// 6. Tool Message (F) - Non top level
	// Total top level: 4 (A, B, D, E)
	// limit * 2 = 4.
	// Let's add one more top-level to trigger pruning:
	// 7. User Message (G) - Top level
	// Total top level: 5 (A, B, D, E, G).
	// Since 5 > 4 (limit * 2), pruning should trigger.
	
	msgs := []message.Message{
		{ID: "msg-A", Role: message.User},
		{ID: "msg-B", Role: message.Assistant},
		{ID: "msg-C", Role: message.Tool},
		{ID: "msg-D", Role: message.User},
		{ID: "msg-E", Role: message.Assistant},
		{ID: "msg-F", Role: message.Tool},
		{ID: "msg-G", Role: message.User},
	}

	for _, msg := range msgs {
		ui.allSessionMessages = append(ui.allSessionMessages, msg)
		if msg.Role == message.User || msg.Role == message.Assistant {
			ui.loadedMessagesCount++
		}
		
		// Only user/assistant messages produce visible top-level chat items in our test simulation
		if msg.Role == message.User || msg.Role == message.Assistant {
			items := chat.ExtractMessageItems(ui.com.Styles, &msg, nil)
			ui.chat.AppendMessages(items...)
		}
	}

	// Before pruning, loadedMessagesCount should be 5
	require.Equal(t, 5, ui.loadedMessagesCount)
	
	// We prune history.
	ui.pruneHistoryIfNeeded()

	// After pruning, loadedMessagesCount should be historyLimit (2)
	require.Equal(t, 2, ui.loadedMessagesCount)
	
	// Remaining top-level messages to load: 5 - 2 = 3
	// The LoadMoreItem should be prepended, plus msg-E and msg-G
	require.Equal(t, 3, ui.chat.Len())
	
	_, ok := ui.chat.ItemAt(0).(*chat.LoadMoreItem)
	require.True(t, ok, "Expected first item to be LoadMoreItem")
	
	// Now we click / trigger loadMoreMessages.
	// With historyBatch = 1, it should load 1 more top-level message (msg-D).
	ui.loadMoreMessages()
	
	// After loading 1 more, loadedMessagesCount should be 3 (msg-D, msg-E, msg-G)
	require.Equal(t, 3, ui.loadedMessagesCount)
	
	// In the chat, the new items should be: LoadMoreItem + msg-D + msg-E + msg-G = 4 items
	require.Equal(t, 4, ui.chat.Len())
}

