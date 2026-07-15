package acp

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/hackafterdark/phosphor/internal/app"
	"github.com/hackafterdark/phosphor/internal/backend"
	"github.com/hackafterdark/phosphor/pkg/message"
	"github.com/hackafterdark/phosphor/pkg/permission"
	"github.com/hackafterdark/phosphor/pkg/pubsub"
	"github.com/stretchr/testify/require"
)

// newTestService returns a Service with an in-memory encoder for testing.
func newTestService(t *testing.T) (*Service, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	s := &Service{
		logger:             slog.Default(),
		Encoder:            json.NewEncoder(&buf),
		sessions:           make(map[string]*sessionState),
		pending:            make(map[string]*pendingPrompt),
		seenText:           make(map[string]int),
		fullText:           make(map[string]string),
		seenThinking:       make(map[string]int),
		seenToolCallStatus: make(map[string]string),
		seenUserMsg:        make(map[string]bool),
		finalEmitted:       make(map[string]bool),
		lastAssistantMsg:   make(map[string]string),
	}
	return s, &buf
}

// readJSON reads the next JSON object from the buffer.
func readJSON(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()
	var obj map[string]any
	require.NoError(t, json.NewDecoder(buf).Decode(&obj))
	return obj
}

// ----- handleInitialize tests -----

func TestHandleInitialize(t *testing.T) {
	t.Parallel()

	s, buf := newTestService(t)

	req := &jsonrpcRequest{
		JSONRPC: "2.0",
		ID:      "init-1",
		Method:  "initialize",
		Params:  mustMarshal(t, initializeRequest{ProtocolVersion: 1}),
	}

	s.handleInitialize(context.Background(), req)

	out := readJSON(t, buf)
	require.Equal(t, "2.0", out["jsonrpc"])
	require.Equal(t, "init-1", out["id"])
	require.Nil(t, out["error"], "expected no error in response")

	result, ok := out["result"].(map[string]any)
	require.True(t, ok, "result should be an object")
	require.Equal(t, float64(1), result["protocolVersion"])

	agentInfo, ok := result["agentInfo"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "phosphor", agentInfo["name"])
	require.Equal(t, "Phosphor", agentInfo["title"])

	caps, ok := result["agentCapabilities"].(map[string]any)
	require.True(t, ok)

	sessionCaps, ok := caps["sessionCapabilities"].(map[string]any)
	require.True(t, ok)
	require.NotNil(t, sessionCaps["loadSession"])
	require.NotNil(t, sessionCaps["close"])
	require.NotNil(t, sessionCaps["delete"])
	require.NotNil(t, sessionCaps["resume"])
	require.NotNil(t, sessionCaps["setMode"])

	mcpCaps, ok := caps["mcpCapabilities"].(map[string]any)
	require.True(t, ok)
	require.NotNil(t, mcpCaps["stdio"])
}

func TestHandleInitialize_InvalidParams(t *testing.T) {
	t.Parallel()

	s, buf := newTestService(t)

	req := &jsonrpcRequest{
		JSONRPC: "2.0",
		ID:      "init-2",
		Method:  "initialize",
		Params:  json.RawMessage(`not valid json`),
	}

	s.handleInitialize(context.Background(), req)

	out := readJSON(t, buf)
	require.Equal(t, "init-2", out["id"])
	require.NotNil(t, out["error"], "expected error for invalid params")

	errObj := out["error"].(map[string]any)
	require.Equal(t, float64(jsonrpcInvalidParams), errObj["code"])
}

// ----- Unknown method test -----

func TestHandleRequest_UnknownMethod(t *testing.T) {
	t.Parallel()

	s, buf := newTestService(t)

	req := &jsonrpcRequest{
		JSONRPC: "2.0",
		ID:      "unknown-1",
		Method:  "session/nonexistent",
		Params:  json.RawMessage(`{}`),
	}

	s.handleRequest(context.Background(), req)

	out := readJSON(t, buf)
	require.Equal(t, "unknown-1", out["id"])
	require.NotNil(t, out["error"])

	errObj := out["error"].(map[string]any)
	require.Equal(t, float64(jsonrpcMethodNotFound), errObj["code"])
}

// ----- Event translation tests -----

func TestTranslateAndSend_MessageEvent(t *testing.T) {
	t.Parallel()

	s, buf := newTestService(t)

	innerMsg := message.Message{
		ID:   "msg-1",
		Role: message.User,
		Parts: []message.ContentPart{
			message.TextContent{Text: "Hello, world!"},
		},
	}
	innerEvent := pubsub.Event[message.Message]{Type: "message", Payload: innerMsg}
	outerEvent := pubsub.Event[tea.Msg]{Type: "message", Payload: innerEvent}

	s.translateAndSend("sess-1", outerEvent)

	out := readJSON(t, buf)
	require.Equal(t, "session/update", out["method"])

	params := out["params"].(map[string]any)
	require.Equal(t, "sess-1", params["sessionId"])

	update := params["update"].(map[string]any)
	require.Equal(t, "user_message_chunk", update["sessionUpdate"])
	require.Equal(t, "Hello, world!", update["text"])
}

func TestTranslateAndSend_AssistantTextMessage(t *testing.T) {
	t.Parallel()

	s, buf := newTestService(t)

	innerMsg := message.Message{
		ID:   "msg-2",
		Role: message.Assistant,
		Parts: []message.ContentPart{
			message.TextContent{Text: "I can help with that."},
		},
	}
	innerEvent := pubsub.Event[message.Message]{Type: "message", Payload: innerMsg}
	outerEvent := pubsub.Event[tea.Msg]{Type: "message", Payload: innerEvent}

	s.translateAndSend("sess-1", outerEvent)

	out := readJSON(t, buf)
	params := out["params"].(map[string]any)
	update := params["update"].(map[string]any)
	require.Equal(t, "agent_message_chunk", update["sessionUpdate"])
	require.Equal(t, "I can help with that.", update["text"])
	require.Equal(t, "msg-2", update["messageId"])
}

func TestTranslateAndSend_AssistantToolCall(t *testing.T) {
	t.Parallel()

	s, buf := newTestService(t)

	innerMsg := message.Message{
		ID:   "msg-3",
		Role: message.Assistant,
		Parts: []message.ContentPart{
			message.ToolCall{ID: "tc-1", Name: "bash", Input: `{"cmd":"ls"}`},
		},
	}
	innerEvent := pubsub.Event[message.Message]{Type: "message", Payload: innerMsg}
	outerEvent := pubsub.Event[tea.Msg]{Type: "message", Payload: innerEvent}

	s.translateAndSend("sess-1", outerEvent)

	out := readJSON(t, buf)
	params := out["params"].(map[string]any)
	update := params["update"].(map[string]any)
	require.Equal(t, "tool_call", update["sessionUpdate"])

	tc := update["toolCallId"].(string)
	require.Equal(t, "tc-1", tc)
	require.Equal(t, "Execute command: ls", update["title"])
	require.Equal(t, "execute", update["kind"])
	require.Equal(t, "pending", update["status"])
	require.Equal(t, map[string]any{"cmd": "ls"}, update["rawInput"])
}

func TestTranslateAndSend_ToolResult(t *testing.T) {
	t.Parallel()

	s, buf := newTestService(t)

	innerMsg := message.Message{
		ID:   "msg-4",
		Role: message.Assistant,
		Parts: []message.ContentPart{
			message.ToolResult{ToolCallID: "tc-1", Name: "bash", Content: "file1\nfile2", IsError: false},
		},
	}
	innerEvent := pubsub.Event[message.Message]{Type: "message", Payload: innerMsg}
	outerEvent := pubsub.Event[tea.Msg]{Type: "message", Payload: innerEvent}

	s.translateAndSend("sess-1", outerEvent)

	out := readJSON(t, buf)
	params := out["params"].(map[string]any)
	update := params["update"].(map[string]any)
	require.Equal(t, "tool_call_update", update["sessionUpdate"])
	require.Equal(t, "tc-1", update["toolCallId"])
	require.Equal(t, "completed", update["status"])
}

func TestTranslateAndSend_ToolResultError(t *testing.T) {
	t.Parallel()

	s, buf := newTestService(t)

	innerMsg := message.Message{
		ID:   "msg-5",
		Role: message.Assistant,
		Parts: []message.ContentPart{
			message.ToolResult{ToolCallID: "tc-2", Name: "bash", Content: "permission denied", IsError: true},
		},
	}
	innerEvent := pubsub.Event[message.Message]{Type: "message", Payload: innerMsg}
	outerEvent := pubsub.Event[tea.Msg]{Type: "message", Payload: innerEvent}

	s.translateAndSend("sess-1", outerEvent)

	dec := json.NewDecoder(buf)

	var out1 map[string]any
	require.NoError(t, dec.Decode(&out1))
	params1 := out1["params"].(map[string]any)
	update1 := params1["update"].(map[string]any)
	require.Equal(t, "agent_message_chunk", update1["sessionUpdate"])
	require.Contains(t, update1["text"].(string), "permission denied")

	var out2 map[string]any
	require.NoError(t, dec.Decode(&out2))
	params2 := out2["params"].(map[string]any)
	update2 := params2["update"].(map[string]any)
	require.Equal(t, "tool_call_update", update2["sessionUpdate"])
	require.Equal(t, "failed", update2["status"])
}

func TestTranslateAndSend_ReasoningContent(t *testing.T) {
	t.Parallel()

	s, buf := newTestService(t)

	innerMsg := message.Message{
		ID:   "msg-6",
		Role: message.Assistant,
		Parts: []message.ContentPart{
			message.ReasoningContent{Thinking: "Let me think about this..."},
		},
	}
	innerEvent := pubsub.Event[message.Message]{Type: "message", Payload: innerMsg}
	outerEvent := pubsub.Event[tea.Msg]{Type: "message", Payload: innerEvent}

	s.translateAndSend("sess-1", outerEvent)

	out := readJSON(t, buf)
	params := out["params"].(map[string]any)
	update := params["update"].(map[string]any)
	require.Equal(t, "agent_thought_chunk", update["sessionUpdate"])

	content := update["content"].(map[string]any)
	require.Equal(t, "text", content["type"])
	require.Equal(t, "Let me think about this...", content["text"])
	require.Equal(t, "msg-6", update["messageId"])
}

func TestTranslateAndSend_UnknownPayloadIgnored(t *testing.T) {
	t.Parallel()

	s, buf := newTestService(t)

	// Wrap a non-message payload.
	outerEvent := pubsub.Event[tea.Msg]{Type: "unknown", Payload: "just a string"}

	s.translateAndSend("sess-1", outerEvent)

	// Nothing should be written to the buffer.
	require.Empty(t, buf.String(), "expected no output for unknown payload type")
}

func TestTranslateAndSend_MixedParts(t *testing.T) {
	t.Parallel()

	s, buf := newTestService(t)

	innerMsg := message.Message{
		ID:   "msg-7",
		Role: message.Assistant,
		Parts: []message.ContentPart{
			message.ReasoningContent{Thinking: "Hmm..."},
			message.TextContent{Text: "The answer is 42."},
			message.ToolCall{ID: "tc-3", Name: "read_file", Input: `{}`},
		},
	}
	innerEvent := pubsub.Event[message.Message]{Type: "message", Payload: innerMsg}
	outerEvent := pubsub.Event[tea.Msg]{Type: "message", Payload: innerEvent}

	s.translateAndSend("sess-1", outerEvent)

	// Should produce 3 notifications.
	var updates []map[string]any
	dec := json.NewDecoder(buf)
	for dec.More() {
		var obj map[string]any
		require.NoError(t, dec.Decode(&obj))
		params := obj["params"].(map[string]any)
		updates = append(updates, params["update"].(map[string]any))
	}

	require.Len(t, updates, 3)
	require.Equal(t, "agent_thought_chunk", updates[0]["sessionUpdate"])
	require.Equal(t, "agent_message_chunk", updates[1]["sessionUpdate"])
	require.Equal(t, "tool_call", updates[2]["sessionUpdate"])

	require.Equal(t, "read", updates[2]["kind"], "read_file should map to 'read' kind")
}

func TestTranslateAndSend_AssistantStreamingToolCall(t *testing.T) {
	t.Parallel()

	s, buf := newTestService(t)

	// 1. Initial tool call with Finished: false (streaming start)
	innerMsg1 := message.Message{
		ID:   "msg-streaming",
		Role: message.Assistant,
		Parts: []message.ContentPart{
			message.ToolCall{ID: "tc-stream", Name: "view", Input: "", Finished: false},
		},
	}
	event1 := pubsub.Event[tea.Msg]{
		Type: "message",
		Payload: pubsub.Event[message.Message]{
			Type:    "message",
			Payload: innerMsg1,
		},
	}

	s.translateAndSend("sess-1", event1)

	// Decode the first notification
	out1 := readJSON(t, buf)
	params1 := out1["params"].(map[string]any)
	update1 := params1["update"].(map[string]any)
	require.Equal(t, "tool_call", update1["sessionUpdate"])
	require.Equal(t, "tc-stream", update1["toolCallId"])
	require.Equal(t, "pending", update1["status"])
	require.Nil(t, update1["rawInput"])

	// Reset buffer
	buf.Reset()

	// 2. Updated tool call with Finished: true (streaming finished) and Input
	innerMsg2 := message.Message{
		ID:   "msg-streaming",
		Role: message.Assistant,
		Parts: []message.ContentPart{
			message.ToolCall{ID: "tc-stream", Name: "view", Input: `{"path": "src/main.go"}`, Finished: true},
		},
	}
	event2 := pubsub.Event[tea.Msg]{
		Type: "message",
		Payload: pubsub.Event[message.Message]{
			Type:    "message",
			Payload: innerMsg2,
		},
	}

	s.translateAndSend("sess-1", event2)

	// Decode the update notification
	out2 := readJSON(t, buf)
	params2 := out2["params"].(map[string]any)
	update2 := params2["update"].(map[string]any)
	require.Equal(t, "tool_call_update", update2["sessionUpdate"])
	require.Equal(t, "tc-stream", update2["toolCallId"])
	require.Equal(t, "in_progress", update2["status"])
	require.Equal(t, "View file src/main.go", update2["title"])
	require.Equal(t, map[string]any{"path": "src/main.go"}, update2["rawInput"])
}

// ----- extractMessageText tests -----

func TestExtractMessageText(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		parts []message.ContentPart
		want  string
	}{
		{
			name:  "single text",
			parts: []message.ContentPart{message.TextContent{Text: "hello"}},
			want:  "hello",
		},
		{
			name: "multiple texts",
			parts: []message.ContentPart{
				message.TextContent{Text: "hello"},
				message.TextContent{Text: "world"},
			},
			want: "hello world",
		},
		{
			name: "text with tool call",
			parts: []message.ContentPart{
				message.TextContent{Text: "let me check"},
				message.ToolCall{ID: "tc-1", Name: "bash"},
				message.TextContent{Text: "done"},
			},
			want: "let me check done",
		},
		{
			name:  "no text parts",
			parts: []message.ContentPart{message.ToolCall{ID: "tc-1", Name: "bash"}},
			want:  "",
		},
		{
			name:  "empty parts",
			parts: nil,
			want:  "",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, extractMessageText(tt.parts))
		})
	}
}

// ----- extractTextFromBlocks tests -----

func TestExtractTextFromBlocks(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		blocks []contentBlock
		want   string
	}{
		{
			name:   "single text block",
			blocks: []contentBlock{{Type: "text", Text: "hello"}},
			want:   "hello",
		},
		{
			name: "multiple text blocks",
			blocks: []contentBlock{
				{Type: "text", Text: "hello"},
				{Type: "text", Text: "world"},
			},
			want: "hello world",
		},
		{
			name: "text with image block",
			blocks: []contentBlock{
				{Type: "text", Text: "look at this"},
				{Type: "image", Text: ""},
				{Type: "text", Text: "cool"},
			},
			want: "look at this cool",
		},
		{
			name:   "no text blocks",
			blocks: []contentBlock{{Type: "image", Text: ""}},
			want:   "",
		},
		{
			name:   "empty blocks",
			blocks: nil,
			want:   "",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, extractTextFromBlocks(tt.blocks))
		})
	}
}

// ----- mapToolKind tests -----

func TestMapToolKind(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		want toolCallKind
	}{
		{"bash", toolCallKindExecute},
		{"shell", toolCallKindExecute},
		{"read_file", toolCallKindRead},
		{"cat", toolCallKindRead},
		{"ls", toolCallKindRead},
		{"grep", toolCallKindSearch},
		{"glob", toolCallKindSearch},
		{"edit", toolCallKindEdit},
		{"write", toolCallKindEdit},
		{"delete_file", toolCallKindDelete},
		{"rm", toolCallKindDelete},
		{"move_file", toolCallKindMove},
		{"rename", toolCallKindMove},
		{"search", toolCallKindSearch},
		{"find", toolCallKindSearch},
		{"fetch", toolCallKindFetch},
		{"web_fetch", toolCallKindFetch},
		{"think", toolCallKindThink},
		{"reason", toolCallKindThink},
		{"some_unknown_tool", toolCallKindOther},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, mapToolKind(tt.name))
		})
	}
}

// ----- Response writing tests -----

func TestWriteResponse(t *testing.T) {
	t.Parallel()

	s, buf := newTestService(t)
	s.writeResponse("req-1", map[string]string{"key": "value"})

	out := readJSON(t, buf)
	require.Equal(t, "2.0", out["jsonrpc"])
	require.Equal(t, "req-1", out["id"])
	require.Nil(t, out["error"])

	result := out["result"].(map[string]any)
	require.Equal(t, "value", result["key"])
}

func TestWriteError(t *testing.T) {
	t.Parallel()

	s, buf := newTestService(t)
	s.writeError("req-2", jsonrpcInternalErr, "something broke")

	out := readJSON(t, buf)
	require.Equal(t, "2.0", out["jsonrpc"])
	require.Equal(t, "req-2", out["id"])

	errObj := out["error"].(map[string]any)
	require.Equal(t, float64(jsonrpcInternalErr), errObj["code"])
	require.Equal(t, "something broke", errObj["message"])
}

func TestWriteNotification(t *testing.T) {
	t.Parallel()

	s, buf := newTestService(t)
	s.writeNotification("session/update", map[string]string{"sessionId": "s1"})

	out := readJSON(t, buf)
	require.Equal(t, "2.0", out["jsonrpc"])
	require.Equal(t, "session/update", out["method"])
	require.Nil(t, out["id"], "notifications should not have an id")
}

// ----- sendSessionUpdate tests -----

func TestSendSessionUpdate(t *testing.T) {
	t.Parallel()

	s, buf := newTestService(t)
	s.sendSessionUpdate("sess-42", SessionUpdate{
		ModeChange: &modeChange{Mode: "plan"},
	})

	out := readJSON(t, buf)
	require.Equal(t, "session/update", out["method"])

	params := out["params"].(map[string]any)
	require.Equal(t, "sess-42", params["sessionId"])

	update := params["update"].(map[string]any)
	require.Equal(t, "mode_change", update["sessionUpdate"])
	require.Equal(t, "plan", update["mode"])
}

// ----- handleLogout test -----

func TestHandleLogout(t *testing.T) {
	t.Parallel()

	s, buf := newTestService(t)
	s.handleLogout(&jsonrpcRequest{ID: "logout-1"})

	out := readJSON(t, buf)
	require.Equal(t, "logout-1", out["id"])
	require.Nil(t, out["error"])
}

// ----- handleSessionClose test (with mock sessions map) -----

func TestHandleSessionClose_SessionNotFound(t *testing.T) {
	t.Parallel()

	s, buf := newTestService(t)

	req := &jsonrpcRequest{
		JSONRPC: "2.0",
		ID:      "close-1",
		Method:  "session/close",
		Params:  mustMarshal(t, sessionCloseRequest{SessionID: "nonexistent"}),
	}

	s.handleSessionClose(req)

	out := readJSON(t, buf)
	require.Equal(t, "close-1", out["id"])
	require.NotNil(t, out["error"])

	errObj := out["error"].(map[string]any)
	require.Equal(t, float64(jsonrpcInvalidReq), errObj["code"])
}

// ----- handleSessionDelete test -----

func TestHandleSessionDelete_SessionNotFound(t *testing.T) {
	t.Parallel()

	s, buf := newTestService(t)

	req := &jsonrpcRequest{
		JSONRPC: "2.0",
		ID:      "del-1",
		Method:  "session/delete",
		Params:  mustMarshal(t, sessionDeleteRequest{SessionID: "nonexistent"}),
	}

	s.handleSessionDelete(req)

	out := readJSON(t, buf)
	require.Equal(t, "del-1", out["id"])
	require.NotNil(t, out["error"])
}

// ----- handleSessionSetMode test -----

func TestHandleSessionSetMode(t *testing.T) {
	t.Parallel()

	s, buf := newTestService(t)
	// Pre-populate a session.
	s.mu.Lock()
	s.sessions["sess-1"] = &sessionState{sessionID: "sess-1", workspaceID: "ws-1"}
	s.mu.Unlock()

	req := &jsonrpcRequest{
		JSONRPC: "2.0",
		ID:      "mode-1",
		Method:  "session/set_mode",
		Params:  mustMarshal(t, sessionSetModeRequest{SessionID: "sess-1", Mode: "plan"}),
	}

	s.handleSessionSetMode(context.Background(), req)

	// Verify both notification and response are in the buffer.
	output := buf.String()
	require.Contains(t, output, `"method":"session/update"`)
	require.Contains(t, output, `"mode_change"`)
	require.Contains(t, output, `"mode":"plan"`)
	require.Contains(t, output, `"id":"mode-1"`)
	require.Contains(t, output, `"jsonrpc":"2.0"`)
}

func TestHandleSessionSetMode_SessionNotFound(t *testing.T) {
	t.Parallel()

	s, buf := newTestService(t)

	req := &jsonrpcRequest{
		JSONRPC: "2.0",
		ID:      "mode-2",
		Method:  "session/set_mode",
		Params:  mustMarshal(t, sessionSetModeRequest{SessionID: "nope", Mode: "plan"}),
	}

	s.handleSessionSetMode(context.Background(), req)

	out := readJSON(t, buf)
	require.Equal(t, "mode-2", out["id"])
	require.NotNil(t, out["error"])
}

// ----- handleSessionCancel test -----

func TestHandleSessionCancel_Notification(t *testing.T) {
	t.Parallel()

	s, buf := newTestService(t)
	// Pre-populate a session.
	s.mu.Lock()
	s.sessions["sess-1"] = &sessionState{sessionID: "sess-1", workspaceID: "ws-1"}
	s.mu.Unlock()

	req := &jsonrpcRequest{
		JSONRPC: "2.0",
		ID:      nil, // notifications have no ID
		Method:  "session/cancel",
		Params:  mustMarshal(t, sessionCancelRequest{SessionID: "sess-1"}),
	}

	s.handleSessionCancel(req)

	// Should send a cancellation notification (no response).
	out := readJSON(t, buf)
	require.Equal(t, "session/update", out["method"])
	params := out["params"].(map[string]any)
	update := params["update"].(map[string]any)
	require.Equal(t, "agent_message_chunk", update["sessionUpdate"])
	require.Equal(t, "[turn cancelled]", update["text"])
}

// ----- handleSessionPrompt test (session not found) -----

func TestHandleSessionPrompt_SessionNotFound(t *testing.T) {
	t.Parallel()

	s, buf := newTestService(t)

	req := &jsonrpcRequest{
		JSONRPC: "2.0",
		ID:      "prompt-1",
		Method:  "session/prompt",
		Params:  mustMarshal(t, sessionPromptRequest{SessionID: "nonexistent", Content: []contentBlock{{Type: "text", Text: "hi"}}}),
	}

	s.handleSessionPrompt(context.Background(), req)

	out := readJSON(t, buf)
	require.Equal(t, "prompt-1", out["id"])
	require.NotNil(t, out["error"])

	errObj := out["error"].(map[string]any)
	require.Equal(t, float64(jsonrpcInvalidReq), errObj["code"])
}

// ----- Helper -----

func mustMarshal(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return json.RawMessage(b)
}

// Suppress unused import warnings.
var _ = strings.TrimSpace

// TestHandleSessionPrompt_ZedPromptField verifies that Zed's "prompt" field
// (instead of the ACP spec's "content") produces extractable text even when
// mixed with resource blocks.
func TestHandleSessionPrompt_ZedPromptField(t *testing.T) {
	t.Parallel()

	blocks := []contentBlock{
		{Type: "text", Text: "can you explain what "},
		{Type: "resource", Resource: &resourceBlock{Text: "file content", URI: "file:///test.go"}},
		{Type: "text", Text: "  does?"},
	}

	text := extractTextFromBlocks(blocks)
	require.Equal(t, "can you explain what    does?", text)
}

// TestHandleSessionPrompt_OnlyContentField verifies the standard "content" field works.
func TestHandleSessionPrompt_OnlyContentField(t *testing.T) {
	t.Parallel()

	blocks := []contentBlock{
		{Type: "text", Text: "hello world"},
	}

	text := extractTextFromBlocks(blocks)
	require.Equal(t, "hello world", text)
}

func TestParseURItoPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		uri  string
		want string
	}{
		{"unix absolute file URI", "file:///etc/hosts", "/etc/hosts"},
		{"windows absolute file URI with triple slash", "file:///C:/Users/test/file.txt", "C:/Users/test/file.txt"},
		{"windows absolute file URI with double slash", "file://D:/workspace/file.txt", "D:/workspace/file.txt"},
		{"not a file URI", "http://example.com/foo", "http://example.com/foo"},
		{"empty URI", "", ""},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, parseURItoPath(tt.uri))
		})
	}
}

func TestExtractPromptAndAttachments(t *testing.T) {
	t.Parallel()

	blocks := []contentBlock{
		{Type: "text", Text: "Please examine "},
		{
			Type: "resource",
			Resource: &resourceBlock{
				URI:      "file:///F:/workspace/main.go",
				Name:     "main.go",
				MIMEType: "text/x-go",
				Text:     "package main\nfunc main() {}",
			},
		},
		{Type: "text", Text: " and tell me what it does. Also check "},
		{
			Type: "resource_link",
			ResourceLink: &resourceLinkBlock{
				URI:      "file:///F:/workspace/README.md",
				Name:     "README.md",
				MIMEType: "text/markdown",
			},
		},
		{Type: "text", Text: "."},
	}

	promptText, attachments := extractPromptAndAttachments(blocks)

	require.Equal(t, "Please examine [main.go] and tell me what it does. Also check [README.md].", promptText)
	require.Len(t, attachments, 2)

	require.Equal(t, "F:/workspace/main.go", attachments[0].FilePath)
	require.Equal(t, "main.go", attachments[0].FileName)
	require.Equal(t, "text/x-go", attachments[0].MimeType)
	require.Equal(t, []byte("package main\nfunc main() {}"), attachments[0].Content)

	require.Equal(t, "F:/workspace/README.md", attachments[1].FilePath)
	require.Equal(t, "README.md", attachments[1].FileName)
	require.Equal(t, "text/markdown", attachments[1].MimeType)
	require.Nil(t, attachments[1].Content)
}

type chanWriter struct {
	mu  *sync.Mutex
	buf *bytes.Buffer
	ch  chan []byte
}

func newChanWriter(ch chan []byte) chanWriter {
	return chanWriter{
		mu:  &sync.Mutex{},
		buf: &bytes.Buffer{},
		ch:  ch,
	}
}

func (cw chanWriter) Write(p []byte) (int, error) {
	cw.mu.Lock()
	defer cw.mu.Unlock()
	cw.buf.Write(p)
	for {
		b := cw.buf.Bytes()
		idx := bytes.IndexByte(b, '\n')
		if idx == -1 {
			break
		}
		line := make([]byte, idx+1)
		copy(line, b[:idx+1])
		cw.buf.Next(idx + 1)
		cw.ch <- line
	}
	return len(p), nil
}

func TestSendRequest(t *testing.T) {
	t.Parallel()

	ch := make(chan []byte, 10)
	cw := newChanWriter(ch)

	s := &Service{
		logger:         slog.Default(),
		Encoder:        json.NewEncoder(cw),
		clientRequests: make(map[string]chan *jsonrpcMessage),
	}

	go func() {
		data := <-ch
		var req jsonrpcMessage
		require.NoError(t, json.Unmarshal(data, &req))
		require.Equal(t, "session/request_permission", req.Method)

		resp := &jsonrpcMessage{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  mustMarshal(t, requestPermissionResponse{Outcome: requestPermissionOutcome{Outcome: "selected", OptionID: "allow_once"}}),
		}

		s.handleResponseMessage(resp)
	}()

	resp, err := s.sendRequest(context.Background(), "session/request_permission", map[string]any{"foo": "bar"})
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Nil(t, resp.Error)

	var respObj requestPermissionResponse
	err = json.Unmarshal(resp.Result, &respObj)
	require.NoError(t, err)
	require.Equal(t, "allow_once", respObj.Outcome.OptionID)
}

func TestHandlePermissionRequest_Allow(t *testing.T) {
	t.Parallel()

	ch := make(chan []byte, 10)
	cw := newChanWriter(ch)

	ctx := context.Background()
	b := backend.New(ctx, nil, nil)

	ws := &backend.Workspace{
		ID:   "ws-1",
		Path: t.TempDir(),
		App: &app.App{
			Permissions: permission.NewPermissionService("", false, nil),
		},
	}
	backend.InsertWorkspaceForTest(b, ws)
	backend.SetWorkspaceShutdownFnForTest(ws, func() {})

	s := &Service{
		backend:        b,
		logger:         slog.Default(),
		Encoder:        json.NewEncoder(cw),
		sessions:       make(map[string]*sessionState),
		clientRequests: make(map[string]chan *jsonrpcMessage),
	}

	s.sessions["sess-1"] = &sessionState{
		sessionID:   "sess-1",
		workspaceID: "ws-1",
	}

	go func() {
		data := <-ch
		var msg jsonrpcMessage
		require.NoError(t, json.Unmarshal(data, &msg))
		require.Equal(t, "session/request_permission", msg.Method)

		var rpcReq requestPermissionRequest
		require.NoError(t, json.Unmarshal(msg.Params, &rpcReq))
		require.Equal(t, "sess-1", rpcReq.SessionID)
		require.Equal(t, "tc-123", rpcReq.ToolCall.ToolCallID)
		require.Equal(t, "write", rpcReq.ToolCall.ToolName)
		require.Equal(t, "write", rpcReq.ToolCall.Title)
		require.Equal(t, "edit", rpcReq.ToolCall.Kind)
		require.Equal(t, "pending", rpcReq.ToolCall.Status)
		require.Empty(t, rpcReq.ToolCall.RawInput)

		resp := &jsonrpcMessage{
			JSONRPC: "2.0",
			ID:      msg.ID,
			Result:  mustMarshal(t, requestPermissionResponse{Outcome: requestPermissionOutcome{Outcome: "selected", OptionID: "allow_once"}}),
		}
		s.handleResponseMessage(resp)
	}()

	permSubCh := ws.Permissions.Subscribe(ctx)

	var requestGranted bool
	var requestErr error
	doneCh := make(chan struct{})
	go func() {
		requestGranted, requestErr = ws.Permissions.Request(ctx, permission.CreatePermissionRequest{
			SessionID:   "sess-1",
			ToolCallID:  "tc-123",
			ToolName:    "write",
			Description: "Write a file",
		})
		close(doneCh)
	}()

	var ev pubsub.Event[permission.PermissionRequest]
	select {
	case ev = <-permSubCh:
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for permission request event")
	}

	s.handlePermissionRequest("sess-1", ev.Payload)

	select {
	case <-doneCh:
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for ws.Permissions.Request to finish")
	}

	require.NoError(t, requestErr)
	require.True(t, requestGranted, "Expected permission request to be granted")
}

func TestHandlePermissionRequest_Deny(t *testing.T) {
	t.Parallel()

	ch := make(chan []byte, 10)
	cw := newChanWriter(ch)

	ctx := context.Background()
	b := backend.New(ctx, nil, nil)

	ws := &backend.Workspace{
		ID:   "ws-1",
		Path: t.TempDir(),
		App: &app.App{
			Permissions: permission.NewPermissionService("", false, nil),
		},
	}
	backend.InsertWorkspaceForTest(b, ws)
	backend.SetWorkspaceShutdownFnForTest(ws, func() {})

	s := &Service{
		backend:        b,
		logger:         slog.Default(),
		Encoder:        json.NewEncoder(cw),
		sessions:       make(map[string]*sessionState),
		clientRequests: make(map[string]chan *jsonrpcMessage),
	}

	s.sessions["sess-1"] = &sessionState{
		sessionID:   "sess-1",
		workspaceID: "ws-1",
	}

	go func() {
		data := <-ch
		var msg jsonrpcMessage
		require.NoError(t, json.Unmarshal(data, &msg))
		require.Equal(t, "session/request_permission", msg.Method)

		var rpcReq requestPermissionRequest
		require.NoError(t, json.Unmarshal(msg.Params, &rpcReq))
		require.Equal(t, "sess-1", rpcReq.SessionID)
		require.Equal(t, "tc-123", rpcReq.ToolCall.ToolCallID)
		require.Equal(t, "write", rpcReq.ToolCall.ToolName)
		require.Equal(t, "write", rpcReq.ToolCall.Title)
		require.Equal(t, "edit", rpcReq.ToolCall.Kind)
		require.Equal(t, "pending", rpcReq.ToolCall.Status)
		require.Empty(t, rpcReq.ToolCall.RawInput)

		resp := &jsonrpcMessage{
			JSONRPC: "2.0",
			ID:      msg.ID,
			Result:  mustMarshal(t, requestPermissionResponse{Outcome: requestPermissionOutcome{Outcome: "selected", OptionID: "reject_once"}}),
		}
		s.handleResponseMessage(resp)
	}()

	permSubCh := ws.Permissions.Subscribe(ctx)

	var requestGranted bool
	var requestErr error
	doneCh := make(chan struct{})
	go func() {
		requestGranted, requestErr = ws.Permissions.Request(ctx, permission.CreatePermissionRequest{
			SessionID:   "sess-1",
			ToolCallID:  "tc-123",
			ToolName:    "write",
			Description: "Write a file",
		})
		close(doneCh)
	}()

	var ev pubsub.Event[permission.PermissionRequest]
	select {
	case ev = <-permSubCh:
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for permission request event")
	}

	s.handlePermissionRequest("sess-1", ev.Payload)

	select {
	case <-doneCh:
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for ws.Permissions.Request to finish")
	}

	require.NoError(t, requestErr)
	require.False(t, requestGranted, "Expected permission request to be denied")
}

func TestHandlePermissionRequest_WithParams(t *testing.T) {
	t.Parallel()

	ch := make(chan []byte, 10)
	cw := newChanWriter(ch)

	ctx := context.Background()
	b := backend.New(ctx, nil, nil)

	ws := &backend.Workspace{
		ID:   "ws-1",
		Path: t.TempDir(),
		App: &app.App{
			Permissions: permission.NewPermissionService("", false, nil),
		},
	}
	backend.InsertWorkspaceForTest(b, ws)
	backend.SetWorkspaceShutdownFnForTest(ws, func() {})

	s := &Service{
		backend:        b,
		logger:         slog.Default(),
		Encoder:        json.NewEncoder(cw),
		sessions:       make(map[string]*sessionState),
		clientRequests: make(map[string]chan *jsonrpcMessage),
	}

	s.sessions["sess-1"] = &sessionState{
		sessionID:   "sess-1",
		workspaceID: "ws-1",
	}

	go func() {
		data := <-ch
		var msg jsonrpcMessage
		require.NoError(t, json.Unmarshal(data, &msg))
		require.Equal(t, "session/request_permission", msg.Method)

		var rpcReq requestPermissionRequest
		require.NoError(t, json.Unmarshal(msg.Params, &rpcReq))
		require.Equal(t, "sess-1", rpcReq.SessionID)
		require.Equal(t, "tc-123", rpcReq.ToolCall.ToolCallID)
		require.Equal(t, "write", rpcReq.ToolCall.ToolName)
		require.Equal(t, "Write file main.go", rpcReq.ToolCall.Title)
		require.Equal(t, "edit", rpcReq.ToolCall.Kind)
		require.Equal(t, "pending", rpcReq.ToolCall.Status)

		var raw map[string]any
		require.NoError(t, json.Unmarshal(rpcReq.ToolCall.RawInput, &raw))
		require.Equal(t, "main.go", raw["TargetFile"])

		resp := &jsonrpcMessage{
			JSONRPC: "2.0",
			ID:      msg.ID,
			Result:  mustMarshal(t, requestPermissionResponse{Outcome: requestPermissionOutcome{Outcome: "selected", OptionID: "allow_once"}}),
		}
		s.handleResponseMessage(resp)
	}()

	permSubCh := ws.Permissions.Subscribe(ctx)

	var requestGranted bool
	var requestErr error
	doneCh := make(chan struct{})
	go func() {
		requestGranted, requestErr = ws.Permissions.Request(ctx, permission.CreatePermissionRequest{
			SessionID:   "sess-1",
			ToolCallID:  "tc-123",
			ToolName:    "write",
			Description: "Write a file",
			Params:      map[string]any{"TargetFile": "main.go"},
		})
		close(doneCh)
	}()

	var ev pubsub.Event[permission.PermissionRequest]
	select {
	case ev = <-permSubCh:
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for permission request event")
	}

	s.handlePermissionRequest("sess-1", ev.Payload)

	select {
	case <-doneCh:
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for ws.Permissions.Request to finish")
	}

	require.NoError(t, requestErr)
	require.True(t, requestGranted, "Expected permission request to be granted")
}

func TestBuildToolCallTitle(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		toolName      string
		inputJson     string
		workspacePath string
		expected      string
	}{
		{
			name:      "no input args",
			toolName:  "view",
			inputJson: `{}`,
			expected:  "view",
		},
		{
			name:      "invalid json",
			toolName:  "view",
			inputJson: `{invalid`,
			expected:  "view",
		},
		{
			name:          "absolute path under workspace",
			toolName:      "view",
			inputJson:     `{"AbsolutePath": "f:/hackafterdark/phosphor/docs/ACP.md"}`,
			workspacePath: "f:/hackafterdark/phosphor",
			expected:      "View file docs/ACP.md",
		},
		{
			name:          "absolute path outside workspace",
			toolName:      "view",
			inputJson:     `{"AbsolutePath": "c:/windows/system32/cmd.exe"}`,
			workspacePath: "f:/hackafterdark/phosphor",
			expected:      "View file cmd.exe",
		},
		{
			name:          "target file parameter",
			toolName:      "write",
			inputJson:     `{"TargetFile": "f:/hackafterdark/phosphor/main.go"}`,
			workspacePath: "f:/hackafterdark/phosphor",
			expected:      "Write file main.go",
		},
		{
			name:      "command line short",
			toolName:  "bash",
			inputJson: `{"CommandLine": "go test ./..."}`,
			expected:  "Execute command: go test ./...",
		},
		{
			name:      "command line long",
			toolName:  "bash",
			inputJson: `{"CommandLine": "go test -v -race -covermode=atomic ./internal/platform/acp/..."}`,
			expected:  "Execute command: go test -v -race -covermode...",
		},
		{
			name:          "file_path parameter relative",
			toolName:      "view",
			inputJson:     `{"file_path": "internal/platform/acp/service.go"}`,
			workspacePath: "f:/hackafterdark/phosphor",
			expected:      "View file internal/platform/acp/service.go",
		},
		{
			name:      "ls parameter path",
			toolName:  "ls",
			inputJson: `{"path": "internal/platform/acp"}`,
			expected:  "List directory internal/platform/acp",
		},
		{
			name:      "ls empty path",
			toolName:  "ls",
			inputJson: `{}`,
			expected:  "List directory",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			actual := buildToolCallTitle(tc.toolName, tc.inputJson, tc.workspacePath)
			require.Equal(t, tc.expected, actual)
		})
	}
}
