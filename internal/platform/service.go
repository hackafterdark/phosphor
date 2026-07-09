package platform

import (
	"context"
)

// Service is the base interface for all Phosphor services.
// Every service (TUI, HTTP API, ACP, mermaid renderer, etc.) implements this.
type Service interface {
	// Name returns the unique identifier (e.g., "tui", "http-api", "acp").
	Name() string

	// Start begins serving.
	// In Option A, it starts the service asynchronously (or returns
	// immediately after listener setup) and does not block the caller.
	Start(ctx context.Context) error

	// Stop gracefully shuts down the service.
	Stop(ctx context.Context) error

	// Describe returns human-readable info about the service.
	Describe() string
}

// AgentService is a Service that connects to the Phosphor agent core.
// Implement this for transports that need to send prompts and receive
// responses.
type AgentService interface {
	Service

	// Connect establishes the transport (opens server, connects to platform).
	// Called after Start but before the service begins processing messages.
	Connect(ctx context.Context) error

	// SetPromptHandler sets the callback for when the agent core has a
	// response ready for this service. The service should render/format
	// and deliver it.
	SetPromptHandler(handler PromptHandler)

	// SendPrompt sends a prompt to the agent core. Called when a user sends
	// a message through this transport.
	SendPrompt(ctx context.Context, req PromptRequest) error
}

// PromptHandler is called by the agent core to deliver responses to a
// service.
type PromptHandler func(ctx context.Context, event PromptEvent) error

// PromptEvent represents an event from the agent core to a service.
type PromptEvent struct {
	Type      EventType         `json:"type"`
	SessionID string            `json:"session_id"`
	Text      string            `json:"text,omitempty"`
	ToolName  string            `json:"tool_name,omitempty"`
	Metadata  map[string]string `json:"metadata,omitempty"`
}

// EventType represents the type of a prompt event.
type EventType string

const (
	EventTurnStart     EventType = "turn_start"
	EventMessageDelta  EventType = "message_delta"
	EventToolCallStart EventType = "tool_call_start"
	EventToolCallEnd   EventType = "tool_call_end"
	EventTurnComplete  EventType = "turn_complete"
	EventSessionStart  EventType = "session_start"
	EventSessionEnd    EventType = "session_end"
)

// PromptRequest represents a prompt from a service to the agent core.
type PromptRequest struct {
	SessionID string     `json:"session_id"`
	Text      string     `json:"text"`
	Images    []Image    `json:"images,omitempty"`
	Mode      PromptMode `json:"mode"`
	Recipient Recipient  `json:"recipient"`
}

// PromptMode controls how the prompt is handled.
type PromptMode string

const (
	PromptModeNormal   PromptMode = "normal"
	PromptModeSteer    PromptMode = "steer"     // Inject mid-run.
	PromptModeFollowUp PromptMode = "follow_up" // Queue after current turn.
)

// Recipient identifies where a message should be delivered.
type Recipient struct {
	Service  string `json:"service"`             // E.g., "tui", "http_api", "discord".
	ChatID   string `json:"chat_id"`             // Channel/user identifier within the service.
	ThreadID string `json:"thread_id,omitempty"` // Optional thread/group.
	UserID   string `json:"user_id,omitempty"`   // Optional user identifier.
}

// Image represents an image attachment.
type Image struct {
	Data     []byte `json:"data"`
	MIMEType string `json:"mime_type"`
	Name     string `json:"name"`
}
