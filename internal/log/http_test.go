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

// TestProviderStreamStallIsCaught covers the reported hang: a backend that opens
// the stream, emits one chunk, then falls silent. The inter-chunk idle watchdog
// must cancel the request so the caller gets an error instead of blocking forever
// on a stream that will never advance.
func TestProviderStreamStallIsCaught(t *testing.T) {
	t.Setenv("PHOSPHOR_PROVIDER_CONNECT_TIMEOUT", "5s")
	t.Setenv("PHOSPHOR_PROVIDER_FIRST_BYTE_TIMEOUT", "10s")
	t.Setenv("PHOSPHOR_PROVIDER_STREAM_IDLE_TIMEOUT", "150ms")

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

	// The follow-on read sees only silence, so the idle watchdog must fail it
	// fast rather than hang the way an unbounded transport would.
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

// TestProviderSlowFirstByteSurvivesTinyIdleWindow is the false-positive guard
// the whole design turns on. A backend that spends a long time before its first
// byte (prefill + queueing) is *alive*, not stalled - so that first gap must be
// governed by the generous first-byte budget, never by the tight inter-chunk idle
// window. We force the two budgets far apart (idle 300ms, first-byte 5s) and make
// the server pause ~1.5s before its first byte: if the first gap wrongly used the
// idle window the request would die at 300ms. It must instead complete.
func TestProviderSlowFirstByteSurvivesTinyIdleWindow(t *testing.T) {
	t.Setenv("PHOSPHOR_PROVIDER_CONNECT_TIMEOUT", "5s")
	t.Setenv("PHOSPHOR_PROVIDER_FIRST_BYTE_TIMEOUT", "5s")
	t.Setenv("PHOSPHOR_PROVIDER_STREAM_IDLE_TIMEOUT", "300ms")

	const preFirstByte = 1500 * time.Millisecond
	chunks := 8
	url := startTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(preFirstByte) // emulate prefill / time in the scheduler queue
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fl := w.(http.Flusher)
		for i := range chunks {
			fmt.Fprintf(w, "data: chunk-%d\n\n", i)
			fl.Flush()
			time.Sleep(60 * time.Millisecond)
		}
	})

	client := NewProviderHTTPClient(false)
	resp, err := client.Get(url)
	require.NoError(t, err)
	defer resp.Body.Close()

	got, err := io.ReadAll(resp.Body)
	require.NoError(t, err, "a slow-but-alive first byte must not be cut off by the idle window")
	require.Contains(t, string(got), "chunk-0")
	require.Contains(t, string(got), fmt.Sprintf("chunk-%d", chunks-1))
}

// TestProviderLongStreamNotTruncated proves a stream that runs far past the
// inter-chunk idle window still completes in full, as long as its bytes keep
// coming. A long reasoning generation must never be truncated for taking a while.
func TestProviderLongStreamNotTruncated(t *testing.T) {
	t.Setenv("PHOSPHOR_PROVIDER_CONNECT_TIMEOUT", "5s")
	t.Setenv("PHOSPHOR_PROVIDER_FIRST_BYTE_TIMEOUT", "5s")
	t.Setenv("PHOSPHOR_PROVIDER_STREAM_IDLE_TIMEOUT", "1s")

	// Total stream time (~3s) is far above the 1s idle budget; gaps stay under it.
	chunks := 60
	url := startTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fl := w.(http.Flusher)
		for i := range chunks {
			fmt.Fprintf(w, "data: chunk-%d\n\n", i)
			fl.Flush()
			time.Sleep(50 * time.Millisecond)
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
	require.NotContains(t, string(got), "chunk-99")
}

// TestProviderWedgedBeforeHeadersIsBounded covers a backend that accepts the TCP
// connection but never produces response headers at all. The body watchdog cannot
// see this (it only starts once headers arrive), so the response-header timeout
// must bound it - proving the first-byte budget really does cap that sub-case.
func TestProviderWedgedBeforeHeadersIsBounded(t *testing.T) {
	t.Setenv("PHOSPHOR_PROVIDER_CONNECT_TIMEOUT", "5s")
	t.Setenv("PHOSPHOR_PROVIDER_FIRST_BYTE_TIMEOUT", "400ms")
	t.Setenv("PHOSPHOR_PROVIDER_STREAM_IDLE_TIMEOUT", "30s")

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
	require.Less(t, time.Since(start), 5*time.Second, "request must fail once the first-byte deadline passes")
}

// TestProviderDialTimeoutOnDeadHost proves the connect budget bounds a backend
// that is simply unreachable, independent of the stream budgets.
func TestProviderDialTimeoutOnDeadHost(t *testing.T) {
	t.Setenv("PHOSPHOR_PROVIDER_CONNECT_TIMEOUT", "200ms")
	t.Setenv("PHOSPHOR_PROVIDER_FIRST_BYTE_TIMEOUT", "30s")
	t.Setenv("PHOSPHOR_PROVIDER_STREAM_IDLE_TIMEOUT", "30s")

	client := NewProviderHTTPClient(false)
	start := time.Now()
	// 240.0.0.1 is TEST-NET-1, non-routable: dialing never completes.
	resp, err := client.Get("http://240.0.0.1:9/completion")
	if err == nil {
		_ = resp.Body.Close()
	}
	require.Error(t, err, "an unreachable host must fail, not hang")
	require.Less(t, time.Since(start), 10*time.Second)
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
