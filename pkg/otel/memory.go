package otel

import (
	"context"
	"sync"
	"time"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// Memory buffer defaults for the in-process span ring used by the
// phosphor_trace tool.
const (
	defaultMemoryBufferSpans = 100
	maxMemoryBufferSpans     = 500
	maxAttrValuesPerSpan     = 24
	maxAttrValueLen          = 120
)

// SpanSummary is a compact snapshot of one completed span, stored in the
// in-memory ring by memorySpanExporter. It is intentionally small: enough to
// reconstruct the shape and timing of a turn (name, nesting, duration,
// status, a few key attributes) without holding full attribute sets alive.
type SpanSummary struct {
	Name        string
	TraceID     string
	SpanID      string
	ParentID    string
	StartUnixMs int64
	DurationMs  float64
	Status      string
	Attributes  map[string]string
}

// memorySpanExporter is a fixed-capacity ring of completed-span summaries.
// It backs the phosphor_trace tool so an agent can inspect its own turn
// without a network round-trip to the OTLP collector.
type memorySpanExporter struct {
	mu       sync.Mutex
	ring     []SpanSummary
	next     int
	count    int
	sessions map[string]string // traceID -> conversation id
}

// newMemorySpanExporter builds the ring with the configured capacity,
// clamped to [1, maxMemoryBufferSpans]; a zero/non-positive requested size
// falls back to defaultMemoryBufferSpans.
func newMemorySpanExporter(capacity int) *memorySpanExporter {
	if capacity <= 0 {
		capacity = defaultMemoryBufferSpans
	}
	if capacity > maxMemoryBufferSpans {
		capacity = maxMemoryBufferSpans
	}
	return &memorySpanExporter{
		ring:     make([]SpanSummary, capacity),
		sessions: make(map[string]string, capacity),
	}
}

// ExportSpans implements [sdktrace.SpanExporter].
func (e *memorySpanExporter) ExportSpans(_ context.Context, spans []sdktrace.ReadOnlySpan) error {
	if e == nil || len(spans) == 0 {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, s := range spans {
		e.ring[e.next] = summarizeSpan(s)
		e.next = (e.next + 1) % len(e.ring)
		if e.count < len(e.ring) {
			e.count++
		}
		if conv := spanConversationID(s); conv != "" {
			e.sessions[s.SpanContext().TraceID().String()] = conv
		}
	}
	return nil
}

// Shutdown implements [sdktrace.SpanExporter]. The ring is plain memory with
// nothing to flush.
func (e *memorySpanExporter) Shutdown(_ context.Context) error { return nil }

// summarizeSpan converts one completed span into its compact form, capping
// the number and length of attributes so the ring stays bounded in bytes as
// well as entries.
func summarizeSpan(s sdktrace.ReadOnlySpan) SpanSummary {
	sum := SpanSummary{
		Name:        s.Name(),
		TraceID:     s.SpanContext().TraceID().String(),
		SpanID:      s.SpanContext().SpanID().String(),
		StartUnixMs: s.StartTime().UnixMilli(),
		DurationMs:  float64(s.EndTime().Sub(s.StartTime())) / float64(time.Millisecond),
	}
	if p := s.Parent(); p.HasSpanID() {
		sum.ParentID = p.SpanID().String()
	}
	if st := s.Status(); st.Code != 0 || st.Description != "" {
		sum.Status = st.Code.String()
		if st.Description != "" {
			sum.Status = st.Code.String() + ": " + st.Description
		}
	}
	if n := len(s.Attributes()); n > 0 {
		sum.Attributes = make(map[string]string, min(n, maxAttrValuesPerSpan))
		for i, kv := range s.Attributes() {
			if i >= maxAttrValuesPerSpan {
				break
			}
			v := kv.Value.String()
			if len(v) > maxAttrValueLen {
				v = v[:maxAttrValueLen]
			}
			sum.Attributes[string(kv.Key)] = v
		}
	}
	return sum
}

// spanConversationID pulls the GenAI conversation id off a span, if present.
func spanConversationID(s sdktrace.ReadOnlySpan) string {
	for _, kv := range s.Attributes() {
		if kv.Key == genAIAttrKeys.ConversationID {
			return kv.Value.String()
		}
	}
	return ""
}

// memoryBuffer is the process-wide ring, installed by [Init] when
// Observability.MemoryBuffer is set. It is nil when the buffer is off, so
// GetSpanSummaries stays zero-cost on the default path.
var memoryBuffer *memorySpanExporter

// GetSpanSummaries returns the buffered span snapshots in completion order
// (oldest first). When sessionID is non-empty, only spans whose trace carries
// that gen_ai.conversation.id are returned; limit caps the tail length
// (default 40, max 100). Returns nil when the buffer is disabled.
func GetSpanSummaries(sessionID string, limit int) []SpanSummary {
	if memoryBuffer == nil {
		return nil
	}
	if limit <= 0 {
		limit = 40
	}
	if limit > 100 {
		limit = 100
	}
	memoryBuffer.mu.Lock()
	defer memoryBuffer.mu.Unlock()

	cap := len(memoryBuffer.ring)
	start := 0
	if memoryBuffer.count == cap {
		start = memoryBuffer.next
	}
	out := make([]SpanSummary, 0, memoryBuffer.count)
	for i := range memoryBuffer.count {
		s := memoryBuffer.ring[(start+i)%cap]
		if sessionID != "" {
			// Prefer the trace-level conversation map (filled from the span
			// that carried the conversation id when it ended); fall back to
			// the summary"s own attribute so filtered queries still match
			// when the root span aged out of the map"s window or the ring.
			conv, ok := memoryBuffer.sessions[s.TraceID]
			if !ok {
				conv = s.Attributes[string(genAIAttrKeys.ConversationID)]
			}
			if conv != sessionID {
				continue
			}
		}
		out = append(out, s)
	}
	if len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out

}
