package tools

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"charm.land/fantasy"
	"github.com/stretchr/testify/require"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func runFetchAgainst(t *testing.T, transport http.RoundTripper) (fantasy.ToolResponse, error) {
	t.Helper()
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")
	tool := NewFetchTool(&mockPermissionService{}, t.TempDir(), nil, &http.Client{Transport: transport})
	input, err := json.Marshal(FetchParams{URL: "https://example.com/x"})
	require.NoError(t, err)
	return tool.Run(ctx, fantasy.ToolCall{
		ID:    "fetch-test",
		Name:  FetchToolName,
		Input: string(input),
	})
}

func TestFetchTool_HostBlockedIsRecoverableToolError(t *testing.T) {
	t.Parallel()

	// A security refusal (host trust, urlguard) must surface to the model
	// as a tool error, never as a Go error that aborts the agent stream.
	resp, err := runFetchAgainst(t, roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return nil, ErrHostBlocked
	}))
	require.NoError(t, err, "a blocked host must not abort the agent stream")
	require.True(t, resp.IsError, "the block must surface to the model as a tool error")
}

func TestFetchTool_TransportFailureIsRecoverableToolError(t *testing.T) {
	t.Parallel()

	resp, err := runFetchAgainst(t, roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("dial tcp: connection reset")
	}))
	require.NoError(t, err)
	require.True(t, resp.IsError)
}
