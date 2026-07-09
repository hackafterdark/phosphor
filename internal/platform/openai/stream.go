package openai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/hackafterdark/phosphor/internal/agent/notify"
	"github.com/hackafterdark/phosphor/internal/backend"
	"github.com/hackafterdark/phosphor/internal/message"
	"github.com/hackafterdark/phosphor/internal/pubsub"
)

// streamHandler manages an OpenAI-compatible SSE stream for a single
// chat completions request. It subscribes to workspace pubsub events,
// filters for assistant messages, and writes them as OpenAI chunk
// events in SSE format.
type streamHandler struct {
	backend     *backend.Backend
	workspaceID string
	sessionID   string
	model       string
	runID       string
	logger      *slog.Logger
	lastSentLen int
}

// newStreamHandler creates a new stream handler.
func newStreamHandler(b *backend.Backend, workspaceID, sessionID, model, runID string, logger *slog.Logger) *streamHandler {
	return &streamHandler{
		backend:     b,
		workspaceID: workspaceID,
		sessionID:   sessionID,
		model:       model,
		runID:       runID,
		logger:      logger,
	}
}

// start begins streaming. It blocks until the context is cancelled or
// the stream completes. Returns any error encountered during streaming.
func (s *streamHandler) start(ctx context.Context, w http.ResponseWriter) error {
	flusher := http.NewResponseController(w)

	// Set SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	// Subscribe to workspace events
	events, err := s.backend.SubscribeEvents(ctx, s.workspaceID)
	if err != nil {
		return fmt.Errorf("failed to subscribe to events: %w", err)
	}

	// Attach a persistent client so the workspace stays alive between requests
	// Use a fixed UUID to ensure the same client is used across all streams
	const openaiClientID = "00000000-0000-0000-0000-000000000001"
	if err := s.backend.AttachClient(s.workspaceID, openaiClientID); err != nil {
		return fmt.Errorf("failed to attach client: %w", err)
	}

	// Listen for message events and stream them as OpenAI chunks
	chunkCount := 0
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case ev, ok := <-events:
			if !ok {
				if s.logger != nil {
					s.logger.Info("Event stream closed",
						"workspace_id", s.workspaceID,
						"session_id", s.sessionID,
						"chunks_sent", chunkCount,
					)
				}
				return nil
			}

			// Check for RunComplete to know when to stop
			if rcEv, ok := ev.Payload.(pubsub.Event[notify.RunComplete]); ok {
				if rcEv.Payload.SessionID == s.sessionID && rcEv.Payload.RunID == s.runID {
					if s.logger != nil {
						s.logger.Info("Run complete received",
							"session_id", s.sessionID,
							"run_id", s.runID,
							"chunks_sent", chunkCount,
						)
					}
					s.writeDone(w, flusher)
					return nil
				}
				continue
			}

			// Filter for message events on the target session
			msgEv, ok := ev.Payload.(pubsub.Event[message.Message])
			if !ok {
				continue
			}
			if msgEv.Payload.SessionID != s.sessionID {
				continue
			}
			if msgEv.Payload.Role != message.Assistant {
				// Log user messages at debug level for transparency
				if s.logger != nil {
					s.logger.Debug("Received non-assistant message (skipped)",
						"session_id", msgEv.Payload.SessionID,
						"role", msgEv.Payload.Role,
						"message_id", msgEv.Payload.ID,
					)
				}
				continue
			}

			newText := msgEv.Payload.Content().Text
			var deltaText string
			if len(newText) > s.lastSentLen {
				deltaText = newText[s.lastSentLen:]
				s.lastSentLen = len(newText)
			} else {
				continue
			}

			chunkCount++
			if s.logger != nil {
				s.logger.Info("Streaming assistant message",
					"message_id", msgEv.Payload.ID,
					"session_id", s.sessionID,
					"text_length", len(msgEv.Payload.Content().Text),
					"chunk_number", chunkCount,
				)
			}

			// Convert to OpenAI delta format and write SSE
			delta := messageToDelta(deltaText)
			chunk := ChatCompletionResponse{
				ID:      newChatCompletionID(s.runID),
				Object:  "chat.completion.chunk",
				Created: nowUnix(),
				Model:   s.model,
				Choices: []ChatCompletionChoice{{
					Index:        0,
					Delta:        delta,
					FinishReason: "",
				}},
			}

			data, err := json.Marshal(chunk)
			if err != nil {
				s.logger.Error("Failed to marshal chunk", "error", err)
				continue
			}

			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
	}
}

// writeDone writes the final [DONE] event to the SSE stream.
func (s *streamHandler) writeDone(w http.ResponseWriter, flusher *http.ResponseController) {
	fmt.Fprintf(w, "data: [DONE]\n\n")
	flusher.Flush()
}

// waitForCompletion blocks until the agent run completes or the context
// is cancelled. Returns the final text and usage info.
func waitForCompletion(ctx context.Context, b *backend.Backend, workspaceID, sessionID, runID string) (text string, usage *UsageInfo, err error) {
	// Subscribe to events to wait for completion
	events, err := b.SubscribeEvents(ctx, workspaceID)
	if err != nil {
		return "", nil, fmt.Errorf("failed to subscribe to events: %w", err)
	}

	for {
		select {
		case <-ctx.Done():
			return "", nil, ctx.Err()
		case ev, ok := <-events:
			if !ok {
				return "", nil, errors.New("event stream closed")
			}

			// Check for RunComplete event
			rcEv, ok := ev.Payload.(pubsub.Event[notify.RunComplete])
			if !ok {
				continue
			}
			if rcEv.Payload.RunID != runID {
				continue
			}

			if rcEv.Payload.Error != "" {
				return "", nil, errors.New(rcEv.Payload.Error)
			}

			return rcEv.Payload.Text, nil, nil
		}
	}
}

// messageToDelta converts the incremental delta text to an OpenAI ChatMessage delta.
// This is used for streaming where we emit each delta text chunk.
func messageToDelta(deltaText string) *ChatMessage {
	delta := &ChatMessage{
		Role: "assistant",
	}

	if deltaText != "" {
		content, _ := json.Marshal(deltaText)
		delta.Content = content
	}

	return delta
}

// resolveModel determines the model name to return in responses.
// The model field is cosmetic — Phosphor uses the configured model from phosphor.json.
func resolveModel(requested, fallback string) string {
	if requested != "" {
		return requested
	}
	if fallback != "" {
		return fallback
	}
	return "phosphor"
}

// newChatCompletionID generates a unique chat completion ID.
func newChatCompletionID(suffix string) string {
	return fmt.Sprintf("chatcmpl-%s", suffix)
}

// newResponseID generates a unique response ID.
func newResponseID(suffix string) string {
	return fmt.Sprintf("resp-%s", suffix)
}

// nowUnix returns the current Unix timestamp in seconds.
func nowUnix() int64 {
	return time.Now().Unix()
}
