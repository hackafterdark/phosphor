package ui

import (
	"testing"

	"github.com/hackafterdark/phosphor/internal/memory"
	"github.com/hackafterdark/phosphor/pkg/message"
	"github.com/stretchr/testify/require"
)

func memoryResult(toolName, content string) message.Message {
	var msg message.Message
	msg.AddToolResult(message.ToolResult{
		ToolCallID: "call-1",
		Name:       toolName,
		Content:    content,
	})
	return msg
}

func TestSourcesFromMessagesReadsTheStoredCards(t *testing.T) {
	t.Parallel()

	search := memoryResult(memory.SearchToolName, Card(2, []Source{
		{ID: "a", Type: "decision", Status: "active", Summary: "First.", Trust: 0.8, Role: RoleRecalled},
		{ID: "b", Type: "constraint", Status: "active", Summary: "Second.", Trust: 0.5, Role: RoleRecalled},
	}))
	write := memoryResult(memory.ToolName, CardLabeled("Touched ", 1, []Source{
		{ID: "b", Type: "constraint", Status: "active", Summary: "Second.", Trust: 0.5, Role: RoleActed},
		{ID: "c", Type: "plan", Status: "active", Summary: "Third.", Trust: 0.5, Role: RoleActed},
	}))

	got := SourcesFromMessages([]message.Message{search, write})

	require.Len(t, got, 3, "one entry recalled and then touched is still one memory")
	require.Equal(t, "a", got[0].ID)
	require.Equal(t, "b", got[1].ID)
	require.Equal(t, RoleRecalled, got[1].Role, "the role of the first sighting is the one that stands")
	require.Equal(t, "c", got[2].ID)
}

func TestSourcesFromMessagesIgnoresWhatIsNotACard(t *testing.T) {
	t.Parallel()

	other := message.Message{}
	other.AddToolResult(message.ToolResult{ToolCallID: "x", Name: "bash", Content: "Recalled 1 memory:\n1. [fake] (nope)\n"})
	failed := message.Message{}
	failed.AddToolResult(message.ToolResult{ToolCallID: "y", Name: memory.ToolName, Content: Card(1, []Source{{ID: "z"}}), IsError: true})
	prose := memoryResult(memory.ReadToolName, "some fetched body that is not a card at all")

	require.Nil(t, SourcesFromMessages([]message.Message{other, failed, prose}))
}

func TestTurnFootprintTakesTheLastWrite(t *testing.T) {
	t.Parallel()

	first := memoryResult(memory.ToolName, "Committed.\nInjected window: 100/2048 bytes. Corpus: 1 active, 0 pending, 0 retired.")
	second := memoryResult(memory.ToolName, "Committed.\nInjected window: 300/2048 bytes. Corpus: 2 active, 1 pending, 0 retired.")

	foot, ok := TurnFootprint([]message.Message{first, second})
	require.True(t, ok)
	require.Equal(t, 300, foot.Injected, "the number that matters for the next write is the newest one")
	require.Equal(t, 2, foot.Active)

	_, ok = TurnFootprint(nil)
	require.False(t, ok)
}
