package log

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
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
// A provider request has three distinct waiting phases, and each needs a
// different kind of bound because "the server is quiet" means something
// different in each one. Getting them wrong is worse than having none at all:
// too tight a deadline aborts a healthy request and the agent re-runs the whole
// turn, so every budget here is sized so it can only ever fire on a genuinely
// stuck connection.
//
//  1. Dial (TCP + TLS). On a listening backend this is milliseconds. A backend
//     that is simply not listening is caught by the connect timeout; there is
//     no scenario where dialing legitimately takes tens of seconds, so this
//     bound can never cut off a working request.
//  2. Time to first byte: from "request fully sent" to "server begins the
//     response." Prefill and request-queueing both live here, and a huge prompt
//     on a busy single GPU (or a request sitting behind other traffic in a
//     continuous-batching scheduler) can legitimately sit in this phase for
//     tens of seconds to minutes while perfectly alive. So the only honest
//     budget for phase 2 is generous - big enough that reaching it means the
//     backend is wedged, not merely slow. This is the one phase where "dead"
//     and "slow" look identical from bytes alone, so we bias hard toward not
//     cutting it off.
//  3. Inter-chunk idle: the gap *between* bytes once bytes have begun. Once a
//     stream is producing tokens they arrive continuously, so a gap of minutes
//     here means the connection went half-open (server crashed after accept, a
//     proxy idle-killed a pooled connection, network drop with no FIN/RST) and
//     neither end notices. This is the trustworthy stall signal and the one the
//     original hang actually needed a guard for: the old code had no deadline at
//     all, so a half-open socket blocked the agent loop forever and no
//     RunComplete was ever published.
//
// Because the phase-3 timer is rearmed on every byte, an actively streaming
// response - however long - is never truncated; only a stalled one is.
//
// All three budgets are process-wide and env-overridable (Go duration strings).
// Raise the first-byte budget if your backend queues long:
// PHOSPHOR_PROVIDER_CONNECT_TIMEOUT (default 30s)
// PHOSPHOR_PROVIDER_FIRST_BYTE_TIMEOUT  (default 10m)
// PHOSPHOR_PROVIDER_STREAM_IDLE_TIMEOUT (default 2m)

const (
	// DefaultProviderConnectTimeout bounds establishing the connection.
	DefaultProviderConnectTimeout = 30 * time.Second
	// DefaultProviderFirstByteTimeout bounds the prefill + queue wait. Deliberately
	// huge: cutting a request mid-prefill is worse than waiting on it, so this
	// only trips on a backend that is truly wedged before ever responding.
	DefaultProviderFirstByteTimeout = 10 * time.Minute
	// DefaultProviderStreamIdleTimeout bounds the gap between streamed bytes - the
	// real stall detector. Modest, but still far above any genuine inter-token gap
	// on a live model.
	DefaultProviderStreamIdleTimeout = 2 * time.Minute
)

func providerConnectTimeout() time.Duration {
	return envDuration("PHOSPHOR_PROVIDER_CONNECT_TIMEOUT", DefaultProviderConnectTimeout)
}

func providerFirstByteTimeout() time.Duration {
	return envDuration("PHOSPHOR_PROVIDER_FIRST_BYTE_TIMEOUT", DefaultProviderFirstByteTimeout)
}

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
// transport. It is the single place provider network behavior is configured so a
// genuinely stuck request surfaces as a provider error (which the agent retries
// or reports, letting the turn finish) instead of hanging forever. When debug is
// set the requests are additionally logged, nesting the logger over the
// hardened transport.
func NewProviderHTTPClient(debug bool) *http.Client {
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		base = &http.Transport{}
	}
	connect := providerConnectTimeout()
	firstByte := providerFirstByteTimeout()
	idle := providerStreamIdleTimeout()

	t := base.Clone()
	// Phase 1: dialing is never legitimately slow, so a short budget is safe.
	// Set DialContext (it takes precedence over Dial when present) so the
	// connect timeout actually applies on a cloned default transport.
	dialer := &net.Dialer{Timeout: connect}
	t.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		return dialer.DialContext(ctx, network, addr)
	}
	// Phase 2 has two sub-cases and both must be governed by the same generous
	// first-byte budget, never a tighter one:
	//   - a backend that buffers until the first token sends its response
	//     headers *with* that token, so the header wait IS the prefill/queue wait;
	//   - a wedged backend that accepts the connection and never sends headers at
	//     all. Our body watchdog can only start once RoundTrip returns (headers
	//     in hand), so it cannot cover this case - the header timeout must, or the
	//     old infinite hang comes right back.
	// Using the first-byte value for both means a legitimately slow prefill is
	// never cut, while a truly stuck connection is still bounded.
	t.ResponseHeaderTimeout = firstByte

	rt := http.RoundTripper(&idleStreamTransport{base: t, firstByte: firstByte, idle: idle})
	if debug {
		rt = &HTTPRoundTripLogger{Transport: rt}
	}

	return &http.Client{Transport: rt}
}

// errStreamIdle reports that the read watchdog gave up on a stream that stopped
// producing bytes, so a blocked reader sees a cause it can attribute rather than
// a bare context cancellation.
var errStreamIdle = errors.New("phosphor: provider stream stalled (no bytes within the idle window)")

// idleStreamTransport wraps a response body with the phase-2/3 read watchdog.
// It does not touch the connect path (phase 1 is on the transport).
type idleStreamTransport struct {
	base      http.RoundTripper
	firstByte time.Duration
	idle      time.Duration
}

func (t *idleStreamTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx, cancel := context.WithCancel(req.Context())
	req = req.WithContext(ctx)

	resp, err := t.base.RoundTrip(req)
	if err != nil {
		cancel()
		return nil, err
	}
	resp.Body = &idleTimeoutBody{
		ReadCloser: resp.Body,
		cancel:     cancel,
		firstByte:  t.firstByte,
		idle:       t.idle,
	}
	return resp, nil
}

// idleTimeoutBody is the phase-2/3 watchdog. The timer is armed before every
// Read and disarmed once that Read returns, so it measures the quiet gap with no
// data rather than the total stream duration. The first Read (no byte seen yet)
// gets the generous first-byte budget; once bytes start flowing the budget drops
// to the tighter inter-chunk idle window. A live stream therefore never trips
// either way no matter how long it runs; only a stalled one does.
type idleTimeoutBody struct {
	io.ReadCloser
	cancel    context.CancelFunc
	firstByte time.Duration
	idle      time.Duration
	timer     *time.Timer
	sawFirst  bool
	closed    bool
	mu        sync.Mutex
}

func (b *idleTimeoutBody) Read(p []byte) (int, error) {
	window := b.idle
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return 0, errStreamIdle
	}
	if !b.sawFirst {
		window = b.firstByte
	}
	if b.timer == nil {
		b.timer = time.AfterFunc(window, b.cancel)
	} else {
		b.timer.Reset(window)
	}
	b.mu.Unlock()

	n, err := b.ReadCloser.Read(p)

	b.mu.Lock()
	if b.timer != nil {
		b.timer.Stop()
	}
	if n > 0 && !b.sawFirst {
		b.sawFirst = true
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
