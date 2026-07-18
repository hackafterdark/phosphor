package agent

import (
	"errors"
	"strings"
	"testing"

	"github.com/hackafterdark/phosphor/pkg/message"
	"github.com/stretchr/testify/require"
)

// TestIsOverflowError verifies that overflow-related error keywords are detected.
func TestIsOverflowError(t *testing.T) {
	t.Parallel()

	// Errors that should match
	overflowCases := []struct {
		err   error
		matches bool
	}{
		{errors.New("context window overflow"), true},
		{errors.New("input exceeds context limit"), true},
		{errors.New("request too large for model"), true},
		{errors.New("token limit exceeded"), true},
		{errors.New("context length exceeded"), true},
		{errors.New("overflow detected"), true},
		{errors.New("some other error"), false},
		{errors.New("connection timeout"), false},
	}

	for _, tc := range overflowCases {
		result := isOverflowError(tc.err)
		if result != tc.matches {
			t.Errorf("isOverflowError(%q) = %v, want %v", tc.err.Error(), result, tc.matches)
		}
	}
}

// TestPruneToolOutputs_Truncation verifies that tool outputs longer than keepChars
// are truncated and marked.
func TestPruneToolOutputs_Truncation(t *testing.T) {
	t.Parallel()

	longText := strings.Repeat("x", 300)
	shortText := "short output"

	msgs := []message.Message{
		newMessage("tool", shortText),
		newMessage("tool", longText),
	}

	pruned := pruneToolOutputs(msgs, 100)
	require.Len(t, pruned, 2)

	// Short message should be unchanged
	require.Equal(t, shortText, pruned[0].Content().Text)
	// Long message should be truncated
	require.True(t, strings.HasSuffix(pruned[1].Content().Text, "\n... [truncated]"))
}

// TestPruneToolOutputs_NoToolMessages verifies that non-tool messages are untouched.
func TestPruneToolOutputs_NoToolMessages(t *testing.T) {
	t.Parallel()

	longText := strings.Repeat("y", 300)
	msgs := []message.Message{
		newPlainMessage("user", longText),
	}

	pruned := pruneToolOutputs(msgs, 100)
	require.Len(t, pruned, 1)
	require.Equal(t, longText, pruned[0].Content().Text)
}

// TestPruneToolOutputs_Empty verifies empty input is handled.
func TestPruneToolOutputs_Empty(t *testing.T) {
	t.Parallel()

	pruned := pruneToolOutputs([]message.Message{}, 200)
	require.Len(t, pruned, 0)
}

// TestPruneToolOutputs_ExactLength verifies messages exactly at the limit are not pruned.
func TestPruneToolOutputs_ExactLength(t *testing.T) {
	t.Parallel()

	exactText := strings.Repeat("z", 200)
	msgs := []message.Message{
		newMessage("tool", exactText),
	}

	pruned := pruneToolOutputs(msgs, 200)
	require.Len(t, pruned, 1)
	require.Equal(t, exactText, pruned[0].Content().Text)
}

// TestPruneAndBuild_Format verifies the output format.
func TestPruneAndBuild_Format(t *testing.T) {
	t.Parallel()

	msgs := []message.Message{
		newMessage("user", "hello"),
		newMessage("tool", strings.Repeat("t", 300)),
		newMessage("assistant", "received"),
	}

	result := PruneAndBuild(msgs, 10)
	require.True(t, strings.Contains(result, "[user] hello"))
	require.True(t, strings.Contains(result, "[assistant] received"))
	require.True(t, strings.Contains(result, "\n... [truncated]"))
}

// TestAggressiveBuild_Truncation verifies aggressive pruning uses 50 chars.
func TestAggressiveBuild_Truncation(t *testing.T) {
	t.Parallel()

	longText := strings.Repeat("a", 300)
	msgs := []message.Message{
		newMessage("tool", longText),
	}

	result := AggressiveBuild(msgs, 50)
	require.True(t, strings.Contains(result, "[tool] " + longText[:50]))
	require.True(t, strings.Contains(result, "\n... [truncated]"))
}

// newMessage creates a Message with a tool result part so pruning triggers.
func newMessage(role, text string) message.Message {
	parts := []message.ContentPart{
		message.TextContent{Text: text},
		message.ToolResult{
			ToolCallID: "call_1",
			Name:       "bash",
			Content:    text,
		},
	}
	return message.Message{
		Role:  message.MessageRole(role),
		Parts: parts,
	}
}

// newPlainMessage creates a Message without any tool result parts.
func newPlainMessage(role, text string) message.Message {
	return message.Message{
		Role:  message.MessageRole(role),
		Parts: []message.ContentPart{message.TextContent{Text: text}},
	}
}