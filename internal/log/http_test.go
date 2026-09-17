package log

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// startTestServer runs handler on a fresh loopback http.Server and returns its
// base URL. It exists so the tests can drive flushing and stalls directly with
// the standard library rather than depend on a response builder.
func startTestServer(t *testing.T, handler http.HandlerFunc) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	srv := &http.Server{Handler: handler}
	go srv.Serve(ln)
	t.Cleanup(func() { _ = srv.Close() })
	return "http://" + ln.Addr().String()
}

func TestHTTPRoundTripLogger(t *testing.T) {
	url := startTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Custom-Header", "test-value")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error": "Internal server error", "code": 500}`))
	})

	client := NewHTTPClient()

	req, err := http.NewRequestWithContext(
		t.Context(),
		http.MethodPost,
		url,
		strings.NewReader(`{"test": "data"}`),
	)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer secret-token")

	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusInternalServerError, resp.StatusCode)
}

func TestFormatHeaders(t *testing.T) {
	headers := http.Header{
		"Content-Type":  []string{"application/json"},
		"Authorization": []string{"Bearer secret-token"},
		"X-API-Key":     []string{"api-key-123"},
		"User-Agent":    []string{"test-agent"},
	}

	formatted := formatHeaders(headers)

	// Sensitive headers are redacted.
	require.Equal(t, "[REDACTED]", formatted["Authorization"][0])
	require.Equal(t, "[REDACTED]", formatted["X-API-Key"][0])

	// Non-sensitive headers are preserved.
	require.Equal(t, "application/json", formatted["Content-Type"][0])
	require.Equal(t, "test-agent", formatted["User-Agent"][0])
}

// TestProviderHTTPClientIdleTimeoutOnStalledStream covers the reported hang: a
// backend that opens the stream, emits one chunk, then falls silent. The read
// watchdog must cancel the request so the caller gets an error instead of
// blocking forever on a stream that will never advance.
func TestProviderHTTPClientIdleTimeoutOnStalledStream(t *testing.T) {
	t.Setenv("PHOSPHOR_PROVIDER_STREAM_IDLE_TIMEOUT", "150ms")
	t.Setenv("PHOSPHOR_PROVIDER_HEADER_TIMEOUT", "10s")

	release := make(chan struct{})
	t.Cleanup(func() { close(release) })

	url := startTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "data: partial\n\n")
		w.(http.Flusher).Flush()
		<-release // hold the stream open, emitting nothing further
	})

	client := NewProviderHTTPClient(false)
	resp, err := client.Get(url)
	require.NoError(t, err)
	defer resp.Body.Close()

	// The first chunk is delivered normally.
	buf := make([]byte, 256)
	n, err := resp.Body.Read(buf)
	require.NoError(t, err)
	require.Contains(t, string(buf[:n]), "partial")

	// The follow-on read sees only silence, so the watchdog must fail it fast
	// rather than hang the way the default transport would.
	errCh := make(chan error, 1)
	go func() {
		_, err := resp.Body.Read(buf)
		errCh <- err
	}()

	select {
	case err := <-errCh:
		require.Error(t, err, "a stalled stream must surface an error, not hang")
	case <-time.After(3 * time.Second):
		t.Fatal("stalled stream did not time out; the read hung")
	}
}

// TestProviderHTTPClientDoesNotCutOffActiveStream guards the other side of the
// same feature: a slow-but-alive stream (bytes keep trickling in faster than the
// idle window) must run to completion untouched, so a long reasoning response is
// never truncated just because it took a while overall.
func TestProviderHTTPClientDoesNotCutOffActiveStream(t *testing.T) {
	t.Setenv("PHOSPHOR_PROVIDER_STREAM_IDLE_TIMEOUT", "2s")
	t.Setenv("PHOSPHOR_PROVIDER_HEADER_TIMEOUT", "10s")

	chunks := 25
	url := startTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fl := w.(http.Flusher)
		for i := range chunks {
			fmt.Fprintf(w, "data: chunk-%d\n\n", i)
			fl.Flush()
			time.Sleep(40 * time.Millisecond)
		}
	})

	client := NewProviderHTTPClient(true) // debug on also drives the logging wrapper
	resp, err := client.Get(url)
	require.NoError(t, err)
	defer resp.Body.Close()

	got, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Contains(t, string(got), "chunk-0")
	require.Contains(t, string(got), fmt.Sprintf("chunk-%d", chunks-1))
}

// TestProviderHTTPClientBoundsHeaderWait covers the "accepted the TCP
// connection, never answered" stall: the response-header timeout must abort the
// request quickly instead of waiting on headers that never come.
func TestProviderHTTPClientBoundsHeaderWait(t *testing.T) {
	t.Setenv("PHOSPHOR_PROVIDER_STREAM_IDLE_TIMEOUT", "30s")
	t.Setenv("PHOSPHOR_PROVIDER_HEADER_TIMEOUT", "150ms")

	release := make(chan struct{})
	t.Cleanup(func() { close(release) })

	// The handler never writes, so no response headers ever reach the client.
	url := startTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		<-release
	})

	client := NewProviderHTTPClient(false)
	start := time.Now()
	resp, err := client.Get(url)
	if err == nil {
		_ = resp.Body.Close()
	}
	require.Error(t, err, "a server that never sends headers must not be waited on forever")
	require.Less(t, time.Since(start), 5*time.Second, "request must fail promptly once the header deadline passes")
}

// TestEnvDurationRejectsBadValues keeps a malformed override from disabling the
// guard: an invalid or non-positive value must fall back to the default.
func TestEnvDurationRejectsBadValues(t *testing.T) {
	def := 7 * time.Second

	t.Setenv("PHOSPHOR_TEST_DUR", "")
	require.Equal(t, def, envDuration("PHOSPHOR_TEST_DUR", def))

	t.Setenv("PHOSPHOR_TEST_DUR", "not-a-duration")
	require.Equal(t, def, envDuration("PHOSPHOR_TEST_DUR", def))

	t.Setenv("PHOSPHOR_TEST_DUR", "-5s")
	require.Equal(t, def, envDuration("PHOSPHOR_TEST_DUR", def))

	t.Setenv("PHOSPHOR_TEST_DUR", "2s")
	require.Equal(t, 2*time.Second, envDuration("PHOSPHOR_TEST_DUR", def))
}
