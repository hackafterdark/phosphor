package openai

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v5"
	"github.com/stretchr/testify/assert"
)

func TestAuthMiddleware_NoApiKey(t *testing.T) {
	e := echo.New()

	// Test with no API key (dev mode)
	middleware := AuthMiddleware("")

	e.Use(middleware)
	e.GET("/test", func(c *echo.Context) error {
		return c.String(http.StatusOK, "OK")
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestAuthMiddleware_WithApiKey(t *testing.T) {
	e := echo.New()

	// Test with API key
	middleware := AuthMiddleware("test-key")

	e.Use(middleware)
	e.GET("/test", func(c *echo.Context) error {
		return c.String(http.StatusOK, "OK")
	})

	// Test without auth header
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)

	// Test with invalid token
	req = httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Bearer wrong-key")
	rec = httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)

	// Test with valid token
	req = httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Bearer test-key")
	rec = httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestExtractPrompt(t *testing.T) {
	tests := []struct {
		name     string
		messages []ChatMessage
		expected string
	}{
		{
			name: "single user message",
			messages: []ChatMessage{
				{Role: "user", Content: json.RawMessage(`"Hello"`)},
			},
			expected: "User: Hello",
		},
		{
			name: "multiple messages, last is user",
			messages: []ChatMessage{
				{Role: "system", Content: json.RawMessage(`"You are helpful"`)},
				{Role: "user", Content: json.RawMessage(`"First question"`)},
				{Role: "assistant", Content: json.RawMessage(`"First answer"`)},
				{Role: "user", Content: json.RawMessage(`"Second question"`)},
			},
			expected: "System: You are helpful\nUser: First question\nAssistant: First answer\nUser: Second question",
		},
		{
			name:     "no user messages",
			messages: []ChatMessage{},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractPrompt(tt.messages)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestResolveModel(t *testing.T) {
	tests := []struct {
		name     string
		request  string
		fallback string
		expected string
	}{
		{
			name:     "requested model",
			request:  "gpt-4",
			fallback: "phosphor",
			expected: "gpt-4",
		},
		{
			name:     "empty request, use fallback",
			request:  "",
			fallback: "phosphor",
			expected: "phosphor",
		},
		{
			name:     "empty request and fallback",
			request:  "",
			fallback: "",
			expected: "phosphor",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := resolveModel(tt.request, tt.fallback)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestNewChatCompletionID(t *testing.T) {
	id := newChatCompletionID("test-run")
	assert.Equal(t, "chatcmpl-test-run", id)
}

func TestNewResponseID(t *testing.T) {
	id := newResponseID("test-response")
	assert.Equal(t, "resp-test-response", id)
}
