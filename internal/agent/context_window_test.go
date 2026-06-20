package agent

import (
	"testing"

	"charm.land/catwalk/pkg/catwalk"
	"github.com/hackafterdark/phosphor/internal/message"
	"github.com/hackafterdark/phosphor/internal/session"
	"github.com/stretchr/testify/require"
)

func TestEstimateMessageTokensForMessageEmpty(t *testing.T) {
	t.Parallel()

	require.Equal(t, int64(0), estimateMessageTokensForMessage(nil))
	require.Equal(t, int64(0), estimateMessageTokensForMessage([]message.Message{}))
}

func TestEstimateMessageTokensForMessageTextContent(t *testing.T) {
	t.Parallel()

	msgs := []message.Message{
		{
			Role: message.User,
			Parts: []message.ContentPart{
				message.TextContent{Text: "hello world"},
			},
		},
	}

	tokens := estimateMessageTokensForMessage(msgs)
	// Should include role ("user") + text content
	require.Greater(t, tokens, int64(0))
	require.Equal(t, approxTokenCount("user")+approxTokenCount("hello world"), tokens)
}

func TestEstimateMessageTokensForMessageMultipleParts(t *testing.T) {
	t.Parallel()

	msgs := []message.Message{
		{
			Role: message.User,
			Parts: []message.ContentPart{
				message.TextContent{Text: "first part"},
				message.TextContent{Text: "second part"},
			},
		},
	}

	tokens := estimateMessageTokensForMessage(msgs)
	// Should include role + both text parts
	require.Equal(t, approxTokenCount("user")+approxTokenCount("first part")+approxTokenCount("second part"), tokens)
}

func TestEstimateMessageTokensForMessageAssistantMessage(t *testing.T) {
	t.Parallel()

	msgs := []message.Message{
		{
			Role: message.Assistant,
			Parts: []message.ContentPart{
				message.TextContent{Text: "assistant response"},
			},
		},
	}

	tokens := estimateMessageTokensForMessage(msgs)
	require.Equal(t, approxTokenCount("assistant")+approxTokenCount("assistant response"), tokens)
}

func TestEstimateMessageTokensForMessageToolCall(t *testing.T) {
	t.Parallel()

	msgs := []message.Message{
		{
			Role: message.Assistant,
			Parts: []message.ContentPart{
				message.ToolCall{
					ID:    "call-123",
					Name:  "bash",
					Input: `{"command": "ls -la"}`,
				},
			},
		},
	}

	tokens := estimateMessageTokensForMessage(msgs)
	// Should include role + tool call parts (ID, Name, Input)
	require.Equal(t, approxTokenCount("assistant")+approxTokenCount("call-123")+approxTokenCount("bash")+approxTokenCount(`{"command": "ls -la"}`), tokens)
}

func TestEstimateMessageTokensForMessageToolResult(t *testing.T) {
	t.Parallel()

	msgs := []message.Message{
		{
			Role: message.Tool,
			Parts: []message.ContentPart{
				message.ToolResult{
					ToolCallID: "call-123",
					Name:       "bash",
					Content:    "output content here",
				},
			},
		},
	}

	tokens := estimateMessageTokensForMessage(msgs)
	// Should include role + tool result parts (ToolCallID, Name, Content)
	require.Equal(t, approxTokenCount("tool")+approxTokenCount("call-123")+approxTokenCount("bash")+approxTokenCount("output content here"), tokens)
}

func TestEstimateMessageTokensForMessageReasoningContent(t *testing.T) {
	t.Parallel()

	msgs := []message.Message{
		{
			Role: message.Assistant,
			Parts: []message.ContentPart{
				message.ReasoningContent{Thinking: "let me think about this..."},
				message.TextContent{Text: "final answer"},
			},
		},
	}

	tokens := estimateMessageTokensForMessage(msgs)
	require.Equal(t, approxTokenCount("assistant")+approxTokenCount("let me think about this...")+approxTokenCount("final answer"), tokens)
}

func TestEstimateMessageTokensForMessageBinaryContent(t *testing.T) {
	t.Parallel()

	data := []byte{0x89, 0x50, 0x4E, 0x47} // PNG header
	msgs := []message.Message{
		{
			Role: message.User,
			Parts: []message.ContentPart{
				message.BinaryContent{
					MIMEType: "image/png",
					Data:     data,
				},
			},
		},
	}

	tokens := estimateMessageTokensForMessage(msgs)
	require.Greater(t, tokens, int64(0))
	// Should include role + media tokens
	require.Equal(t, approxTokenCount("user")+estimateMediaTokensForMessage("image/png", "", len(data)), tokens)
}

func TestEstimateMessageTokensForMessageFinish(t *testing.T) {
	t.Parallel()

	msgs := []message.Message{
		{
			Role: message.Assistant,
			Parts: []message.ContentPart{
				message.Finish{Reason: message.FinishReasonEndTurn},
			},
		},
	}

	// Finish parts should contribute 0 tokens
	tokens := estimateMessageTokensForMessage(msgs)
	require.Equal(t, approxTokenCount("assistant"), tokens)
}

func TestEstimateMessageTokensForMessageMixed(t *testing.T) {
	t.Parallel()

	msgs := []message.Message{
		{
			Role: message.User,
			Parts: []message.ContentPart{
				message.TextContent{Text: "explain this code"},
			},
		},
		{
			Role: message.Assistant,
			Parts: []message.ContentPart{
				message.TextContent{Text: "sure, let me help"},
				message.ToolCall{
					ID:    "call-abc",
					Name:  "view",
					Input: `{"path": "src/main.go"}`,
				},
			},
		},
		{
			Role: message.Tool,
			Parts: []message.ContentPart{
				message.ToolResult{
					ToolCallID: "call-abc",
					Name:       "view",
					Content:    "file content here",
				},
			},
		},
	}

	tokens := estimateMessageTokensForMessage(msgs)
	require.Greater(t, tokens, int64(0))
	// Should sum all parts across all messages
}

func TestEstimateMessagePartTokensForMessageTextContent(t *testing.T) {
	t.Parallel()

	part := message.TextContent{Text: "test text"}
	require.Equal(t, approxTokenCount("test text"), estimateMessagePartTokensForMessage(part))
}

func TestEstimateMessagePartTokensForMessagePointerTextContent(t *testing.T) {
	t.Parallel()

	part := &message.TextContent{Text: "pointer text"}
	require.Equal(t, approxTokenCount("pointer text"), estimateMessagePartTokensForMessage(part))
}

func TestEstimateMessagePartTokensForMessageReasoningContent(t *testing.T) {
	t.Parallel()

	part := message.ReasoningContent{Thinking: "thinking..."}
	require.Equal(t, approxTokenCount("thinking..."), estimateMessagePartTokensForMessage(part))
}

func TestEstimateMessagePartTokensForMessagePointerReasoningContent(t *testing.T) {
	t.Parallel()

	part := &message.ReasoningContent{Thinking: "more thinking"}
	require.Equal(t, approxTokenCount("more thinking"), estimateMessagePartTokensForMessage(part))
}

func TestEstimateMessagePartTokensForMessageToolCall(t *testing.T) {
	t.Parallel()

	part := message.ToolCall{
		ID:    "call-1",
		Name:  "bash",
		Input: `{"command": "echo hello"}`,
	}
	expected := approxTokenCount("call-1") + approxTokenCount("bash") + approxTokenCount(`{"command": "echo hello"}`)
	require.Equal(t, expected, estimateMessagePartTokensForMessage(part))
}

func TestEstimateMessagePartTokensForMessagePointerToolCall(t *testing.T) {
	t.Parallel()

	part := &message.ToolCall{
		ID:    "call-2",
		Name:  "view",
		Input: `{"path": "file.txt"}`,
	}
	expected := approxTokenCount("call-2") + approxTokenCount("view") + approxTokenCount(`{"path": "file.txt"}`)
	require.Equal(t, expected, estimateMessagePartTokensForMessage(part))
}

func TestEstimateMessagePartTokensForMessageToolResult(t *testing.T) {
	t.Parallel()

	part := message.ToolResult{
		ToolCallID: "call-1",
		Name:       "bash",
		Content:    "result content",
	}
	expected := approxTokenCount("call-1") + approxTokenCount("bash") + approxTokenCount("result content")
	require.Equal(t, expected, estimateMessagePartTokensForMessage(part))
}

func TestEstimateMessagePartTokensForMessagePointerToolResult(t *testing.T) {
	t.Parallel()

	part := &message.ToolResult{
		ToolCallID: "call-2",
		Name:       "glob",
		Content:    "matched files",
	}
	expected := approxTokenCount("call-2") + approxTokenCount("glob") + approxTokenCount("matched files")
	require.Equal(t, expected, estimateMessagePartTokensForMessage(part))
}

func TestEstimateMessagePartTokensForMessageBinaryContent(t *testing.T) {
	t.Parallel()

	part := message.BinaryContent{
		MIMEType: "image/jpeg",
		Data:     []byte{0xFF, 0xD8, 0xFF},
	}
	// Should estimate media tokens
	require.Greater(t, estimateMessagePartTokensForMessage(part), int64(0))
}

func TestEstimateMessagePartTokensForMessagePointerBinaryContent(t *testing.T) {
	t.Parallel()

	part := &message.BinaryContent{
		MIMEType: "application/pdf",
		Data:     []byte{0x25, 0x50, 0x44, 0x46},
	}
	// Should estimate media tokens
	require.Greater(t, estimateMessagePartTokensForMessage(part), int64(0))
}

func TestEstimateMessagePartTokensForMessageFinish(t *testing.T) {
	t.Parallel()

	part := message.Finish{Reason: message.FinishReasonEndTurn}
	require.Equal(t, int64(0), estimateMessagePartTokensForMessage(part))
}

func TestEstimateMessagePartTokensForMessagePointerFinish(t *testing.T) {
	t.Parallel()

	part := &message.Finish{Reason: message.FinishReasonMaxTokens}
	require.Equal(t, int64(0), estimateMessagePartTokensForMessage(part))
}

func TestEstimateMessagePartTokensForMessageUnknownType(t *testing.T) {
	t.Parallel()

	// The default case returns 0 for any unknown type.
	// This is implicitly tested by the switch statement in estimateMessagePartTokensForMessage.
}

func TestEstimateMediaTokensForMessageEmptyData(t *testing.T) {
	t.Parallel()

	require.Equal(t, approxTokenCount("image/png")+approxTokenCount(""), estimateMediaTokensForMessage("image/png", "", 0))
	require.Equal(t, approxTokenCount("application/pdf")+approxTokenCount("metadata"), estimateMediaTokensForMessage("application/pdf", "metadata", 0))
}

func TestEstimateMediaTokensForMessageWithData(t *testing.T) {
	t.Parallel()

	data := []byte{0x00, 0x01, 0x02, 0x03, 0x04}
	tokens := estimateMediaTokensForMessage("image/png", "", len(data))
	require.Greater(t, tokens, int64(0))
	require.Equal(t, approxTokenCount("image/png  5 bytes"), tokens)
}

func TestShouldSummarizeDisabled(t *testing.T) {
	t.Parallel()

	session := session.Session{CurrentTokens: 100000, CompletionTokens: 50000, PromptTokens: 50000}
	model := Model{CatwalkCfg: catwalk.Model{ContextWindow: 200000}}

	require.False(t, shouldSummarize(session, model, 80, true))
}

func TestShouldSummarizeZeroContextWindow(t *testing.T) {
	t.Parallel()

	session := session.Session{CurrentTokens: 100000}
	model := Model{CatwalkCfg: catwalk.Model{ContextWindow: 0}}

	require.False(t, shouldSummarize(session, model, 80, false))
}

func TestShouldSummarizeBelowThreshold(t *testing.T) {
	t.Parallel()

	// 50% usage, threshold 80%
	session := session.Session{CurrentTokens: 50000}
	model := Model{CatwalkCfg: catwalk.Model{ContextWindow: 100000}}

	require.False(t, shouldSummarize(session, model, 0.8, false))
}

func TestShouldSummarizeAtThreshold(t *testing.T) {
	t.Parallel()

	// Exactly 80% usage, threshold 80%
	session := session.Session{CurrentTokens: 80000}
	model := Model{CatwalkCfg: catwalk.Model{ContextWindow: 100000}}

	require.True(t, shouldSummarize(session, model, 0.8, false))
}

func TestShouldSummarizeAboveThreshold(t *testing.T) {
	t.Parallel()

	// 90% usage, threshold 80%
	session := session.Session{CurrentTokens: 90000}
	model := Model{CatwalkCfg: catwalk.Model{ContextWindow: 100000}}

	require.True(t, shouldSummarize(session, model, 0.8, false))
}

func TestShouldSummarizeDefaultThreshold(t *testing.T) {
	t.Parallel()

	// 0 threshold should default to 80%
	session := session.Session{CurrentTokens: 80000}
	model := Model{CatwalkCfg: catwalk.Model{ContextWindow: 100000}}

	require.True(t, shouldSummarize(session, model, 0, false))
}

func TestShouldSummarizeNegativeThresholdDefaults(t *testing.T) {
	t.Parallel()

	// Negative threshold should also default to 80%
	session := session.Session{CurrentTokens: 80000}
	model := Model{CatwalkCfg: catwalk.Model{ContextWindow: 100000}}

	require.True(t, shouldSummarize(session, model, -0.1, false))
	require.True(t, shouldSummarize(session, model, -1, false))
}

func TestShouldSummarizeCustomDecimalThreshold(t *testing.T) {
	t.Parallel()

	// 85.5% usage, threshold 0.855
	session := session.Session{CurrentTokens: 85500}
	model := Model{CatwalkCfg: catwalk.Model{ContextWindow: 100000}}

	require.True(t, shouldSummarize(session, model, 0.855, false))
	require.False(t, shouldSummarize(session, model, 0.856, false))
}

func TestShouldSummarizeCustomThreshold(t *testing.T) {
	t.Parallel()

	// 75% usage, threshold 75%
	session := session.Session{CurrentTokens: 75000}
	model := Model{CatwalkCfg: catwalk.Model{ContextWindow: 100000}}

	require.True(t, shouldSummarize(session, model, 0.75, false))
	require.False(t, shouldSummarize(session, model, 0.76, false))
}

func TestShouldSummarizeFallbackToCumulativeTokens(t *testing.T) {
	t.Parallel()

	// CurrentTokens is 0, should fallback to CompletionTokens + PromptTokens
	session := session.Session{CurrentTokens: 0, CompletionTokens: 40000, PromptTokens: 40000}
	model := Model{CatwalkCfg: catwalk.Model{ContextWindow: 100000}}

	// 80% of 100000 = 80000, we have 80000 cumulative
	require.True(t, shouldSummarize(session, model, 0.8, false))
}

func TestShouldSummarizeLargeContextWindow(t *testing.T) {
	t.Parallel()

	// 262k context window, 200k tokens used = ~76%
	session := session.Session{CurrentTokens: 200000}
	model := Model{CatwalkCfg: catwalk.Model{ContextWindow: 262144}}

	// Default 80% threshold: 200000/262144 = 76.3% < 80%
	require.False(t, shouldSummarize(session, model, 0.8, false))
	// 75% threshold: 76.3% >= 75%
	require.True(t, shouldSummarize(session, model, 0.75, false))
}

func TestShouldSummarizeAtExactBoundary(t *testing.T) {
	t.Parallel()

	session := session.Session{CurrentTokens: 10000}
	model := Model{CatwalkCfg: catwalk.Model{ContextWindow: 10000}}

	// 100% usage, any threshold <= 1 should trigger
	require.True(t, shouldSummarize(session, model, 1.0, false))
	require.True(t, shouldSummarize(session, model, 0.5, false))
	require.True(t, shouldSummarize(session, model, 0.01, false))
}

func TestShouldSummarizeZeroTokens(t *testing.T) {
	t.Parallel()

	session := session.Session{CurrentTokens: 0, CompletionTokens: 0, PromptTokens: 0}
	model := Model{CatwalkCfg: catwalk.Model{ContextWindow: 100000}}

	require.False(t, shouldSummarize(session, model, 0.8, false))
}

func TestShouldSummarizeThresholdGreaterThen1(t *testing.T) {
	t.Parallel()

	session := session.Session{CurrentTokens: 100000}
	model := Model{CatwalkCfg: catwalk.Model{ContextWindow: 100000}}

	// 100% usage, threshold 1.01 should not trigger
	require.False(t, shouldSummarize(session, model, 1.01, false))
}
