package client

import (
	"context"
	"time"
)

type EventType string

const (
	EventSessionStart  EventType = "session_start"
	EventSessionEnd    EventType = "session_end"
	EventMessageDelta  EventType = "message_delta"
	EventToolCall      EventType = "tool_call"
	EventToolResult    EventType = "tool_result"
	EventAgentComplete EventType = "agent_complete"
)

type Event struct {
	Type      EventType
	SessionID string
	Timestamp time.Time

	// Fields populated depending on Type
	MessageID  string
	Text       string
	ToolName   string
	ToolCallID string
	ToolInput  string
	ToolResult string
	Error      string
}

// Subscribe is a package-level convenience function to subscribe to session events.
func Subscribe(h *SessionHandle, ctx context.Context, eventType EventType, handler func(event Event)) (func(), error) {
	return h.Subscribe(ctx, eventType, handler)
}
