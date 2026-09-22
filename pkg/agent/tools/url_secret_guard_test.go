package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"charm.land/fantasy"
	"github.com/hackafterdark/phosphor/pkg/secrets"
	"github.com/hackafterdark/phosphor/pkg/security/urlguard"
	"github.com/stretchr/testify/require"
)

// guardToken is a long, low-entropy value so the only reason a request carrying it
// can be refused is the known-value registry pass in urlguard.Check.
const guardToken = "Qm4bGu8TgQm4bGu8TgQm4bGu8TgQm4b"

func init() { secrets.Register(guardToken) }

func guardSessionCtx() context.Context {
	return context.WithValue(context.Background(), SessionIDContextKey, "url-guard-test-session")
}

func allowAllHosts(context.Context, string) (bool, error) { return true, nil }

func TestFetchTool_RefusesRegisteredSecretInQuery(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("server must not be dialed when the URL guard aborts")
	}))
	defer srv.Close()

	tool := NewFetchTool(&mockPermissionService{}, t.TempDir(), nil)
	input, err := json.Marshal(FetchParams{
		URL:    srv.URL + "/log?token=" + guardToken,
		Format: "text",
	})
	require.NoError(t, err)

	resp, err := tool.Run(guardSessionCtx(), fantasy.ToolCall{
		ID: "fetch-guard", Name: FetchToolName, Input: string(input),
	})
	require.NoError(t, err)
	require.True(t, resp.IsError, "fetch must abort, got: %s", resp.Content)
	require.Contains(t, resp.Content, urlguard.ErrMessage)
}

func TestFetchTool_AllowsCleanQuery(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	tool := NewFetchTool(&mockPermissionService{}, t.TempDir(), nil)
	input, err := json.Marshal(FetchParams{URL: srv.URL + "/search?q=hello&page=2", Format: "text"})
	require.NoError(t, err)

	resp, err := tool.Run(guardSessionCtx(), fantasy.ToolCall{
		ID: "fetch-clean", Name: FetchToolName, Input: string(input),
	})
	require.NoError(t, err)
	require.False(t, resp.IsError, "a clean URL must not be blocked, got: %s", resp.Content)
}

func TestWebFetchTool_RefusesRegisteredSecretInQuery(t *testing.T) {
	allowList = make(map[string]bool)

	tool := NewWebFetchTool(t.TempDir(), nil, allowAllHosts)
	input, err := json.Marshal(WebFetchParams{URL: "https://example.com/dl?sig=" + guardToken})
	require.NoError(t, err)

	resp, err := tool.Run(guardSessionCtx(), fantasy.ToolCall{
		ID: "web-guard", Name: WebFetchToolName, Input: string(input),
	})
	require.NoError(t, err)
	require.True(t, resp.IsError, "web_fetch must abort, got: %s", resp.Content)
	require.Contains(t, resp.Content, urlguard.ErrMessage)
}

// TestWebFetchTransport_RefusesSecretBeforeDialing checks the securityTransport layer
// itself, the path that would otherwise leak the credential onto the wire.
func TestWebFetchTransport_RefusesSecretBeforeDialing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("transport dialed despite the secret in the URL")
	}))
	defer srv.Close()

	client := &http.Client{Timeout: time.Second, Transport: newSecurityTransport(http.DefaultTransport, allowAllHosts, nil)}
	req, err := http.NewRequestWithContext(t.Context(), "GET", srv.URL+"/report?token="+guardToken, nil)
	require.NoError(t, err)

	resp, err := client.Do(req)
	require.Error(t, err, "the transport must refuse a URL carrying a known secret")
	require.ErrorContains(t, err, urlguard.ErrMessage)
	if resp != nil {
		resp.Body.Close()
	}
}

func TestWebFetchTransport_AllowsCleanRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	client := &http.Client{Timeout: time.Second, Transport: newSecurityTransport(http.DefaultTransport, allowAllHosts, nil)}
	req, err := http.NewRequestWithContext(t.Context(), "GET", srv.URL+"/report?view=list", nil)
	require.NoError(t, err)

	resp, err := client.Do(req)
	require.NoError(t, err, "a clean URL must pass the transport guard")
	resp.Body.Close()
}

func TestDownloadTool_RefusesRegisteredSecretInQuery(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("download server must not be dialed when the URL guard aborts")
	}))
	defer srv.Close()

	tool := NewDownloadTool(&mockPermissionService{}, t.TempDir(), nil)
	input, err := json.Marshal(DownloadParams{
		URL:      srv.URL + "/artifact.bin?token=" + guardToken,
		FilePath: "artifact.bin",
	})
	require.NoError(t, err)

	resp, err := tool.Run(guardSessionCtx(), fantasy.ToolCall{
		ID: "download-guard", Name: DownloadToolName, Input: string(input),
	})
	require.NoError(t, err)
	require.True(t, resp.IsError, "download must abort, got: %s", resp.Content)
	require.Contains(t, resp.Content, urlguard.ErrMessage)
}
