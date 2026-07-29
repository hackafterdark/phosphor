// Package acp implements the Agent Client Protocol (ACP) v1 for Phosphor.
//
// ACP is a JSON-RPC 2.0 protocol for communication between code editors/IDEs
// and coding agents. It was created by JetBrains and Zed Industries to
// standardize agent-editor interoperability. Local agents communicate via
// JSON-RPC over stdio (newline-delimited JSON).
//
// This package implements the agent side (server) of the protocol. The IDE
// acts as the client, launching phosphor acp as a subprocess and exchanging
// messages over its stdin/stdout.
package acp

import (
	"encoding/json"
	"fmt"
)

// ----- JSON-RPC 2.0 envelope types -----

// jsonrpcRequest is a JSON-RPC 2.0 method request.
type jsonrpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// jsonrpcResponse is a JSON-RPC 2.0 response.
type jsonrpcResponse struct {
	JSONRPC string        `json:"jsonrpc"`
	ID      any           `json:"id"`
	Result  any           `json:"result,omitempty"`
	Error   *jsonrpcError `json:"error,omitempty"`
}

// jsonrpcError is a JSON-RPC 2.0 error object.
type jsonrpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// jsonrpcNotification is a JSON-RPC 2.0 notification (no id, no response).
type jsonrpcNotification struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// jsonrpcMessage is a generic JSON-RPC 2.0 message envelope for requests, responses, and notifications.
type jsonrpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *jsonrpcError   `json:"error,omitempty"`
}

// Standard JSON-RPC error codes.
const (
	jsonrpcParseError     = -32700
	jsonrpcInvalidReq     = -32600
	jsonrpcMethodNotFound = -32601
	jsonrpcInvalidParams  = -32602
	jsonrpcInternalErr    = -32603
	jsonrpcServerErr      = -32000
)

// ----- ACP v1: initialize -----

// initializeRequest is sent by the client (IDE) as the first message.
type initializeRequest struct {
	ProtocolVersion    int                `json:"protocolVersion"`
	ClientInfo         clientInfo         `json:"clientInfo"`
	ClientCapabilities clientCapabilities `json:"clientCapabilities"`
}

// clientInfo identifies the connecting IDE.
type clientInfo struct {
	Name    string `json:"name"`
	Title   string `json:"title"`
	Version string `json:"version"`
}

// clientCapabilities declares what the client supports.
type clientCapabilities struct {
	FS       fsCapabilities           `json:"fs,omitempty"`
	Terminal bool                     `json:"terminal,omitempty"`
	Session  sessionClientCaps        `json:"sessionCapabilities,omitempty"`
	Prompt   promptClientCapabilities `json:"promptCapabilities,omitempty"`
	MCP      mcpClientCapabilities    `json:"mcpCapabilities,omitempty"`
}

// fsCapabilities declares filesystem operations the client can perform.
type fsCapabilities struct {
	ReadTextFile  bool `json:"readTextFile,omitempty"`
	WriteTextFile bool `json:"writeTextFile,omitempty"`
}

// sessionClientCapabilities declares session lifecycle support.
type sessionClientCaps struct {
	Close  any `json:"close,omitempty"`
	Delete any `json:"delete,omitempty"`
	Resume any `json:"resume,omitempty"`
}

// promptClientCapabilities declares prompt input support.
type promptClientCapabilities struct {
	Image           bool `json:"image,omitempty"`
	Audio           bool `json:"audio,omitempty"`
	EmbeddedContext bool `json:"embeddedContext,omitempty"`
}

// mcpClientCapabilities declares MCP transport support.
type mcpClientCapabilities struct {
	Stdio bool `json:"stdio,omitempty"`
	HTTP  bool `json:"http,omitempty"`
	SSE   bool `json:"sse,omitempty"`
}

// initializeResponse is the agent's reply to initialize.
type initializeResponse struct {
	ProtocolVersion   int               `json:"protocolVersion"`
	AgentInfo         agentInfo         `json:"agentInfo"`
	AgentCapabilities agentCapabilities `json:"agentCapabilities"`
	AuthMethods       []authMethod      `json:"authMethods"`
}

// agentInfo identifies this agent.
type agentInfo struct {
	Name    string `json:"name"`
	Title   string `json:"title"`
	Version string `json:"version"`
}

// agentCapabilities declares what the agent supports.
type agentCapabilities struct {
	Prompt  promptAgentCapabilities  `json:"promptCapabilities,omitempty"`
	Session sessionAgentCapabilities `json:"sessionCapabilities,omitempty"`
	MCP     mcpAgentCapabilities     `json:"mcpCapabilities,omitempty"`
	Auth    any                      `json:"auth,omitempty"`
}

// promptAgentCapabilities declares prompt output support.
type promptAgentCapabilities struct {
	Image           bool `json:"image,omitempty"`
	Audio           bool `json:"audio,omitempty"`
	EmbeddedContext bool `json:"embeddedContext,omitempty"`
}

// sessionAgentCapabilities declares session lifecycle support.
type sessionAgentCapabilities struct {
	LoadSession bool `json:"loadSession,omitempty"`
	Close       any  `json:"close,omitempty"`
	Delete      any  `json:"delete,omitempty"`
	Resume      any  `json:"resume,omitempty"`
	SetMode     any  `json:"setMode,omitempty"`
}

// mcpAgentCapabilities declares MCP server connections the agent can make.
type mcpAgentCapabilities struct {
	Stdio bool `json:"stdio,omitempty"`
	HTTP  bool `json:"http,omitempty"`
	SSE   bool `json:"sse,omitempty"`
}

// authMethod describes an authentication method the agent supports.
type authMethod struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Options any    `json:"options,omitempty"`
}

// ----- ACP v1: session methods -----

// sessionNewRequest creates a new conversation session.
type sessionNewRequest struct {
	Cwd                   string            `json:"cwd"`
	MCPServers            []mcpServerConfig `json:"mcpServers,omitempty"`
	AdditionalDirectories []string          `json:"additionalDirectories,omitempty"`
	SessionID             string            `json:"sessionId,omitempty"`
	Mode                  string            `json:"mode,omitempty"`
}

// mcpServerConfig describes an MCP server to connect to.
type mcpServerConfig struct {
	Name  string          `json:"name"`
	Stdio *mcpStdioServer `json:"stdio,omitempty"`
	HTTP  *mcpHTTPServer  `json:"http,omitempty"`
	SSE   *mcpSSEServer   `json:"sse,omitempty"`
}

// mcpStdioServer runs an MCP server as a subprocess.
type mcpStdioServer struct {
	Command string            `json:"command"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
}

// mcpHTTPServer connects to an MCP server over HTTP.
type mcpHTTPServer struct {
	URL string `json:"url"`
}

// mcpSSEServer connects to an MCP server over SSE.
type mcpSSEServer struct {
	URL string `json:"url"`
}

// sessionNewResponse is returned by session/new with the new session ID.
type sessionNewResponse struct {
	SessionID string `json:"sessionId"`
}

// sessionLoadRequest resumes an existing session, replaying history.
type sessionLoadRequest struct {
	SessionID string `json:"sessionId"`
}

// sessionPromptRequest sends a user message to the agent.
type sessionPromptRequest struct {
	SessionID string         `json:"sessionId"`
	Content   []contentBlock `json:"content,omitempty"`
	Prompt    []contentBlock `json:"prompt,omitempty"` // Zed uses "prompt" instead of "content"
}

// contentBlock is a piece of content in a prompt (text, image, audio, etc.).
type contentBlock struct {
	Type         string             `json:"type"`
	Text         string             `json:"text,omitempty"`
	Data         string             `json:"data,omitempty"`
	MIMEType     string             `json:"mimeType,omitempty"`
	Name         string             `json:"name,omitempty"`
	URL          string             `json:"url,omitempty"`
	Audio        string             `json:"audio,omitempty"`
	Resource     *resourceBlock     `json:"resource,omitempty"`
	ResourceLink *resourceLinkBlock `json:"resource_link,omitempty"`
}

// resourceBlock is an embedded resource content block.
type resourceBlock struct {
	URI      string `json:"uri"`
	MIMEType string `json:"mimeType"`
	Text     string `json:"text,omitempty"`
	Blob     string `json:"blob,omitempty"`
	Name     string `json:"name,omitempty"`
}

// resourceLinkBlock is a reference to an accessible resource.
type resourceLinkBlock struct {
	URI      string `json:"uri"`
	MIMEType string `json:"mimeType"`
	Name     string `json:"name,omitempty"`
}

// sessionPromptResponse is the agent's reply to session/prompt.
type sessionPromptResponse struct {
	StopReason stopReason `json:"stopReason"`
	Meta       any        `json:"_meta,omitempty"`
}

// stopReason indicates why the agent stopped processing.
type stopReason string

// Stop reason type values.
const (
	stopReasonEndTurn         stopReason = "end_turn"
	stopReasonMaxTokens       stopReason = "max_tokens"
	stopReasonMaxTurnRequests stopReason = "max_turn_requests"
	stopReasonRefusal         stopReason = "refusal"
	stopReasonCancelled       stopReason = "cancelled"
)

// sessionCancelRequest cancels an ongoing turn (notification, no response).
type sessionCancelRequest struct {
	SessionID string `json:"sessionId"`
}

// sessionCloseRequest closes a session and frees resources.
type sessionCloseRequest struct {
	SessionID string `json:"sessionId"`
}

// sessionDeleteRequest removes a session from history.
type sessionDeleteRequest struct {
	SessionID string `json:"sessionId"`
}

// sessionResumeRequest reconnects without replaying history.
type sessionResumeRequest struct {
	SessionID string `json:"sessionId"`
}

// sessionSetModeRequest switches the agent's operating mode.
type sessionSetModeRequest struct {
	SessionID string `json:"sessionId"`
	Mode      string `json:"mode"`
}

// ----- ACP v1: session/update notification -----

// SessionNotification is the envelope for session/update notifications.
type SessionNotification struct {
	SessionID string        `json:"sessionId"`
	Update    SessionUpdate `json:"update"`
}

// SessionUpdate is a discriminated union of update sub-types.
//
// It uses custom JSON marshaling to inject the "sessionUpdate" discriminator
// required by the ACP v1 spec. For Zed compatibility, message chunks also
// include a flat "type" discriminator and "text" string alongside the
// spec-compliant "content" ContentBlock.
type SessionUpdate struct {
	UserMessageChunk  *SessionUpdateUserMessageChunk
	AgentMessageChunk *SessionUpdateAgentMessageChunk
	AgentThoughtChunk *SessionUpdateAgentThoughtChunk
	ToolCall          *SessionUpdateToolCall
	ToolCallUpdate    *SessionToolCallUpdate
	Plan              *SessionUpdatePlan
	Usage             *SessionUsageUpdate
	ModeChange        *modeChange
}

// MarshalJSON implements custom JSON marshaling for the SessionUpdate union.
// It serializes the active variant and injects the "sessionUpdate" discriminator.
func (u SessionUpdate) MarshalJSON() ([]byte, error) {
	var m map[string]any

	switch {
	case u.UserMessageChunk != nil:
		b, err := json.Marshal(*u.UserMessageChunk)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(b, &m); err != nil {
			return nil, err
		}
		m["sessionUpdate"] = "user_message_chunk"

	case u.AgentMessageChunk != nil:
		b, err := json.Marshal(*u.AgentMessageChunk)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(b, &m); err != nil {
			return nil, err
		}
		m["sessionUpdate"] = "agent_message_chunk"

	case u.AgentThoughtChunk != nil:
		b, err := json.Marshal(*u.AgentThoughtChunk)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(b, &m); err != nil {
			return nil, err
		}
		m["sessionUpdate"] = "agent_thought_chunk"

	case u.ToolCall != nil:
		b, err := json.Marshal(*u.ToolCall)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(b, &m); err != nil {
			return nil, err
		}
		m["sessionUpdate"] = "tool_call"

	case u.ToolCallUpdate != nil:
		b, err := json.Marshal(*u.ToolCallUpdate)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(b, &m); err != nil {
			return nil, err
		}
		m["sessionUpdate"] = "tool_call_update"

	case u.Plan != nil:
		b, err := json.Marshal(*u.Plan)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(b, &m); err != nil {
			return nil, err
		}
		m["sessionUpdate"] = "plan"

	case u.Usage != nil:
		b, err := json.Marshal(*u.Usage)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(b, &m); err != nil {
			return nil, err
		}
		m["sessionUpdate"] = "usage_update"

	case u.ModeChange != nil:
		b, err := json.Marshal(*u.ModeChange)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(b, &m); err != nil {
			return nil, err
		}
		m["sessionUpdate"] = "mode_change"

	default:
		return nil, fmt.Errorf("sessionUpdate: no variant set")
	}

	return json.Marshal(m)
}

// ----- session/update sub-types -----

// SessionUpdateUserMessageChunk streams a user message chunk.
type SessionUpdateUserMessageChunk struct {
	MessageID *string      `json:"messageId,omitempty"`
	Content   contentBlock `json:"content"`
	// Zed compatibility: flat text string alongside spec content block.
	Text string `json:"text,omitempty"`
}

// SessionUpdateAgentMessageChunk streams an agent response chunk.
type SessionUpdateAgentMessageChunk struct {
	MessageID *string      `json:"messageId,omitempty"`
	Content   contentBlock `json:"content"`
	// Zed compatibility: flat text string alongside spec content block.
	Text string `json:"text,omitempty"`
}

// SessionUpdateAgentThoughtChunk streams agent reasoning.
// Uses the spec format: content block containing thinking text.
type SessionUpdateAgentThoughtChunk struct {
	MessageID *string      `json:"messageId,omitempty"`
	Content   contentBlock `json:"content"`
}

// SessionUpdateToolCall reports a new tool call.
// Fields are flat at the top level per the ACP v1 spec.
type SessionUpdateToolCall struct {
	ToolCallID string          `json:"toolCallId"`
	Title      string          `json:"title"`
	Kind       toolCallKind    `json:"kind"`
	Status     string          `json:"status"`
	Content    []contentBlock  `json:"content,omitempty"`
	Locations  []location      `json:"locations,omitempty"`
	RawInput   json.RawMessage `json:"rawInput,omitempty"`
}

// SessionToolCallUpdate reports a tool call status/content update.
// Fields are flat at the top level per the ACP v1 spec.
type SessionToolCallUpdate struct {
	ToolCallID string          `json:"toolCallId"`
	Status     string          `json:"status"`
	Content    []contentBlock  `json:"content,omitempty"`
	Error      string          `json:"error,omitempty"`
	Title      string          `json:"title,omitempty"`
	Kind       toolCallKind    `json:"kind,omitempty"`
	Locations  []location      `json:"locations,omitempty"`
	RawInput   json.RawMessage `json:"rawInput,omitempty"`
}

// SessionUpdatePlan reports the agent's execution plan.
type SessionUpdatePlan struct {
	Entries []planEntry `json:"entries"`
}

// SessionUsageUpdate reports token usage and cost.
type SessionUsageUpdate struct {
	Used uint64     `json:"used"`
	Size uint64     `json:"size"`
	Cost *usageCost `json:"cost,omitempty"`
}

// usageCost is the cost portion of a usage update.
type usageCost struct {
	Amount   float64 `json:"amount"`
	Currency string  `json:"currency"`
}

// modeChange reports an agent-initiated mode switch.
type modeChange struct {
	Mode string `json:"mode"`
}

// ----- Shared types -----

// toolCallKind is the category of a tool call.
type toolCallKind string

// Tool call kind values.
const (
	toolCallKindRead    toolCallKind = "read"
	toolCallKindEdit    toolCallKind = "edit"
	toolCallKindDelete  toolCallKind = "delete"
	toolCallKindMove    toolCallKind = "move"
	toolCallKindSearch  toolCallKind = "search"
	toolCallKindExecute toolCallKind = "execute"
	toolCallKindThink   toolCallKind = "think"
	toolCallKindFetch   toolCallKind = "fetch"
	toolCallKindOther   toolCallKind = "other"
)

// location references a file and position.
type location struct {
	Path      string `json:"path"`
	Line      int    `json:"line,omitempty"`
	EndLine   int    `json:"endLine,omitempty"`
	Column    int    `json:"column,omitempty"`
	EndColumn int    `json:"endColumn,omitempty"`
}

// planEntry is one item in the agent's plan.
type planEntry struct {
	Content  string       `json:"content"`
	Priority planPriority `json:"priority,omitempty"`
	Status   planStatus   `json:"status,omitempty"`
}

// planPriority is the priority of a plan entry.
type planPriority string

const (
	planPriorityHigh   planPriority = "high"
	planPriorityMedium planPriority = "medium"
	planPriorityLow    planPriority = "low"
)

// planStatus is the status of a plan entry.
type planStatus string

const (
	planStatusPending    planStatus = "pending"
	planStatusInProgress planStatus = "in_progress"
	planStatusCompleted  planStatus = "completed"
)

// ----- ACP v1: session/request_permission -----

// PermissionToolCall contains details about the tool call requesting authorization.
type PermissionToolCall struct {
	ToolCallID string          `json:"toolCallId"`
	ToolName   string          `json:"toolName"`
	Title      string          `json:"title"`
	Kind       string          `json:"kind,omitempty"`
	Status     string          `json:"status,omitempty"`
	RawInput   json.RawMessage `json:"rawInput,omitempty"`
}

// requestPermissionRequest asks the IDE user to authorize a tool call.
type requestPermissionRequest struct {
	SessionID string             `json:"sessionId"`
	ToolCall  PermissionToolCall `json:"toolCall"`
	Options   []permissionOption `json:"options"`
}

// permissionOption is a choice presented to the user for a permission request.
type permissionOption struct {
	OptionID string `json:"optionId"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
}

// permissionOptionKind is the kind of a permission option.
type permissionOptionKind string

const (
	permAllowOnce    permissionOptionKind = "allow_once"
	permAllowAlways  permissionOptionKind = "allow_always"
	permRejectOnce   permissionOptionKind = "reject_once"
	permRejectAlways permissionOptionKind = "reject_always"
)

// requestPermissionResponse is the response to a permission request.
type requestPermissionResponse struct {
	Outcome requestPermissionOutcome `json:"outcome"`
}

// requestPermissionOutcome is the result of a permission request.
type requestPermissionOutcome struct {
	Outcome  string `json:"outcome"`
	OptionID string `json:"optionId,omitempty"`
}

func (o *requestPermissionOutcome) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		o.Outcome = "selected"
		o.OptionID = s
		return nil
	}
	type rawOutcome struct {
		Outcome       string `json:"outcome"`
		OptionID      string `json:"optionId"`
		OptionIdSnake string `json:"option_id"`
		Kind          string `json:"kind"`
	}
	var raw rawOutcome
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	o.Outcome = raw.Outcome
	switch {
	case raw.OptionID != "":
		o.OptionID = raw.OptionID
	case raw.OptionIdSnake != "":
		o.OptionID = raw.OptionIdSnake
	case raw.Kind != "":
		o.OptionID = raw.Kind
	case raw.Outcome != "" && raw.Outcome != "selected" && raw.Outcome != "cancelled":
		o.OptionID = raw.Outcome
	}
	return nil
}

// logoutRequest ends an authenticated session.
type logoutRequest struct{}
