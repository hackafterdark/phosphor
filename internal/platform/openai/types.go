// Package openai implements OpenAI-compatible API endpoints for Phosphor.
//
// This package provides HTTP handlers that accept requests in OpenAI's wire
// format (chat completions, responses API, models listing) and route them
// through Phosphor's backend to the agent core. Streaming is implemented via
// SSE with OpenAI-compatible chunk format.
package openai

import (
	"encoding/json"
	"strings"
)

// ----- Request types -----

// ChatCompletionRequest represents an OpenAI chat completions request.
type ChatCompletionRequest struct {
	Model         string         `json:"model"`
	Messages      []ChatMessage  `json:"messages"`
	Stream        bool           `json:"stream"`
	StreamOptions *StreamOptions `json:"stream_options,omitempty"`
	Temperature   *float64       `json:"temperature,omitempty"`
	MaxTokens     *int           `json:"max_tokens,omitempty"`
	TopP          *float64       `json:"top_p,omitempty"`
	TopK          *int64         `json:"top_k,omitempty"`
	FrequencyPenalty *float64    `json:"frequency_penalty,omitempty"`
	PresencePenalty  *float64    `json:"presence_penalty,omitempty"`
	Seed          *int64         `json:"seed,omitempty"`
	MinP          *float64       `json:"min_p,omitempty"`
	RepetitionPenalty *float64   `json:"repetition_penalty,omitempty"`
	Stop          []string       `json:"stop,omitempty"`
	TopLogProbs   *int64         `json:"top_logprobs,omitempty"`
	MaxThinkingTokens *int64    `json:"max_thinking_tokens,omitempty"`

	// SessionID is the optional session identifier for persistent
	// conversations. Populated from the X-Phosphor-Session-Id header or
	// the request body's session_id field. Empty means auto-create.
	SessionID string `json:"session_id,omitempty"`
}

// ChatMessage represents a single message in a chat completion request.
type ChatMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

// StreamOptions controls streaming behavior.
type StreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

// ResponseCreateRequest represents an OpenAI responses API request.
type ResponseCreateRequest struct {
	Model              string            `json:"model"`
	Input              json.RawMessage   `json:"input"`
	PreviousResponseID string            `json:"previous_response_id,omitempty"`
	Metadata           map[string]string `json:"metadata,omitempty"`
	Stream             bool              `json:"stream"`
}

// ----- Response types -----

// ChatCompletionResponse represents an OpenAI chat completions response.
type ChatCompletionResponse struct {
	ID      string                 `json:"id"`
	Object  string                 `json:"object"` // "chat.completion" or "chat.completion.chunk"
	Created int64                  `json:"created"`
	Model   string                 `json:"model"`
	Choices []ChatCompletionChoice `json:"choices"`
	Usage   *UsageInfo             `json:"usage,omitempty"`
}

// ChatCompletionChoice represents a single choice in a chat completion response.
type ChatCompletionChoice struct {
	Index        int          `json:"index"`
	Message      *ChatMessage `json:"message,omitempty"`
	Delta        *ChatMessage `json:"delta,omitempty"`
	FinishReason string       `json:"finish_reason"`
}

// UsageInfo represents token usage information.
type UsageInfo struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// ResponseCreateResponse represents an OpenAI responses API response.
type ResponseCreateResponse struct {
	ID     string           `json:"id"`
	Object string           `json:"object"` // "response" or "response.output_text.delta"
	Model  string           `json:"model"`
	Output []ResponseOutput `json:"output"`
	Usage  *UsageInfo       `json:"usage,omitempty"`
}

// ResponseOutput represents an output item in a responses API response.
type ResponseOutput struct {
	ID      string            `json:"id"`
	Type    string            `json:"type"` // "message"
	Role    string            `json:"role"`
	Content []ResponseContent `json:"content"`
}

// ResponseContent represents a content item in a responses API output.
type ResponseContent struct {
	Type  string `json:"type"` // "output_text"
	Text  string `json:"text"`
	Delta string `json:"delta,omitempty"`
}

// ----- Helper functions -----

// extractPrompt stitches all messages from an OpenAI messages array into a
// single prompt string with role prefixes. This preserves conversation
// history so the agent sees the full context, not just the last user message.
func extractPrompt(messages []ChatMessage) string {
	var sb strings.Builder
	for _, msg := range messages {
		if msg.Role == "" || msg.Content == nil {
			continue
		}
		var text string
		if err := json.Unmarshal(msg.Content, &text); err != nil {
			continue
		}
		if text == "" {
			continue
		}
		if sb.Len() > 0 {
			sb.WriteByte('\n')
		}
		switch msg.Role {
		case "system":
			sb.WriteString("System: ")
		case "assistant":
			sb.WriteString("Assistant: ")
		case "user":
			sb.WriteString("User: ")
		default:
			sb.WriteString(msg.Role + ": ")
		}
		sb.WriteString(text)
	}
	return sb.String()
}

// extractLastUserMessage returns the text of the last user message in the
// array, or empty string if none found. This is used for persistence so we
// don't store the stitched prompt (which would duplicate conversation history).
func extractLastUserMessage(messages []ChatMessage) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			var text string
			if err := json.Unmarshal(messages[i].Content, &text); err == nil {
				return text
			}
		}
	}
	return ""
}

// extractSystemPrompt extracts the system message from an OpenAI messages array,
// if one is present. Returns empty string if no system message is found.
func extractSystemPrompt(messages []ChatMessage) string {
	for _, msg := range messages {
		if msg.Role == "system" {
			var text string
			if err := json.Unmarshal(msg.Content, &text); err == nil {
				return text
			}
		}
	}
	return ""
}
