package log

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// NewHTTPClient creates an HTTP client with debug logging enabled when debug mode is on.
func NewHTTPClient() *http.Client {
	return &http.Client{
		Transport: &HTTPRoundTripLogger{
			Transport: http.DefaultTransport,
		},
	}
}

// HTTPRoundTripLogger is an http.RoundTripper that logs requests and responses.
type HTTPRoundTripLogger struct {
	Transport http.RoundTripper
}

// RoundTrip implements http.RoundTripper interface with logging.
func (h *HTTPRoundTripLogger) RoundTrip(req *http.Request) (*http.Response, error) {
	var err error
	var save io.ReadCloser
	save, req.Body, err = drainBody(req.Body)
	if err != nil {
		slog.Error(
			"HTTP request failed",
			"method", req.Method,
			"url", req.URL,
			"error", err,
		)
		return nil, err
	}

	if slog.Default().Enabled(req.Context(), slog.LevelDebug) {
		slog.Debug(
			"HTTP Request",
			"method", req.Method,
			"url", req.URL,
			"body", bodyToString(save),
		)
	}

	start := time.Now()
	resp, err := h.Transport.RoundTrip(req)
	duration := time.Since(start)
	if err != nil {
		slog.Error(
			"HTTP request failed",
			"method", req.Method,
			"url", req.URL,
			"duration_ms", duration.Milliseconds(),
			"error", err,
		)
		return resp, err
	}

	save, resp.Body, err = drainBody(resp.Body)
	if err != nil {
		slog.Error("Failed to drain response body", "error", err)
		return resp, err
	}
	if slog.Default().Enabled(req.Context(), slog.LevelDebug) {
		slog.Debug(
			"HTTP Response",
			"status_code", resp.StatusCode,
			"status", resp.Status,
			"headers", formatHeaders(resp.Header),
			"body", bodyToString(save),
			"content_length", resp.ContentLength,
			"duration_ms", duration.Milliseconds(),
		)
	}
	return resp, nil
}

func bodyToString(body io.ReadCloser) string {
	if body == nil {
		return ""
	}
	src, err := io.ReadAll(body)
	if err != nil {
		slog.Error("Failed to read body", "error", err)
		return ""
	}
	var b bytes.Buffer
	if json.Indent(&b, bytes.TrimSpace(src), "", "  ") != nil {
		// not json probably
		return string(src)
	}
	return b.String()
}

// formatHeaders formats HTTP headers for logging, filtering out sensitive information.
func formatHeaders(headers http.Header) map[string][]string {
	filtered := make(map[string][]string)
	for key, values := range headers {
		lowerKey := strings.ToLower(key)
		// Filter out sensitive headers
		if strings.Contains(lowerKey, "authorization") ||
			strings.Contains(lowerKey, "api-key") ||
			strings.Contains(lowerKey, "token") ||
			strings.Contains(lowerKey, "secret") {
			filtered[key] = []string{"[REDACTED]"}
		} else {
			filtered[key] = values
		}
	}
	return filtered
}

func drainBody(b io.ReadCloser) (r1, r2 io.ReadCloser, err error) {
	if b == nil || b == http.NoBody {
		return http.NoBody, http.NoBody, nil
	}
	var buf bytes.Buffer
	if _, err = buf.ReadFrom(b); err != nil {
		return nil, b, err
	}
	if err = b.Close(); err != nil {
		return nil, b, err
	}
	return io.NopCloser(&buf), io.NopCloser(bytes.NewReader(buf.Bytes())), nil
}

//
// Provider transport hardening.
//
// A provider request can be accepted at the TCP level and then go silent: a
// local/queueing backend (e.g. vLLM) may hold the connection without ever
// sending response headers, or open the SSE stream and then stop emitting
// bytes. Go's default http.Transport has no response-header deadline and the
// agent run context carries no deadline, so such a stall blocks the whole
// agent loop forever - the run never finishes, no RunComplete is published,
// and the TUI spinner counts up indefinitely. The transport below bounds both
// failure modes without truncating a healthy stream:
//
//   - ResponseHeaderTimeout: a hard ceiling on the wait for the first
//     response headers (catches "accepted, never answered").
//   - an idle-stream watchdog on the response body: the request is cancelled
//     only if *no bytes at all* arrive for StreamIdleTimeout. Because the
//     timer is rearmed on every Read, an actively streaming response is never
//     cut off no matter how long it runs; only a genuinely stalled one is.
//
// The timeouts are process-wide and conservative; override with
// PHOSPHOR_PROVIDER_HEADER_TIMEOUT / PHOSPHOR_PROVIDER_STREAM_IDLE_TIMEOUT
// (Go duration strings) if a backend legitimately needs longer.

const (
	// DefaultProviderHeaderTimeout bounds the wait for response headers.
	DefaultProviderHeaderTimeout = 60 * time.Second
	// DefaultProviderStreamIdleTimeout bounds the gap between streamed bytes.
	// Generous on purpose: a reasoning model can pause between chunks, so we
	// only fire on a real stall, not on a slow-but-alive one.
	DefaultProviderStreamIdleTimeout = 5 * time.Minute
)

// providerHeaderTimeout reads the response-header ceiling, honoring the env
// override and falling back to the default for empty/invalid values.
func providerHeaderTimeout() time.Duration {
	return envDuration("PHOSPHOR_PROVIDER_HEADER_TIMEOUT", DefaultProviderHeaderTimeout)
}

// providerStreamIdleTimeout reads the stream idle ceiling, honoring the env
// override and falling back to the default for empty/invalid values.
func providerStreamIdleTimeout() time.Duration {
	return envDuration("PHOSPHOR_PROVIDER_STREAM_IDLE_TIMEOUT", DefaultProviderStreamIdleTimeout)
}

// envDuration parses a Go duration from an env var, returning def when unset
// or unparseable so a typo can never disable the guard entirely.
func envDuration(key string, def time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return def
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		slog.Warn("Invalid provider timeout override, using default",
			"var", key, "value", raw, "default", def.String())
		return def
	}
	return d
}

// NewProviderHTTPClient builds the http.Client handed to every LLM provider
// transport. It is the single place provider network behavior is configured so
// a stalled backend surfaces as a provider error (which the agent retries or
// reports) instead of hanging the turn forever. When debug is set the requests
// are additionally logged, nesting the logger over the hardened transport.
func NewProviderHTTPClient(debug bool) *http.Client {
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		base = &http.Transport{}
	}
	t := base.Clone()
	t.ResponseHeaderTimeout = providerHeaderTimeout()

	rt := http.RoundTripper(&idleStreamTransport{base: t, idle: providerStreamIdleTimeout()})
	if debug {
		rt = &HTTPRoundTripLogger{Transport: rt}
	}

	return &http.Client{Transport: rt}
}

// errStreamIdle is returned once the read watchdog has cancelled a stalled
// response, so a blocked reader sees a clear cause rather than a bare context
// cancellation it cannot attribute.
var errStreamIdle = errors.New("phosphor: provider stream idle timeout (no bytes received within the read window)")

// idleStreamTransport arms a read-idle watchdog around each response body.
type idleStreamTransport struct {
	base http.RoundTripper
	idle time.Duration
}

func (t *idleStreamTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx, cancel := context.WithCancel(req.Context())
	req = req.WithContext(ctx)

	resp, err := t.base.RoundTrip(req)
	if err != nil {
		cancel()
		return nil, err
	}
	resp.Body = &idleTimeoutBody{ReadCloser: resp.Body, cancel: cancel, idle: t.idle}
	return resp, nil
}

// idleTimeoutBody cancels the request when no bytes are produced within the
// idle window. The timer is (re)armed before every Read and disarmed once the
// Read returns, so the watchdog measures the gap with no data rather than the
// total stream duration.
type idleTimeoutBody struct {
	io.ReadCloser
	cancel context.CancelFunc
	idle   time.Duration
	timer  *time.Timer
	closed bool
	mu     sync.Mutex
}

func (b *idleTimeoutBody) Read(p []byte) (int, error) {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return 0, errStreamIdle
	}
	if b.timer == nil {
		b.timer = time.AfterFunc(b.idle, b.cancel)
	} else {
		b.timer.Reset(b.idle)
	}
	b.mu.Unlock()

	n, err := b.ReadCloser.Read(p)

	b.mu.Lock()
	if b.timer != nil {
		b.timer.Stop()
	}
	b.mu.Unlock()
	return n, err
}

func (b *idleTimeoutBody) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil
	}
	b.closed = true
	if b.timer != nil {
		b.timer.Stop()
	}
	b.cancel()
	return b.ReadCloser.Close()
}
