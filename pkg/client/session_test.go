package client_test

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hackafterdark/phosphor/pkg/client"
	"github.com/hackafterdark/phosphor/pkg/config"
	"github.com/hackafterdark/phosphor/pkg/db"
	"github.com/stretchr/testify/require"
)

func TestSessionLifecycleAndEvents(t *testing.T) {
	// Enable mock providers for testing
	originalUseMock := config.UseMockProviders
	config.UseMockProviders = true
	defer func() {
		config.UseMockProviders = originalUseMock
		config.ResetProviders()
	}()
	config.ResetProviders()

	// Spin up local mock OpenAI SSE server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
			return
		}

		chunks := []string{"Hello ", "from ", "mock ", "agent!"}
		for _, chunk := range chunks {
			resp := fmt.Sprintf(`{"id":"chatcmpl-123","object":"chat.completion.chunk","created":1677652288,"model":"mock-model","choices":[{"index":0,"delta":{"content":"%s"},"finish_reason":null}]}`, chunk)
			fmt.Fprintf(w, "data: %s\n\n", resp)
			flusher.Flush()
			time.Sleep(10 * time.Millisecond)
		}

		fmt.Fprintf(w, "data: {\"id\":\"chatcmpl-123\",\"object\":\"chat.completion.chunk\",\"created\":1677652288,\"model\":\"mock-model\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
		flusher.Flush()

		fmt.Fprintf(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer server.Close()

	// Create a temp workspace directory
	tmpDir := t.TempDir()
	wsDir := filepath.Join(tmpDir, "workspace")
	require.NoError(t, os.MkdirAll(wsDir, 0o755))

	// Setup a basic phosphor.json configuration file in the workspace
	phosphorJson := fmt.Sprintf(`{
		"options": {
			"data_directory": ".phosphor",
			"disable_default_providers": true
		},
		"providers": {
			"mock": {
				"type": "openai",
				"base_url": "%s",
				"api_key": "mock-key",
				"models": [
					{
						"id": "mock-model",
						"name": "Mock Model",
						"context_window": 8192,
						"default_max_tokens": 1024
					}
				]
			}
		},
		"models": {
			"large": {
				"provider": "mock",
				"model": "mock-model"
			},
			"small": {
				"provider": "mock",
				"model": "mock-model"
			}
		}
	}`, server.URL)
	require.NoError(t, os.WriteFile(filepath.Join(wsDir, "phosphor.json"), []byte(phosphorJson), 0o644))

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	// Initialize the session
	h, err := client.NewSession(ctx, wsDir)
	require.NoError(t, err)
	defer h.Close()

	require.NotEmpty(t, h.ID)

	// Keep track of received events
	var (
		sessionStartReceived  bool
		sessionEndReceived    bool
		messageDeltaReceived  bool
		agentCompleteReceived bool
	)

	// Subscribe to events
	unsubStart, err := h.Subscribe(ctx, client.EventSessionStart, func(ev client.Event) {
		sessionStartReceived = true
		require.Equal(t, h.ID, ev.SessionID)
	})
	require.NoError(t, err)
	defer unsubStart()

	unsubEnd, err := h.Subscribe(ctx, client.EventSessionEnd, func(ev client.Event) {
		sessionEndReceived = true
		require.Equal(t, h.ID, ev.SessionID)
	})
	require.NoError(t, err)
	defer unsubEnd()

	unsubDelta, err := h.Subscribe(ctx, client.EventMessageDelta, func(ev client.Event) {
		messageDeltaReceived = true
		require.Equal(t, h.ID, ev.SessionID)
		require.NotEmpty(t, ev.Text)
	})
	require.NoError(t, err)
	defer unsubDelta()

	unsubComplete, err := h.Subscribe(ctx, client.EventAgentComplete, func(ev client.Event) {
		agentCompleteReceived = true
		require.Equal(t, h.ID, ev.SessionID)
	})
	require.NoError(t, err)
	defer unsubComplete()

	// Send a message
	var output bytes.Buffer
	err = h.SendMessage(ctx, "Hello Phosphor!", &output)
	require.NoError(t, err)

	// Wait briefly for asynchronous events to flush
	time.Sleep(200 * time.Millisecond)

	// Verify events were triggered
	require.True(t, sessionStartReceived, "EventSessionStart should have been received")
	require.True(t, sessionEndReceived, "EventSessionEnd should have been received")
	require.True(t, messageDeltaReceived, "EventMessageDelta should have been received")
	require.True(t, agentCompleteReceived, "EventAgentComplete should have been received")
}

func TestSessionWithCustomDB(t *testing.T) {
	// Enable mock providers for testing
	originalUseMock := config.UseMockProviders
	config.UseMockProviders = true
	defer func() {
		config.UseMockProviders = originalUseMock
		config.ResetProviders()
	}()
	config.ResetProviders()

	// Create a temp workspace directory
	tmpDir := t.TempDir()
	wsDir := filepath.Join(tmpDir, "workspace")
	require.NoError(t, os.MkdirAll(wsDir, 0o755))

	// Setup a basic phosphor.json configuration file in the workspace
	phosphorJson := `{
		"options": {
			"data_directory": ".phosphor",
			"disable_default_providers": true
		}
	}`
	require.NoError(t, os.WriteFile(filepath.Join(wsDir, "phosphor.json"), []byte(phosphorJson), 0o644))

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	// 1. Manually open a database connection
	dataDir := filepath.Join(wsDir, ".phosphor")
	dbConn, err := db.Connect(ctx, dataDir)
	require.NoError(t, err)

	// 2. Initialize the session passing the custom DB connection
	h, err := client.NewSession(ctx, wsDir, client.WithDB(dbConn))
	require.NoError(t, err)

	require.NotEmpty(t, h.ID)

	// 3. Close the handle, which should not release/close dbConn because it doesn't own it
	err = h.Close()
	require.NoError(t, err)

	// Test that database connection is still alive (e.g. Ping doesn't fail)
	err = dbConn.PingContext(ctx)
	require.NoError(t, err)

	// Clean up by releasing our own reference
	err = db.Release(dataDir)
	require.NoError(t, err)
}

func TestSessionWithSkipMigrations(t *testing.T) {
	// Enable mock providers for testing
	originalUseMock := config.UseMockProviders
	config.UseMockProviders = true
	defer func() {
		config.UseMockProviders = originalUseMock
		config.ResetProviders()
	}()
	config.ResetProviders()

	// Create a temp workspace directory
	tmpDir := t.TempDir()
	wsDir := filepath.Join(tmpDir, "workspace")
	require.NoError(t, os.MkdirAll(wsDir, 0o755))

	// Setup a basic phosphor.json configuration file in the workspace
	phosphorJson := `{
		"options": {
			"data_directory": ".phosphor",
			"disable_default_providers": true
		}
	}`
	require.NoError(t, os.WriteFile(filepath.Join(wsDir, "phosphor.json"), []byte(phosphorJson), 0o644))

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	// Connect manually with skipMigrations. This should NOT run migrations (e.g. no goose_db_version table).
	dataDir := filepath.Join(wsDir, ".phosphor")
	dbConn, err := db.Connect(ctx, dataDir, db.WithSkipMigrations(true))
	require.NoError(t, err)

	// Verify that goose_db_version table does not exist
	var tableName string
	err = dbConn.QueryRowContext(ctx, "SELECT name FROM sqlite_master WHERE type='table' AND name='goose_db_version'").Scan(&tableName)
	require.Error(t, err) // Should error (no rows found)

	// Close database reference
	err = db.Release(dataDir)
	require.NoError(t, err)
}

func TestSessionWithCustomDBName(t *testing.T) {
	// Enable mock providers for testing
	originalUseMock := config.UseMockProviders
	config.UseMockProviders = true
	defer func() {
		config.UseMockProviders = originalUseMock
		config.ResetProviders()
	}()
	config.ResetProviders()

	// Create a temp workspace directory
	tmpDir := t.TempDir()
	wsDir := filepath.Join(tmpDir, "workspace")
	require.NoError(t, os.MkdirAll(wsDir, 0o755))

	// Setup a basic phosphor.json configuration file in the workspace
	phosphorJson := `{
		"options": {
			"data_directory": ".phosphor",
			"disable_default_providers": true
		}
	}`
	require.NoError(t, os.WriteFile(filepath.Join(wsDir, "phosphor.json"), []byte(phosphorJson), 0o644))

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	// 1. Initialize session with custom db name
	h, err := client.NewSession(ctx, wsDir, client.WithDBName("custom_phosphor.db"))
	require.NoError(t, err)
	defer h.Close()

	// 2. Verify that .phosphor/custom_phosphor.db was actually created on disk
	dbPath := filepath.Join(wsDir, ".phosphor", "custom_phosphor.db")
	require.FileExists(t, dbPath)

	// 3. Verify that the default phosphor.db was NOT created
	defaultDBPath := filepath.Join(wsDir, ".phosphor", "phosphor.db")
	_, err = os.Stat(defaultDBPath)
	require.True(t, os.IsNotExist(err))
}
