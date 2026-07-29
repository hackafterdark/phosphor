package acp

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/google/uuid"
	"github.com/hackafterdark/phosphor/internal/backend"
	"github.com/hackafterdark/phosphor/internal/platform"
	"github.com/hackafterdark/phosphor/internal/proto"
	notify "github.com/hackafterdark/phosphor/pkg/agent/notify"
	"github.com/hackafterdark/phosphor/pkg/config"
	"github.com/hackafterdark/phosphor/pkg/message"
	"github.com/hackafterdark/phosphor/pkg/permission"
	"github.com/hackafterdark/phosphor/pkg/pubsub"
)

// Service implements the ACP v1 agent server over stdio JSON-RPC.
//
// It registers with the platform registry as a Service and handles the full
// ACP lifecycle: initialize → session setup → prompt turns with streaming
// updates → stop.
type Service struct {
	backend   *backend.Backend
	cfgStore  *config.ConfigStore
	logger    *slog.Logger
	writeMu   sync.Mutex // guards writes to stdout via Encoder
	Encoder   *json.Encoder
	providers []acpProvider

	mu       sync.Mutex
	sessions map[string]*sessionState

	seenMu             sync.Mutex
	seenText           map[string]int    // keyed by messageID, value = last emitted text length
	fullText           map[string]string // keyed by messageID, value = accumulated full text
	seenThinking       map[string]int    // keyed by messageID, value = last emitted thinking length
	seenToolCallStatus map[string]string // keyed by toolCallID, value = last emitted status
	seenUserMsg        map[string]bool   // keyed by messageID, value = true if user msg already sent
	finalMu            sync.Mutex
	finalEmitted       map[string]bool   // tracks which messageIDs already got the final consolidated chunk
	lastAssistantMsg   map[string]string // maps sessionID -> last assistant message ID seen for that session

	pendingMu sync.Mutex
	pending   map[string]*pendingPrompt // keyed by JSON-RPC request ID

	clientRequestsMu sync.Mutex
	clientRequests   map[string]chan *jsonrpcMessage // keyed by request ID
}

type acpProvider struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Model string `json:"model"`
}

type sessionState struct {
	sessionID   string
	workspaceID string
	clientID    string
	eventCh     <-chan pubsub.Event[tea.Msg]
	cancel      context.CancelFunc
}

type pendingPrompt struct {
	respCh    chan sessionPromptResponse
	runID     string
	sessionID string
}

// NewService creates a new ACP service.
func NewService(backend *backend.Backend, cfgStore *config.ConfigStore, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{
		backend:            backend,
		cfgStore:           cfgStore,
		logger:             logger,
		Encoder:            json.NewEncoder(os.Stdout),
		sessions:           make(map[string]*sessionState),
		pending:            make(map[string]*pendingPrompt),
		seenText:           make(map[string]int),
		fullText:           make(map[string]string),
		seenThinking:       make(map[string]int),
		seenToolCallStatus: make(map[string]string),
		seenUserMsg:        make(map[string]bool),
		finalEmitted:       make(map[string]bool),
		lastAssistantMsg:   make(map[string]string),
		clientRequests:     make(map[string]chan *jsonrpcMessage),
	}
}

// Name returns "acp".
func (s *Service) Name() string { return "acp" }

// Describe returns a description of the ACP service.
func (s *Service) Describe() string {
	return "Agent Client Protocol v1 server (stdio JSON-RPC)"
}

// Start begins the ACP JSON-RPC server on stdio. It blocks until the context
// is cancelled or stdin closes.
func (s *Service) Start(ctx context.Context) error {
	s.providers = s.resolveProviders()
	s.logger.Info("ACP service loop started, listening on stdio", "providersCount", len(s.providers))

	mainCtx, mainCancel := context.WithCancel(ctx)
	defer mainCancel()

	// Main dispatch loop: read JSON-RPC from stdin, write responses/notifications to stdout.
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var msg jsonrpcMessage
		if err := json.Unmarshal(line, &msg); err != nil {
			s.writeError(nil, jsonrpcParseError, "parse error: "+err.Error())
			continue
		}

		if msg.Method != "" {
			req := jsonrpcRequest{
				JSONRPC: msg.JSONRPC,
				ID:      msg.ID,
				Method:  msg.Method,
				Params:  msg.Params,
			}
			go s.handleRequest(mainCtx, &req)
		} else if msg.ID != nil {
			s.handleResponseMessage(&msg)
		} else {
			s.writeError(nil, jsonrpcInvalidReq, "invalid message: missing method or id")
		}
	}

	if err := scanner.Err(); err != nil && err != io.EOF {
		s.logger.Error("ACP stdio scanner error", "error", err)
		return fmt.Errorf("stdio read error: %w", err)
	}
	s.logger.Info("ACP stdio scanner reached EOF, stopping service loop")
	return nil
}

// Stop gracefully shuts down all sessions.
func (s *Service) Stop(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for id, ss := range s.sessions {
		s.logger.Info("Closing ACP session", "sessionID", id)
		ss.cancel()
		delete(s.sessions, id)
	}
	return nil
}

// resolveProviders reads configured providers and converts them to ACP format.
func (s *Service) resolveProviders() []acpProvider {
	if s.cfgStore == nil {
		return nil
	}
	cfg := s.cfgStore.Config()
	providers, err := config.Providers(cfg)
	if err != nil || len(providers) == 0 {
		return nil
	}

	out := make([]acpProvider, 0, len(providers))
	for _, p := range providers {
		out = append(out, acpProvider{
			ID:   string(p.ID),
			Name: p.Name,
		})
	}
	return out
}

// ----- Request dispatch -----

func (s *Service) handleRequest(ctx context.Context, req *jsonrpcRequest) {
	s.logger.Info("Received ACP request", "method", req.Method, "id", req.ID)
	switch req.Method {
	case "initialize":
		s.handleInitialize(ctx, req)
	case "session/new":
		s.handleSessionNew(ctx, req)
	case "session/load":
		s.handleSessionLoad(ctx, req)
	case "session/prompt":
		s.handleSessionPrompt(ctx, req)
	case "session/cancel":
		s.handleSessionCancel(req)
	case "session/close":
		s.handleSessionClose(req)
	case "session/delete":
		s.handleSessionDelete(req)
	case "session/resume":
		s.handleSessionResume(ctx, req)
	case "session/set_mode":
		s.handleSessionSetMode(ctx, req)
	case "logout":
		s.handleLogout(req)
	default:
		s.logger.Warn("ACP method not found", "method", req.Method, "id", req.ID)
		s.writeError(req.ID, jsonrpcMethodNotFound, "method not found: "+req.Method)
	}
}

func (s *Service) handleResponseMessage(msg *jsonrpcMessage) {
	var idStr string
	if msg.ID != nil {
		switch v := msg.ID.(type) {
		case string:
			idStr = v
		case float64:
			idStr = fmt.Sprintf("%.0f", v)
		default:
			idStr = fmt.Sprintf("%v", v)
		}
	}

	s.logger.Debug("Received ACP response message from client", "id", idStr)

	s.clientRequestsMu.Lock()
	ch, ok := s.clientRequests[idStr]
	s.clientRequestsMu.Unlock()

	if ok {
		ch <- msg
	} else {
		s.logger.Warn("Received response for unknown request ID", "id", idStr)
	}
}

// ----- Response helpers -----

func (s *Service) writeResponse(id any, result any) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	resp := jsonrpcResponse{JSONRPC: "2.0", ID: id, Result: result}
	s.logger.Debug("Writing ACP response", "id", id)
	if err := s.Encoder.Encode(resp); err != nil {
		s.logger.Warn("Failed to write JSON-RPC response", "id", id, "error", err)
	}
}

func (s *Service) writeError(id any, code int, message string) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	resp := jsonrpcResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &jsonrpcError{Code: code, Message: message},
	}
	s.logger.Error("Writing ACP error response", "id", id, "code", code, "message", message)
	if err := s.Encoder.Encode(resp); err != nil {
		s.logger.Warn("Failed to write JSON-RPC error", "id", id, "error", err)
	}
}

func (s *Service) writeNotification(method string, params any) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	p, err := json.Marshal(params)
	if err != nil {
		s.logger.Warn("Failed to marshal notification params", "method", method, "error", err)
		return
	}
	notif := jsonrpcNotification{JSONRPC: "2.0", Method: method, Params: p}
	s.logger.Debug("Writing ACP notification", "method", method)
	if err := s.Encoder.Encode(notif); err != nil {
		s.logger.Warn("Failed to write JSON-RPC notification", "method", method, "error", err)
	}
}

func (s *Service) sendRequest(ctx context.Context, method string, params any) (*jsonrpcMessage, error) {
	id := uuid.New().String()
	ch := make(chan *jsonrpcMessage, 1)

	s.clientRequestsMu.Lock()
	s.clientRequests[id] = ch
	s.clientRequestsMu.Unlock()

	defer func() {
		s.clientRequestsMu.Lock()
		delete(s.clientRequests, id)
		s.clientRequestsMu.Unlock()
	}()

	p, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request params: %w", err)
	}

	req := jsonrpcRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  p,
	}

	s.writeMu.Lock()
	err = s.Encoder.Encode(req)
	s.writeMu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case resp := <-ch:
		return resp, nil
	}
}

func (s *Service) sendSessionUpdate(sessionID string, update SessionUpdate) {
	s.writeNotification("session/update", SessionNotification{
		SessionID: sessionID,
		Update:    update,
	})
}

func (s *Service) registerPending(runID, sessionID string) (chan sessionPromptResponse, func()) {
	s.pendingMu.Lock()
	defer s.pendingMu.Unlock()

	ch := make(chan sessionPromptResponse, 1)
	s.pending[runID] = &pendingPrompt{
		respCh:    ch,
		runID:     runID,
		sessionID: sessionID,
	}

	cleanup := func() {
		s.pendingMu.Lock()
		defer s.pendingMu.Unlock()
		delete(s.pending, runID)
	}

	return ch, cleanup
}

func (s *Service) resolvePendingRun(runID, sessionID string, resp sessionPromptResponse) {
	s.pendingMu.Lock()
	defer s.pendingMu.Unlock()

	if runID != "" {
		if p, ok := s.pending[runID]; ok {
			delete(s.pending, runID)
			select {
			case p.respCh <- resp:
			default:
			}
			return
		}
	}

	for id, p := range s.pending {
		if p.sessionID == sessionID {
			delete(s.pending, id)
			select {
			case p.respCh <- resp:
			default:
			}
			return
		}
	}
}

// ----- Handler implementations -----

func (s *Service) handleInitialize(_ context.Context, req *jsonrpcRequest) {
	var params initializeRequest
	if err := json.Unmarshal(req.Params, &params); err != nil {
		s.writeError(req.ID, jsonrpcInvalidParams, "invalid initialize params")
		return
	}

	resp := initializeResponse{
		ProtocolVersion: 1,
		AgentInfo: agentInfo{
			Name:    "phosphor",
			Title:   "Phosphor",
			Version: "0.1.0",
		},
		AgentCapabilities: agentCapabilities{
			Prompt: promptAgentCapabilities{
				Image:           true,
				EmbeddedContext: true,
			},
			Session: sessionAgentCapabilities{
				LoadSession: true,
				Close:       struct{}{},
				Delete:      struct{}{},
				Resume:      struct{}{},
				SetMode:     struct{}{},
			},
			MCP: mcpAgentCapabilities{
				Stdio: true,
			},
		},
		AuthMethods: []authMethod{},
	}

	s.writeResponse(req.ID, resp)
}

func (s *Service) handleSessionNew(ctx context.Context, req *jsonrpcRequest) {
	var params sessionNewRequest
	if err := json.Unmarshal(req.Params, &params); err != nil {
		s.writeError(req.ID, jsonrpcInvalidParams, "invalid session/new params")
		return
	}

	cwd := params.Cwd
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			s.writeError(req.ID, jsonrpcInternalErr, "failed to get working directory")
			return
		}
	}

	// Create workspace via backend.
	clientID := uuid.New().String()
	wsProto := proto.Workspace{
		Path:     cwd,
		ClientID: clientID,
	}

	ws, _, err := s.backend.CreateWorkspace(wsProto)
	if err != nil {
		s.writeError(req.ID, jsonrpcInternalErr, "failed to create workspace: "+err.Error())
		return
	}

	// Create session within the workspace.
	se, err := s.backend.CreateSession(ctx, ws.ID, "ACP Session")
	if err != nil {
		s.writeError(req.ID, jsonrpcInternalErr, "failed to create session: "+err.Error())
		return
	}

	// Mark session service origin as "acp".
	if err := s.backend.UpdateSessionStateless(ctx, ws.ID, se.ID, false, "acp"); err != nil {
		s.logger.Warn("Failed to set ACP session service origin", "error", err)
	}

	// Subscribe to events for this session's workspace.
	eventCh, err := s.backend.SubscribeEvents(ctx, ws.ID)
	if err != nil {
		s.writeError(req.ID, jsonrpcInternalErr, "failed to subscribe to events: "+err.Error())
		return
	}

	sessionCtx, cancel := context.WithCancel(ctx)

	s.mu.Lock()
	s.sessions[se.ID] = &sessionState{
		sessionID:   se.ID,
		workspaceID: ws.ID,
		clientID:    clientID,
		eventCh:     eventCh,
		cancel:      cancel,
	}
	s.mu.Unlock()

	// Attach stream client to prevent workspace teardown from hold expiry.
	if err := s.backend.AttachClient(ws.ID, clientID); err != nil {
		s.logger.Warn("Failed to attach ACP stream client", "error", err)
	}

	go s.fanOutEvents(sessionCtx, se.ID, eventCh)

	s.writeResponse(req.ID, sessionNewResponse{SessionID: se.ID})
}

func (s *Service) handleSessionLoad(ctx context.Context, req *jsonrpcRequest) {
	var params sessionLoadRequest
	if err := json.Unmarshal(req.Params, &params); err != nil {
		s.writeError(req.ID, jsonrpcInvalidParams, "invalid session/load params")
		return
	}

	if params.SessionID == "" {
		s.writeError(req.ID, jsonrpcInvalidParams, "sessionId is required")
		return
	}

	// Find the backend workspace that contains this session.
	ws, err := s.findBackendWorkspaceForSession(params.SessionID)
	if err != nil {
		s.writeError(req.ID, jsonrpcInternalErr, "session not found: "+params.SessionID)
		return
	}

	// Get session messages for replay.
	msgs, err := s.backend.ListSessionMessages(ctx, ws.ID, params.SessionID)
	if err != nil {
		s.writeError(req.ID, jsonrpcInternalErr, "failed to load session messages: "+err.Error())
		return
	}

	// Replay message history as user_message_chunk and agent_message_chunk.
	for _, m := range msgs {
		text := extractMessageText(m.Parts)
		switch m.Role {
		case message.User:
			s.sendSessionUpdate(params.SessionID, SessionUpdate{
				UserMessageChunk: &SessionUpdateUserMessageChunk{
					Content: contentBlock{Type: "text", Text: text},
					Text:    text,
				},
			})
		case message.Assistant:
			s.sendSessionUpdate(params.SessionID, SessionUpdate{
				AgentMessageChunk: &SessionUpdateAgentMessageChunk{
					MessageID: strPtr(m.ID),
					Content:   contentBlock{Type: "text", Text: text},
					Text:      text,
				},
			})
		}
	}

	// Subscribe to events for this session.
	eventCh, err := s.backend.SubscribeEvents(ctx, ws.ID)
	if err != nil {
		s.writeError(req.ID, jsonrpcInternalErr, "failed to subscribe to events: "+err.Error())
		return
	}

	sessionCtx, cancel := context.WithCancel(ctx)

	s.mu.Lock()
	s.sessions[params.SessionID] = &sessionState{
		sessionID:   params.SessionID,
		workspaceID: ws.ID,
		eventCh:     eventCh,
		cancel:      cancel,
	}
	s.mu.Unlock()

	go s.fanOutEvents(sessionCtx, params.SessionID, eventCh)

	s.writeResponse(req.ID, sessionNewResponse{SessionID: params.SessionID})
}

func (s *Service) handleSessionPrompt(ctx context.Context, req *jsonrpcRequest) {
	var params sessionPromptRequest
	if err := json.Unmarshal(req.Params, &params); err != nil {
		s.writeError(req.ID, jsonrpcInvalidParams, "invalid session/prompt params")
		return
	}

	if params.SessionID == "" {
		s.writeError(req.ID, jsonrpcInvalidParams, "sessionId is required")
		return
	}

	// Extract text content and attachments from the prompt.
	// Zed sends the content array as "prompt" instead of "content".
	content := params.Content
	if content == nil {
		content = params.Prompt
	}
	promptText, attachments := extractPromptAndAttachments(content)
	if promptText == "" {
		s.writeError(req.ID, jsonrpcInvalidParams, "prompt content must contain at least one text block")
		return
	}

	// Look up the session state to get workspace ID.
	s.mu.Lock()
	_, ok := s.sessions[params.SessionID]
	if !ok {
		s.mu.Unlock()
		s.writeError(req.ID, jsonrpcInvalidReq, "session not found: "+params.SessionID)
		return
	}
	workspaceID := s.sessions[params.SessionID].workspaceID
	s.mu.Unlock()

	// Generate a RunID for tracking completion.
	runID := uuid.New().String()

	respCh, unregister := s.registerPending(runID, params.SessionID)
	defer unregister()

	// Send message to backend using proto.AgentMessage.
	msg := proto.AgentMessage{
		SessionID:   params.SessionID,
		RunID:       runID,
		Prompt:      promptText,
		Attachments: attachments,
	}

	if err := s.backend.SendMessage(workspaceID, msg); err != nil {
		s.writeError(req.ID, jsonrpcInternalErr, "failed to send message: "+err.Error())
		return
	}

	select {
	case <-ctx.Done():
		s.writeError(req.ID, jsonrpcInternalErr, "context cancelled")
	case resp := <-respCh:
		s.writeResponse(req.ID, resp)
	}
}

func (s *Service) handleSessionCancel(req *jsonrpcRequest) {
	var params sessionCancelRequest
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return // Notification, ignore malformed
	}

	s.mu.Lock()
	ss, ok := s.sessions[params.SessionID]
	s.mu.Unlock()

	if !ok {
		return
	}

	// Cancel ongoing session in backend.
	if s.backend != nil {
		if err := s.backend.CancelSession(ss.workspaceID, params.SessionID); err != nil {
			s.logger.Warn("Failed to cancel session in backend", "sessionID", params.SessionID, "error", err)
		}
	}

	// Resolve any pending prompt for this session as cancelled immediately.
	s.resolvePendingRun("", params.SessionID, sessionPromptResponse{
		StopReason: stopReasonCancelled,
	})

	// Send a cancellation indicator to the IDE.
	s.sendSessionUpdate(params.SessionID, SessionUpdate{
		AgentMessageChunk: &SessionUpdateAgentMessageChunk{
			Text:    "[turn cancelled]",
			Content: contentBlock{Type: "text", Text: "[turn cancelled]"},
		},
	})
}

func (s *Service) handleSessionClose(req *jsonrpcRequest) {
	var params sessionCloseRequest
	if err := json.Unmarshal(req.Params, &params); err != nil {
		s.writeError(req.ID, jsonrpcInvalidParams, "invalid session/close params")
		return
	}

	s.mu.Lock()
	ss, ok := s.sessions[params.SessionID]
	if ok {
		clientID := ss.clientID
		workspaceID := ss.workspaceID
		ss.cancel()
		delete(s.sessions, params.SessionID)
		s.mu.Unlock()

		// Detach stream client so workspace teardown can proceed if no
		// other clients hold it.
		s.backend.DetachClient(workspaceID, clientID)
	} else {
		s.mu.Unlock()
	}

	if !ok {
		s.writeError(req.ID, jsonrpcInvalidReq, "session not found: "+params.SessionID)
		return
	}
	s.writeResponse(req.ID, struct{}{})
}

func (s *Service) handleSessionDelete(req *jsonrpcRequest) {
	var params sessionDeleteRequest
	if err := json.Unmarshal(req.Params, &params); err != nil {
		s.writeError(req.ID, jsonrpcInvalidParams, "invalid session/delete params")
		return
	}

	s.mu.Lock()
	ss, ok := s.sessions[params.SessionID]
	if !ok {
		s.mu.Unlock()
		s.writeError(req.ID, jsonrpcInvalidReq, "session not found: "+params.SessionID)
		return
	}
	workspaceID := ss.workspaceID
	clientID := ss.clientID
	s.mu.Unlock()

	// Delete the session from the backend.
	ctx := context.Background()
	if err := s.backend.DeleteSession(ctx, workspaceID, params.SessionID); err != nil {
		s.writeError(req.ID, jsonrpcInternalErr, "failed to delete session: "+err.Error())
		return
	}

	s.mu.Lock()
	ss.cancel()
	delete(s.sessions, params.SessionID)
	s.mu.Unlock()

	// Detach stream client.
	s.backend.DetachClient(workspaceID, clientID)

	s.writeResponse(req.ID, struct{}{})
}

func (s *Service) handleSessionResume(ctx context.Context, req *jsonrpcRequest) {
	var params sessionResumeRequest
	if err := json.Unmarshal(req.Params, &params); err != nil {
		s.writeError(req.ID, jsonrpcInvalidParams, "invalid session/resume params")
		return
	}

	if params.SessionID == "" {
		s.writeError(req.ID, jsonrpcInvalidParams, "sessionId is required")
		return
	}

	// Find the workspace for this session.
	ws, err := s.findBackendWorkspaceForSession(params.SessionID)
	if err != nil {
		s.writeError(req.ID, jsonrpcInternalErr, "session not found: "+params.SessionID)
		return
	}

	// Subscribe to events without replaying history.
	eventCh, err := s.backend.SubscribeEvents(ctx, ws.ID)
	if err != nil {
		s.writeError(req.ID, jsonrpcInternalErr, "failed to subscribe to events: "+err.Error())
		return
	}

	sessionCtx, cancel := context.WithCancel(ctx)

	s.mu.Lock()
	s.sessions[params.SessionID] = &sessionState{
		sessionID:   params.SessionID,
		workspaceID: ws.ID,
		eventCh:     eventCh,
		cancel:      cancel,
	}
	s.mu.Unlock()

	go s.fanOutEvents(sessionCtx, params.SessionID, eventCh)

	s.writeResponse(req.ID, sessionNewResponse{SessionID: params.SessionID})
}

func (s *Service) handleSessionSetMode(ctx context.Context, req *jsonrpcRequest) {
	var params sessionSetModeRequest
	if err := json.Unmarshal(req.Params, &params); err != nil {
		s.writeError(req.ID, jsonrpcInvalidParams, "invalid session/set_mode params")
		return
	}

	s.mu.Lock()
	_, ok := s.sessions[params.SessionID]
	s.mu.Unlock()

	if !ok {
		s.writeError(req.ID, jsonrpcInvalidReq, "session not found: "+params.SessionID)
		return
	}

	// Send mode change notification to the IDE.
	s.sendSessionUpdate(params.SessionID, SessionUpdate{
		ModeChange: &modeChange{Mode: params.Mode},
	})

	s.writeResponse(req.ID, struct{}{})
}

func (s *Service) handleLogout(req *jsonrpcRequest) {
	s.writeResponse(req.ID, struct{}{})
}

// ----- Event fan-out -----

// fanOutEvents reads events from the workspace event channel and translates
// them into ACP session/update notifications for the given session ID.
func (s *Service) fanOutEvents(ctx context.Context, sessionID string, eventCh <-chan pubsub.Event[tea.Msg]) {
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-eventCh:
			if !ok {
				return
			}
			s.translateAndSend(sessionID, ev)
		}
	}
}

// translateAndSend converts a backend event into an ACP session/update notification.
//
// Events from the broker are wrapped as tea.Msg(pubsub.Event[T]). Since
// tea.Msg is just any, ev.Payload is the upstream pubsub.Event[T] directly.
func (s *Service) translateAndSend(sessionID string, ev pubsub.Event[tea.Msg]) {
	switch p := ev.Payload.(type) {
	case pubsub.Event[message.Message]:
		s.handleMessage(sessionID, p.Payload)
	case pubsub.Event[notify.Notification]:
		s.handleNotification(sessionID, p.Payload)
	case pubsub.Event[notify.RunComplete]:
		s.handleRunComplete(sessionID, p.Payload)
	case pubsub.Event[permission.PermissionRequest]:
		s.handlePermissionRequest(sessionID, p.Payload)
	default:
		// Unknown payload type; ignore.
	}
}

// handleRunComplete handles authoritative end-of-run signals.
func (s *Service) handleRunComplete(sessionID string, rc notify.RunComplete) {
	if rc.SessionID != "" && rc.SessionID != sessionID {
		return
	}

	reason := stopReasonEndTurn
	if rc.Cancelled {
		reason = stopReasonCancelled
	}

	s.resolvePendingRun(rc.RunID, sessionID, sessionPromptResponse{
		StopReason: reason,
	})
}

// handleNotification translates agent notifications (finished, error) into ACP updates.
func (s *Service) handleNotification(sessionID string, n notify.Notification) {
	if n.SessionID != "" && n.SessionID != sessionID {
		return
	}

	switch n.Type {
	case notify.TypeAgentError, notify.TypeAgentFinished:
		s.resolvePendingRun(n.RunID, sessionID, sessionPromptResponse{
			StopReason: stopReasonEndTurn,
		})

		// Look up the last assistant message ID for this session so we
		// can find the accumulated full text keyed by that message ID
		// (not the session ID).
		s.mu.Lock()
		msgID := s.lastAssistantMsg[sessionID]
		s.mu.Unlock()
		if msgID == "" {
			return
		}

		// Emit one final consolidated message chunk with messageId so Zed
		// knows the response is complete. Streaming deltas were sent without
		// messageId; this final chunk groups them into a single visible
		// response bubble. Keyed by message ID so each turn's final chunk
		// is tracked independently.
		s.finalMu.Lock()
		if s.finalEmitted[msgID] {
			s.finalMu.Unlock()
			return
		}
		s.finalEmitted[msgID] = true
		s.finalMu.Unlock()

		// Use accumulated full text tracked during streaming.
		s.seenMu.Lock()
		text, ok := s.fullText[msgID]
		alreadyStreamed := s.seenText[msgID] > 0
		s.seenMu.Unlock()
		if !ok || text == "" {
			return
		}

		if alreadyStreamed {
			return
		}

		s.sendSessionUpdate(sessionID, SessionUpdate{
			AgentMessageChunk: &SessionUpdateAgentMessageChunk{
				MessageID: strPtr(msgID),
				Content:   contentBlock{Type: "text", Text: text},
				Text:      text,
			},
		})
	default:
	}
}

// handlePermissionRequest forwards permission requests to the client and grants/denies them.
func (s *Service) handlePermissionRequest(sessionID string, req permission.PermissionRequest) {
	if req.SessionID != "" && req.SessionID != sessionID {
		return
	}

	s.mu.Lock()
	state, ok := s.sessions[sessionID]
	s.mu.Unlock()
	if !ok {
		s.logger.Warn("Session not found for permission request", "sessionID", sessionID)
		return
	}

	workspaceID := state.workspaceID

	go func() {
		options := []permissionOption{
			{OptionID: string(permAllowOnce), Name: "Allow Once", Kind: string(permAllowOnce)},
			{OptionID: string(permAllowAlways), Name: "Allow for Session", Kind: string(permAllowAlways)},
			{OptionID: string(permRejectOnce), Name: "Deny Once", Kind: string(permRejectOnce)},
			{OptionID: string(permRejectAlways), Name: "Deny Always", Kind: string(permRejectAlways)},
		}

		var inputJsonStr string
		if req.Params != nil {
			if b, err := json.Marshal(req.Params); err == nil {
				inputJsonStr = string(b)
			}
		}

		var workspacePath string
		if ws, err := s.backend.GetWorkspace(workspaceID); err == nil {
			workspacePath = ws.Path
		}

		title := buildToolCallTitle(req.ToolName, inputJsonStr, workspacePath)

		var rawInput json.RawMessage
		if req.Params != nil {
			if b, err := json.Marshal(req.Params); err == nil {
				rawInput = json.RawMessage(b)
			}
		}

		rpcReq := requestPermissionRequest{
			SessionID: sessionID,
			ToolCall: PermissionToolCall{
				ToolCallID: req.ToolCallID,
				ToolName:   req.ToolName,
				Title:      title,
				Kind:       string(mapToolKind(req.ToolName)),
				Status:     "pending",
				RawInput:   rawInput,
			},
			Options: options,
		}

		s.logger.Info("Sending session/request_permission to client", "toolCallID", req.ToolCallID, "toolName", req.ToolName)
		permCtx, permCancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer permCancel()
		respMsg, err := s.sendRequest(permCtx, "session/request_permission", rpcReq)
		if err != nil {
			s.logger.Error("Failed to send permission request or get response", "error", err)
			_, _ = s.backend.GrantPermission(workspaceID, proto.PermissionGrant{
				Permission: proto.PermissionRequest{
					ID:          req.ID,
					SessionID:   req.SessionID,
					ToolCallID:  req.ToolCallID,
					ToolName:    req.ToolName,
					Description: req.Description,
					Action:      req.Action,
					Params:      req.Params,
					Path:        req.Path,
				},
				Action: proto.PermissionDeny,
			})
			return
		}

		if respMsg.Error != nil {
			s.logger.Error("Client returned error for permission request", "code", respMsg.Error.Code, "message", respMsg.Error.Message)
			_, _ = s.backend.GrantPermission(workspaceID, proto.PermissionGrant{
				Permission: proto.PermissionRequest{
					ID:          req.ID,
					SessionID:   req.SessionID,
					ToolCallID:  req.ToolCallID,
					ToolName:    req.ToolName,
					Description: req.Description,
					Action:      req.Action,
					Params:      req.Params,
					Path:        req.Path,
				},
				Action: proto.PermissionDeny,
			})
			return
		}

		var respObj requestPermissionResponse
		if err := json.Unmarshal(respMsg.Result, &respObj); err != nil {
			s.logger.Error("Failed to unmarshal permission response", "error", err)
			_, _ = s.backend.GrantPermission(workspaceID, proto.PermissionGrant{
				Permission: proto.PermissionRequest{
					ID:          req.ID,
					SessionID:   req.SessionID,
					ToolCallID:  req.ToolCallID,
					ToolName:    req.ToolName,
					Description: req.Description,
					Action:      req.Action,
					Params:      req.Params,
					Path:        req.Path,
				},
				Action: proto.PermissionDeny,
			})
			return
		}

		outcome := respObj.Outcome

		var action proto.PermissionAction
		if outcome.Outcome == "cancelled" {
			action = proto.PermissionDeny
		} else {
			switch permissionOptionKind(outcome.OptionID) {
			case permAllowOnce, "once", "allow":
				action = proto.PermissionAllow
			case permAllowAlways, "always":
				action = proto.PermissionAllowForSession
			case permRejectOnce, permRejectAlways, "reject", "deny":
				action = proto.PermissionDeny
			default:
				s.logger.Warn("Unknown permission option returned by client, denying", "optionID", outcome.OptionID)
				action = proto.PermissionDeny
			}
		}

		s.logger.Info("Applying permission decision", "optionID", outcome.OptionID, "action", action)
		_, err = s.backend.GrantPermission(workspaceID, proto.PermissionGrant{
			Permission: proto.PermissionRequest{
				ID:          req.ID,
				SessionID:   req.SessionID,
				ToolCallID:  req.ToolCallID,
				ToolName:    req.ToolName,
				Description: req.Description,
				Action:      req.Action,
				Params:      req.Params,
				Path:        req.Path,
			},
			Action: action,
		})
		if err != nil {
			s.logger.Error("Failed to apply permission decision", "error", err)
		}
	}()
}

// handleMessage translates a message.Message event into ACP update notifications.
func (s *Service) handleMessage(sessionID string, m message.Message) {
	if m.SessionID != "" && m.SessionID != sessionID {
		return
	}

	switch m.Role {
	case message.User:
		s.seenMu.Lock()
		if s.seenUserMsg[m.ID] {
			s.seenMu.Unlock()
			return
		}
		s.seenUserMsg[m.ID] = true
		s.seenMu.Unlock()

		text := extractMessageText(m.Parts)
		if text == "" {
			return
		}
		s.sendSessionUpdate(sessionID, SessionUpdate{
			UserMessageChunk: &SessionUpdateUserMessageChunk{
				Content: contentBlock{Type: "text", Text: text},
				Text:    text,
			},
		})

	case message.Assistant:
		for _, part := range m.Parts {
			switch p := part.(type) {
			case message.TextContent:
				text := p.Text
				if text == "" {
					continue
				}
				// Deduplicate: the message service publishes the full
				// accumulated text on every Update. Only emit the delta.
				s.seenMu.Lock()
				lastLen := s.seenText[m.ID]
				if len(text) <= lastLen {
					s.seenMu.Unlock()
					continue
				}
				delta := text[lastLen:]
				s.seenText[m.ID] = len(text)

				// Track accumulated full text for final consolidated message.
				s.fullText[m.ID] = text
				s.seenMu.Unlock()

				// Track this message ID as the latest assistant message
				// for its session so handleNotification can look up the
				// correct accumulated text (keyed by message ID, not
				// session ID).
				s.mu.Lock()
				s.lastAssistantMsg[sessionID] = m.ID
				s.mu.Unlock()

				s.sendSessionUpdate(sessionID, SessionUpdate{
					AgentMessageChunk: &SessionUpdateAgentMessageChunk{
						MessageID: strPtr(m.ID),
						Content:   contentBlock{Type: "text", Text: delta},
						Text:      delta,
					},
				})
			case message.ToolCall:
				s.seenMu.Lock()
				lastStatus := s.seenToolCallStatus[p.ID]
				if lastStatus == "in_progress" || lastStatus == "completed" || lastStatus == "failed" {
					s.seenMu.Unlock()
					continue
				}
				s.seenMu.Unlock()

				s.mu.Lock()
				state, hasSession := s.sessions[sessionID]
				s.mu.Unlock()

				var workspacePath string
				if hasSession {
					if ws, err := s.backend.GetWorkspace(state.workspaceID); err == nil {
						workspacePath = ws.Path
					}
				}

				title := buildToolCallTitle(p.Name, p.Input, workspacePath)

				var rawInput json.RawMessage
				if p.Input != "" {
					if json.Valid([]byte(p.Input)) {
						rawInput = json.RawMessage(p.Input)
					} else {
						if b, err := json.Marshal(p.Input); err == nil {
							rawInput = json.RawMessage(b)
						}
					}
				}

				if lastStatus == "" {
					s.seenMu.Lock()
					s.seenToolCallStatus[p.ID] = "pending"
					s.seenMu.Unlock()

					s.sendSessionUpdate(sessionID, SessionUpdate{
						ToolCall: &SessionUpdateToolCall{
							ToolCallID: p.ID,
							Title:      title,
							Kind:       mapToolKind(p.Name),
							Status:     "pending",
							RawInput:   rawInput,
						},
					})

					if p.Finished {
						s.seenMu.Lock()
						s.seenToolCallStatus[p.ID] = "in_progress"
						s.seenMu.Unlock()

						var locations []location
						if p.Input != "" {
							locations = buildToolCallLocations(p.Name, p.Input, workspacePath)
						}

						s.sendSessionUpdate(sessionID, SessionUpdate{
							ToolCallUpdate: &SessionToolCallUpdate{
								ToolCallID: p.ID,
								Status:     "in_progress",
								Title:      title,
								Kind:       mapToolKind(p.Name),
								Locations:  locations,
								RawInput:   rawInput,
							},
						})
					}
				} else if lastStatus == "pending" && p.Finished {
					s.seenMu.Lock()
					s.seenToolCallStatus[p.ID] = "in_progress"
					s.seenMu.Unlock()

					var locations []location
					if p.Input != "" {
						locations = buildToolCallLocations(p.Name, p.Input, workspacePath)
					}

					s.sendSessionUpdate(sessionID, SessionUpdate{
						ToolCallUpdate: &SessionToolCallUpdate{
							ToolCallID: p.ID,
							Status:     "in_progress",
							Title:      title,
							Kind:       mapToolKind(p.Name),
							Locations:  locations,
							RawInput:   rawInput,
						},
					})
				}
			case message.ToolResult:
				status := "completed"
				if p.IsError {
					status = "failed"

					s.mu.Lock()
					lastMsgID := s.lastAssistantMsg[sessionID]
					s.mu.Unlock()

					errText := p.Content
					if errText == "" {
						errText = "unknown error"
					}
					errMsg := fmt.Sprintf("\n⚠️ [Tool Error: %s]\n", errText)
					var msgIDPtr *string
					if lastMsgID != "" {
						msgIDPtr = &lastMsgID
					}
					s.sendSessionUpdate(sessionID, SessionUpdate{
						AgentMessageChunk: &SessionUpdateAgentMessageChunk{
							MessageID: msgIDPtr,
							Content:   contentBlock{Type: "text", Text: errMsg},
							Text:      errMsg,
						},
					})
				}
				s.seenMu.Lock()
				lastStatus := s.seenToolCallStatus[p.ToolCallID]
				if lastStatus == status {
					s.seenMu.Unlock()
					continue
				}
				s.seenToolCallStatus[p.ToolCallID] = status
				s.seenMu.Unlock()

				var content []contentBlock
				if p.Content != "" {
					content = []contentBlock{{Type: "text", Text: p.Content}}
				}
				s.sendSessionUpdate(sessionID, SessionUpdate{
					ToolCallUpdate: &SessionToolCallUpdate{
						ToolCallID: p.ToolCallID,
						Status:     status,
						Content:    content,
						Error:      p.Content,
					},
				})
			case message.ReasoningContent:
				if p.Thinking != "" {
					s.seenMu.Lock()
					lastLen := s.seenThinking[m.ID]
					if len(p.Thinking) <= lastLen {
						s.seenMu.Unlock()
						continue
					}
					delta := p.Thinking[lastLen:]
					s.seenThinking[m.ID] = len(p.Thinking)
					s.seenMu.Unlock()

					s.sendSessionUpdate(sessionID, SessionUpdate{
						AgentThoughtChunk: &SessionUpdateAgentThoughtChunk{
							MessageID: strPtr(m.ID),
							Content:   contentBlock{Type: "text", Text: delta},
						},
					})
				}
			}
		}
	}
}

// ----- Workspace lookup helpers -----

// findBackendWorkspaceForSession finds the backend.Workspace containing the
// given session ID. If not found in currently active in-memory workspaces
// (e.g. after process restart), it attempts to load the workspace for the
// current working directory and query its database.
func (s *Service) findBackendWorkspaceForSession(sessionID string) (*backend.Workspace, error) {
	ctx := context.Background()
	for _, pw := range s.backend.ListWorkspaces() {
		se, err := s.backend.GetSession(ctx, pw.ID, sessionID)
		if err == nil && se.ID == sessionID {
			return s.backend.GetWorkspace(pw.ID)
		}
	}

	// Fallback: If workspace is not in memory (e.g. fresh process start on session/resume),
	// ensure the workspace for the current working directory is loaded and search its database.
	cwd, err := os.Getwd()
	if err == nil && cwd != "" {
		clientID := uuid.New().String()
		wsProto := proto.Workspace{
			Path:     cwd,
			ClientID: clientID,
		}
		ws, _, createErr := s.backend.CreateWorkspace(wsProto)
		if createErr == nil {
			se, getErr := s.backend.GetSession(ctx, ws.ID, sessionID)
			if getErr == nil && se.ID == sessionID {
				if attachErr := s.backend.AttachClient(ws.ID, clientID); attachErr != nil {
					s.logger.Warn("Failed to attach client during session fallback lookup", "error", attachErr)
				}
				return ws, nil
			}
		}
	}

	return nil, fmt.Errorf("workspace not found for session: %s", sessionID)
}

// ----- Helpers -----

// extractMessageText extracts plain text from message.ContentPart values.
func extractMessageText(parts []message.ContentPart) string {
	var b strings.Builder
	for _, p := range parts {
		if tc, ok := p.(message.TextContent); ok {
			if b.Len() > 0 {
				b.WriteByte(' ')
			}
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

// extractTextFromBlocks extracts plain text from ACP content blocks.
func extractTextFromBlocks(blocks []contentBlock) string {
	var b strings.Builder
	for _, block := range blocks {
		if block.Type == "text" && block.Text != "" {
			if b.Len() > 0 {
				b.WriteByte(' ')
			}
			b.WriteString(block.Text)
		}
	}
	return b.String()
}

// mapToolKind maps a Phosphor tool name to an ACP tool call kind.
func mapToolKind(name string) toolCallKind {
	switch name {
	case "bash", "shell":
		return toolCallKindExecute
	case "view", "view_node", "read_file", "cat", "ls":
		return toolCallKindRead
	case "edit", "write", "append", "multiedit":
		return toolCallKindEdit
	case "delete_file", "rm":
		return toolCallKindDelete
	case "move_file", "rename":
		return toolCallKindMove
	case "glob", "grep", "rg", "search", "find", "structural_search", "references":
		return toolCallKindSearch
	case "fetch", "web_fetch", "download", "agentic_fetch":
		return toolCallKindFetch
	case "think", "reason":
		return toolCallKindThink
	default:
		return toolCallKindOther
	}
}

// strPtr returns a pointer to the given string.
func strPtr(s string) *string { return &s }

func parseURItoPath(uriStr string) string {
	p := uriStr
	if strings.HasPrefix(p, "file://") {
		p = strings.TrimPrefix(p, "file://")
		// On Windows, if we have file:///C:/foo, the remaining is /C:/foo.
		// We want to keep C:/foo, so we trim the leading slash if it is followed by a drive letter.
		if len(p) >= 3 && p[0] == '/' && p[2] == ':' {
			p = p[1:]
		}
	}
	return p
}

func extractPromptAndAttachments(blocks []contentBlock) (string, []proto.Attachment) {
	var b strings.Builder
	var attachments []proto.Attachment

	for _, block := range blocks {
		switch block.Type {
		case "text":
			if block.Text != "" {
				b.WriteString(block.Text)
			}
		case "resource":
			if block.Resource != nil {
				uri := block.Resource.URI
				path := parseURItoPath(uri)
				name := block.Resource.Name
				if name == "" {
					name = filepath.Base(path)
				}
				if name == "." || name == "/" {
					name = "resource"
				}

				b.WriteString(fmt.Sprintf("[%s]", name))

				var content []byte
				if block.Resource.Text != "" {
					content = []byte(block.Resource.Text)
				} else if block.Resource.Blob != "" {
					if dec, err := base64.StdEncoding.DecodeString(block.Resource.Blob); err == nil {
						content = dec
					}
				}

				mime := block.Resource.MIMEType
				if mime == "" {
					mime = "text/plain"
				}

				attachments = append(attachments, proto.Attachment{
					FilePath: path,
					FileName: name,
					MimeType: mime,
					Content:  content,
				})
			}
		case "resource_link":
			if block.ResourceLink != nil {
				uri := block.ResourceLink.URI
				path := parseURItoPath(uri)
				name := block.ResourceLink.Name
				if name == "" {
					name = filepath.Base(path)
				}
				if name == "." || name == "/" {
					name = "resource"
				}

				b.WriteString(fmt.Sprintf("[%s]", name))

				mime := block.ResourceLink.MIMEType
				if mime == "" {
					mime = "text/plain"
				}

				attachments = append(attachments, proto.Attachment{
					FilePath: path,
					FileName: name,
					MimeType: mime,
				})
			}
		}
	}

	return b.String(), attachments
}

// buildToolCallTitle returns a descriptive title for a tool call based on its arguments.
func buildToolCallTitle(name, inputJsonStr, workspacePath string) string {
	var args map[string]any
	if err := json.Unmarshal([]byte(inputJsonStr), &args); err == nil {
		// Extract common path/file field if present
		var pathVal string
		for _, key := range []string{"file_path", "TargetFile", "AbsolutePath", "SearchPath", "path", "Path"} {
			if val, ok := args[key]; ok {
				if strVal, ok := val.(string); ok && strVal != "" {
					cleanPath := filepath.Clean(strVal)
					if !filepath.IsAbs(cleanPath) && !strings.HasPrefix(cleanPath, "..") {
						pathVal = filepath.ToSlash(cleanPath)
					} else if workspacePath != "" {
						if rel, err := filepath.Rel(workspacePath, cleanPath); err == nil && !strings.HasPrefix(rel, "..") {
							pathVal = filepath.ToSlash(rel)
						} else {
							pathVal = filepath.Base(cleanPath)
						}
					} else {
						pathVal = filepath.Base(cleanPath)
					}
					break
				}
			}
		}

		// Extract pattern/query field if present
		var patternVal string
		for _, key := range []string{"pattern", "query", "Query", "Pattern", "prompt", "Prompt"} {
			if val, ok := args[key]; ok {
				if strVal, ok := val.(string); ok && strVal != "" {
					patternVal = strVal
					break
				}
			}
		}

		// Extract url field if present
		var urlVal string
		for _, key := range []string{"url", "Url", "URL"} {
			if val, ok := args[key]; ok {
				if strVal, ok := val.(string); ok && strVal != "" {
					urlVal = strVal
					break
				}
			}
		}

		// Extract command field if present
		var cmdVal string
		for _, key := range []string{"CommandLine", "command", "cmd"} {
			if val, ok := args[key]; ok {
				if strVal, ok := val.(string); ok && strVal != "" {
					cmdVal = strVal
					break
				}
			}
		}

		// Format according to the tool name
		switch name {
		case "view":
			if pathVal != "" {
				return fmt.Sprintf("View file %s", pathVal)
			}
		case "ls":
			if pathVal != "" {
				return fmt.Sprintf("List directory %s", pathVal)
			}
			return "List directory"
		case "edit", "multiedit":
			if pathVal != "" {
				return fmt.Sprintf("Edit file %s", pathVal)
			}
		case "write":
			if pathVal != "" {
				return fmt.Sprintf("Write file %s", pathVal)
			}
		case "append":
			if pathVal != "" {
				return fmt.Sprintf("Append to file %s", pathVal)
			}
		case "glob":
			if patternVal != "" {
				return fmt.Sprintf("Glob pattern %s", patternVal)
			}
		case "grep", "rg":
			if patternVal != "" {
				return fmt.Sprintf("Grep for \"%s\"", patternVal)
			}
		case "search", "find":
			if patternVal != "" {
				return fmt.Sprintf("Search for \"%s\"", patternVal)
			}
		case "structural_search":
			if patternVal != "" {
				return fmt.Sprintf("Structural search for \"%s\"", patternVal)
			}
		case "web_search":
			if patternVal != "" {
				return fmt.Sprintf("Web search for \"%s\"", patternVal)
			}
		case "fetch", "web_fetch", "download":
			if urlVal != "" {
				return fmt.Sprintf("Fetch %s", urlVal)
			}
		case "agentic_fetch":
			if urlVal != "" {
				return fmt.Sprintf("Fetch %s", urlVal)
			} else if patternVal != "" {
				return fmt.Sprintf("Fetch information about \"%s\"", patternVal)
			}
		case "bash", "shell":
			if cmdVal != "" {
				cmd := cmdVal
				if len(cmd) > 30 {
					cmd = cmd[:27] + "..."
				}
				return fmt.Sprintf("Execute command: %s", cmd)
			}
		}

		// Fallbacks if formatting specific to tool name wasn't possible due to missing arguments
		if pathVal != "" {
			return fmt.Sprintf("%s %s", name, pathVal)
		}
		if urlVal != "" {
			return fmt.Sprintf("%s %s", name, urlVal)
		}
		if patternVal != "" {
			return fmt.Sprintf("%s: %s", name, patternVal)
		}
		if cmdVal != "" {
			cmd := cmdVal
			if len(cmd) > 30 {
				cmd = cmd[:27] + "..."
			}
			return fmt.Sprintf("%s: %s", name, cmd)
		}
	}
	return name
}

// buildToolCallLocations parses the tool call inputs and returns any
// referenced files as locations.
func buildToolCallLocations(name, inputJsonStr, workspacePath string) []location {
	var args map[string]any
	if err := json.Unmarshal([]byte(inputJsonStr), &args); err != nil {
		return nil
	}
	var pathVal string
	for _, key := range []string{"file_path", "TargetFile", "AbsolutePath", "SearchPath", "path", "Path"} {
		if val, ok := args[key]; ok {
			if strVal, ok := val.(string); ok && strVal != "" {
				pathVal = strVal
				break
			}
		}
	}
	if pathVal == "" {
		return nil
	}

	cleanPath := filepath.Clean(pathVal)
	resolvedPath := cleanPath
	if !filepath.IsAbs(cleanPath) && workspacePath != "" {
		resolvedPath = filepath.Join(workspacePath, cleanPath)
	}

	return []location{
		{
			Path: filepath.ToSlash(resolvedPath),
		},
	}
}

// Ensure Service implements platform.Service.
var _ platform.Service = (*Service)(nil)
