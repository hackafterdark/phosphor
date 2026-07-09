package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/hackafterdark/phosphor/internal/backend"
	"github.com/hackafterdark/phosphor/internal/config"
	"github.com/hackafterdark/phosphor/internal/proto"
	"github.com/labstack/echo/v5"
)

// Handler provides OpenAI-compatible HTTP endpoints.
type Handler struct {
	backend            *backend.Backend
	workspaceID        string
	model              string
	logger             *slog.Logger
	acceptSystemPrompt bool
	logRequestBody     bool
}

// NewHandler creates a new OpenAI handler.
func NewHandler(b *backend.Backend, workspaceID string, logger *slog.Logger, acceptSystemPrompt, logRequestBody bool) *Handler {
	return &Handler{
		backend:            b,
		workspaceID:        workspaceID,
		logger:             logger,
		acceptSystemPrompt: acceptSystemPrompt,
		logRequestBody:     logRequestBody,
	}
}

// WorkspaceID returns the handler's workspace ID.
func (h *Handler) WorkspaceID() string {
	return h.workspaceID
}

// HandleHealth responds with a simple health check.
func (h *Handler) HandleHealth(c *echo.Context) error {
	return c.String(http.StatusOK, "OK")
}

// HandleChatCompletions processes OpenAI chat completions requests.
func (h *Handler) HandleChatCompletions(c *echo.Context) error {
	// Optionally log raw request body for client debugging (e.g. Open WebUI
	// compatibility). Read before Bind so we capture the unmodified wire
	// format; restore the reader so Bind can still consume it.
	var rawBody []byte
	if h.logRequestBody {
		rawBody, _ = io.ReadAll(c.Request().Body)
		c.Request().Body = io.NopCloser(bytes.NewReader(rawBody))
	}

	var req ChatCompletionRequest
	if err := c.Bind(&req); err != nil {
		if h.logger != nil {
			h.logger.Warn("Failed to bind chat completion request", "error", err)
		}
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}

	if h.logRequestBody && len(rawBody) > 0 && h.logger != nil {
		bodyStr := string(rawBody)
		if len(bodyStr) > maxLogBodySize {
			bodyStr = bodyStr[:maxLogBodySize] + "...(truncated)"
		}
		h.logger.Debug("OpenAI request body",
			"method", c.Request().Method,
			"path", c.Request().URL.Path,
			"body_size", len(rawBody),
			"body", bodyStr,
		)
	}

	if h.logger != nil {
		h.logger.Info("Chat completions request",
			"model", req.Model,
			"stream", req.Stream,
			"messages_count", len(req.Messages),
		)
	}

	// Resolve session
	sessionID, err := h.resolveSession(c.Request(), req.SessionID)
	if err != nil {
		if h.logger != nil {
			h.logger.Error("Failed to resolve session", "error", err)
		}
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}

	// Extract prompt from messages (stitched for full context)
	prompt := extractPrompt(req.Messages)
	if prompt == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "no user message found")
	}

	// Extract the original last user message for persistence (avoid storing
	// the stitched version which would duplicate conversation history).
	userPrompt := extractLastUserMessage(req.Messages)

	// Extract system prompt if accepted
	var systemPrompt string
	if h.acceptSystemPrompt {
		systemPrompt = extractSystemPrompt(req.Messages)
	}

	// Auto-name the default session from the first user prompt.
	h.autoNameDefaultSession(c.Request().Context(), sessionID, prompt)

	// Return X-Phosphor-Session-Id so clients can opt into stateful sessions.
	h.setSessionHeader(c, sessionID)

	// Resolve model
	model := resolveModel(req.Model, h.model)

	// Generate run ID
	runID := fmt.Sprintf("run-%s", newChatCompletionID(""))

	// Dispatch to backend
	msg := proto.AgentMessage{
		SessionID:         sessionID,
		Prompt:            prompt,
		UserPrompt:        userPrompt,
		RunID:             runID,
		SystemPrompt:      systemPrompt,
		Temperature:       req.Temperature,
		MaxOutputTokens: func() int64 { if req.MaxTokens != nil { return int64(*req.MaxTokens) }; return 0 }(),
		TopP:              req.TopP,
		TopK:              req.TopK,
		FrequencyPenalty:  req.FrequencyPenalty,
		PresencePenalty:   req.PresencePenalty,
		Seed:              req.Seed,
		MinP:              req.MinP,
		RepetitionPenalty: req.RepetitionPenalty,
		Stop:              req.Stop,
		TopLogProbs:       req.TopLogProbs,
		MaxThinkingTokens: req.MaxThinkingTokens,
	}

	if err := h.backend.SendMessage(h.workspaceID, msg); err != nil {
		if h.logger != nil {
			h.logger.Error("Failed to send message to agent",
				"error", err,
				"workspace_id", h.workspaceID,
				"session_id", sessionID,
			)
		}
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}

	if h.logger != nil {
		h.logger.Info("Message dispatched to agent",
			"run_id", runID,
			"session_id", sessionID,
		)
	}

	// Handle streaming vs non-streaming
	if req.Stream {
		return h.handleStreaming(c, runID, sessionID, model)
	}

	return h.handleNonStreaming(c, runID, sessionID, model)
}

// handleStreaming sets up SSE streaming for chat completions.
func (h *Handler) handleStreaming(c *echo.Context, runID, sessionID, model string) error {
	// Get the ResponseWriter directly from Echo v5 context
	w := c.Response()

	// Set SSE headers BEFORE writing anything - use Echo's response header map
	c.Response().Header().Set("Content-Type", "text/event-stream")
	c.Response().Header().Set("Cache-Control", "no-cache")
	c.Response().Header().Set("Connection", "keep-alive")
	c.Response().Header().Set("X-Accel-Buffering", "no")

	stream := newStreamHandler(h.backend, h.workspaceID, sessionID, model, runID, h.logger)

	// Start streaming synchronously - blocks until completion or context cancellation
	if err := stream.start(c.Request().Context(), w); err != nil && !errors.Is(err, context.Canceled) {
		if h.logger != nil {
			h.logger.Error("Stream error", "error", err)
		}
		return err
	}

	return nil
}

// handleNonStreaming waits for completion and returns the result.
func (h *Handler) handleNonStreaming(c *echo.Context, runID, sessionID, model string) error {
	resultCtx, resultCancel := context.WithTimeout(c.Request().Context(), 5*time.Minute)
	defer resultCancel()

	h.logger.Info("Waiting for agent completion", "run_id", runID, "session_id", sessionID)
	text, _, err := waitForCompletion(resultCtx, h.backend, h.workspaceID, sessionID, runID)
	if err != nil {
		h.logger.Error("Failed to wait for completion", "error", err)
		if errors.Is(err, context.DeadlineExceeded) {
			return echo.NewHTTPError(http.StatusGatewayTimeout, "request timed out")
		}
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}

	h.logger.Info("Agent completion received", "run_id", runID, "text_length", len(text))

	resp := ChatCompletionResponse{
		ID:      newChatCompletionID(runID),
		Object:  "chat.completion",
		Created: nowUnix(),
		Model:   model,
		Choices: []ChatCompletionChoice{{
			Index:        0,
			Message:      &ChatMessage{Role: "assistant", Content: json.RawMessage(`"` + text + `"`)},
			FinishReason: "stop",
		}},
	}

	return c.JSON(http.StatusOK, resp)
}

// HandleResponses processes OpenAI responses API requests.
func (h *Handler) HandleResponses(c *echo.Context) error {
	var req ResponseCreateRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}

	// Extract prompt from input
	prompt := extractPromptFromResponsesInput(req.Input)
	if prompt == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "no input found")
	}

	// Resolve session
	sessionID := c.Request().Header.Get("X-Phosphor-Session-Id")
	if sessionID == "" && req.PreviousResponseID != "" {
		// TODO: Look up session from previous_response_id
		sessionID = fmt.Sprintf("resp-%s", req.PreviousResponseID)
	}

	// If no session ID was provided, use the default (stateless) session.
	var resolveErr error
	if sessionID == "" {
		sessionID, resolveErr = h.getOrCreateDefaultSession(c.Request().Context())
		if resolveErr != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, resolveErr.Error())
		}
	}

	// Auto-name the default session from the first user prompt.
	h.autoNameDefaultSession(c.Request().Context(), sessionID, prompt)

	// Return X-Phosphor-Session-Id so clients can opt into stateful sessions.
	h.setSessionHeader(c, sessionID)

	// Resolve model
	model := resolveModel(req.Model, h.model)

	// Generate run ID
	runID := fmt.Sprintf("run-%s", newResponseID(""))

	// Dispatch to backend
	msg := proto.AgentMessage{
		SessionID: sessionID,
		Prompt:    prompt,
		RunID:     runID,
	}

	if err := h.backend.SendMessage(h.workspaceID, msg); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}

	// Handle streaming vs non-streaming
	if req.Stream {
		return h.handleStreaming(c, runID, sessionID, model)
	}

	return h.handleNonStreaming(c, runID, sessionID, model)
}

// HandleModels lists available models.
func (h *Handler) HandleModels(c *echo.Context) error {
	cfgStore := h.backend.Config()
	if cfgStore == nil {
		resp := map[string]interface{}{
			"object": "list",
			"data": []map[string]interface{}{
				{
					"id":       "phosphor-default",
					"object":   "model",
					"created":  0,
					"owned_by": "phosphor",
				},
			},
		}
		return c.JSON(http.StatusOK, resp)
	}

	cfg := cfgStore.Config()
	models := make([]map[string]interface{}, 0)

	// Show configured models from phosphor.json (large and small)
	for _, key := range []config.SelectedModelType{config.SelectedModelTypeLarge, config.SelectedModelTypeSmall} {
		if m, ok := cfg.Models[key]; ok && m.Model != "" {
			models = append(models, map[string]interface{}{
				"id":       m.Model,
				"object":   "model",
				"created":  0,
				"owned_by": m.Provider,
			})
		}
	}

	if len(models) == 0 {
		models = []map[string]interface{}{
			{
				"id":       "phosphor-default",
				"object":   "model",
				"created":  0,
				"owned_by": "phosphor",
			},
		}
	}

	resp := map[string]interface{}{
		"object": "list",
		"data":   models,
	}

	return c.JSON(http.StatusOK, resp)
}

// resolveSession resolves or creates a session for the request. When the
// client provides a session ID (via header or request body) it is used as-is.
// Otherwise a shared default (stateless) session per workspace is returned —
// this prevents Open WebUI from spawning one "Untitled" session per turn.
func (h *Handler) resolveSession(req *http.Request, requestedSessionID string) (string, error) {
	// Path 1: Client explicitly provides a session ID → use it (stateful).
	if requestedSessionID != "" {
		return requestedSessionID, nil
	}

	// Check header
	sessionID := req.Header.Get("X-Phosphor-Session-Id")
	if sessionID != "" {
		return sessionID, nil
	}

	// Path 2: No session ID → use (or create) the default (stateless) session.
	return h.getOrCreateDefaultSession(req.Context())
}

// getOrCreateDefaultSession returns the workspace's shared stateless session,
// creating one on first request. The returned session is marked IsStateless
// so the agent skips history loading and the TUI hides it from session lists.
func (h *Handler) getOrCreateDefaultSession(ctx context.Context) (string, error) {
	sess, err := h.backend.GetDefaultSession(ctx, h.workspaceID)
	if err == nil {
		return sess.ID, nil
	}
	if !errors.Is(err, backend.ErrSessionNotFound) {
		return "", fmt.Errorf("failed to get default session: %w", err)
	}

	// Create the stateless (default) session.
	newSess, err := h.backend.CreateSession(ctx, h.workspaceID, "API Sessions")
	if err != nil {
		return "", fmt.Errorf("failed to create default session: %w", err)
	}

	// Mark it as stateless with provenance so the agent and TUI can act on it.
	if err := h.backend.UpdateSessionStateless(ctx, h.workspaceID, newSess.ID, true, "openai-api"); err != nil {
		return "", fmt.Errorf("failed to mark default session as stateless: %w", err)
	}

	return newSess.ID, nil
}

// setSessionHeader writes the resolved session ID back to the response so
// OpenAI-compatible clients can opt into stateful sessions on follow-up turns.
func (h *Handler) setSessionHeader(c *echo.Context, sessionID string) {
	c.Response().Header().Set("X-Phosphor-Session-Id", sessionID)
}

// autoNameDefaultSession renames the default (stateless) session from its
// placeholder "API Sessions" to a short excerpt of the first user prompt.
// This gives the shared audit-log session a usable name without requiring
// an extra LLM call for title generation.
func (h *Handler) autoNameDefaultSession(ctx context.Context, sessionID, prompt string) {
	sess, err := h.backend.GetSession(ctx, h.workspaceID, sessionID)
	if err != nil || !sess.IsStateless || sess.Title != "API Sessions" {
		return
	}
	title := prompt
	if len(title) > 50 {
		title = title[:50] + "..."
	}
	if err := h.backend.RenameSession(ctx, h.workspaceID, sessionID, title); err != nil {
		if h.logger != nil {
			h.logger.Warn("Failed to auto-name default session", "error", err)
		}
	}
}

// extractPromptFromResponsesInput extracts the prompt from a responses API input.
// The input can be a string or an array of messages.
func extractPromptFromResponsesInput(input json.RawMessage) string {
	var text string
	if err := json.Unmarshal(input, &text); err == nil {
		return text
	}

	var messages []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(input, &messages); err != nil {
		return ""
	}

	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			return messages[i].Content
		}
	}

	return ""
}

// resolveAPIKey resolves the API key from environment or config.
func resolveAPIKey(cfg *config.ConfigStore) string {
	if cfg == nil {
		return os.Getenv("API_SERVER_KEY")
	}

	entry, ok := cfg.Config().Services["openai-api"]
	if !ok {
		return os.Getenv("API_SERVER_KEY")
	}

	return entry.Auth.Key
}

// maxLogBodySize caps the size of request bodies logged for debugging.
const maxLogBodySize = 4 * 1024