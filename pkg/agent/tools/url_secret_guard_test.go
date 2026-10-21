package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"charm.land/fantasy"
	"github.com/hackafterdark/phosphor/pkg/egress"
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
// TestSecuredHTTPClientFailsClosedOnUnknownHost pins finding #6: a client
// built with no prompt handler must refuse raw-IP and unknown-hostname
// destinations instead of dialing them, so no network tool can reach the
// wire through an unsecured client.
func TestSecuredHTTPClientFailsClosedOnUnknownHost(t *testing.T) {
	t.Parallel()

	client := NewSecuredHTTPClient(nil, nil)

	t.Run("unknown hostname is blocked", func(t *testing.T) {
		_, err := client.Get("https://trusted.example.com/")
		require.Error(t, err)
		require.Contains(t, err.Error(), "no prompt handler configured")
	})

	t.Run("public raw IP is blocked", func(t *testing.T) {
		_, err := client.Get("http://8.8.8.8/")
		require.Error(t, err)
		require.Contains(t, err.Error(), "raw IP")
	})

	t.Run("bracketed IPv6 literal is blocked", func(t *testing.T) {
		_, err := client.Get("http://[::2]:6379/")
		require.Error(t, err)
	})
}

// TestFetchDownloadToolsRouteThroughSecuredClient verifies the fetch and
// download tools refuse an unapproved host even when the tool permission
// check passed — the permission gate and the transport trust gate are
// independent.
func TestFetchDownloadToolsRouteThroughSecuredClient(t *testing.T) {
	perm := &mockPermissionService{}
	workingDir := t.TempDir()

	fetchTool := NewFetchTool(perm, workingDir, nil)
	input, err := json.Marshal(FetchParams{URL: "https://trusted.example.com/x", Format: "text"})
	require.NoError(t, err)
	_, err = fetchTool.Run(guardSessionCtx(), fantasy.ToolCall{
		ID: "fetch-trust", Name: FetchToolName, Input: string(input),
	})
	require.Error(t, err, "fetch must fail closed on an unapproved host")
	require.Contains(t, err.Error(), "no prompt handler configured")

	dlTool := NewDownloadTool(perm, workingDir, nil)
	dlInput, err := json.Marshal(DownloadParams{URL: "https://trusted.example.com/x", FilePath: "x.bin"})
	require.NoError(t, err)
	_, err = dlTool.Run(guardSessionCtx(), fantasy.ToolCall{
		ID: "dl-trust", Name: DownloadToolName, Input: string(dlInput),
	})
	require.Error(t, err, "download must fail closed on an unapproved host")
	require.Contains(t, err.Error(), "no prompt handler configured")
}

// TestWrapClientRecognizesSecuredClient pins that egress.WrapClient does not
// double-wrap the secured client handed to the tools by the coordinator.
func TestWrapClientRecognizesSecuredClient(t *testing.T) {
	t.Parallel()

	client := NewSecuredHTTPClient(nil, nil)
	wrapped := egress.WrapClient(client)
	require.Same(t, client, wrapped, "secured client must pass through WrapClient unchanged")
}