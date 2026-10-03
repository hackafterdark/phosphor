package otel

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMemorySpanExporterRingEviction(t *testing.T) {
	t.Parallel()

	mem := newMemorySpanExporter(2)
	summaries := []SpanSummary{
		{Name: "a", SpanID: "1", TraceID: "t1"},
		{Name: "b", SpanID: "2", TraceID: "t1"},
		{Name: "c", SpanID: "3", TraceID: "t1"},
	}
	for _, s := range summaries {
		mem.ring[mem.next] = s
		mem.next = (mem.next + 1) % len(mem.ring)
		if mem.count < len(mem.ring) {
			mem.count++
		}
	}

	// A ring of 2 must hold only the two most recent, oldest first.
	got := drain(mem)
	require.Len(t, got, 2)
	require.Equal(t, "b", got[0].Name)
	require.Equal(t, "c", got[1].Name)
}

func TestMemorySpanExporterCapacityClamps(t *testing.T) {
	t.Parallel()

	require.Len(t, newMemorySpanExporter(0).ring, defaultMemoryBufferSpans, "zero falls back to the default")
	require.Len(t, newMemorySpanExporter(10_000).ring, maxMemoryBufferSpans, "oversized requests clamp to the max")
}

func TestMemoryExporterExportSpansFillsRing(t *testing.T) {
	t.Parallel()

	mem := newMemorySpanExporter(3)
	require.NoError(t, mem.ExportSpans(context.Background(), nil))
	require.Zero(t, mem.count, "an empty batch is a no-op")
}

func TestGetSpanSummariesDisabled(t *testing.T) {
	t.Parallel()

	require.Nil(t, GetSpanSummaries("", 10), "a disabled buffer returns nothing")
}

func TestGetSpanSummariesSessionFilterAndLimit(t *testing.T) {
	t.Parallel()

	prev := memoryBuffer
	defer func() { memoryBuffer = prev }()

	mem := newMemorySpanExporter(6)
	mem.ring[0] = SpanSummary{Name: "s1", SpanID: "1", TraceID: "ta"}
	mem.ring[1] = SpanSummary{Name: "s2", SpanID: "2", TraceID: "tb"}
	mem.ring[2] = SpanSummary{Name: "s3", SpanID: "3", TraceID: "ta"}
	// s4 has no map entry: it must still match via its own attribute.
	mem.ring[3] = SpanSummary{Name: "s4", SpanID: "4", TraceID: "tc",
		Attributes: map[string]string{"gen_ai.conversation.id": "sess-a"}}
	mem.next = 4
	mem.count = 4
	mem.sessions["ta"] = "sess-a"
	mem.sessions["tb"] = "sess-b"
	memoryBuffer = mem

	all := GetSpanSummaries("", 10)
	require.Len(t, all, 4, "no filter returns everything buffered")

	filtered := GetSpanSummaries("sess-a", 10)
	require.Equal(t, []string{"s1", "s3", "s4"}, []string{filtered[0].Name, filtered[1].Name, filtered[2].Name},
		"map miss falls back to the summary's own conversation id attribute")

	tail := GetSpanSummaries("", 2)
	require.Equal(t, []string{"s3", "s4"}, []string{tail[0].Name, tail[1].Name}, "limit caps the tail")
}

func drain(mem *memorySpanExporter) []SpanSummary {
	out := make([]SpanSummary, 0, mem.count)
	start := 0
	if mem.count == len(mem.ring) {
		start = mem.next
	}
	for i := range mem.count {
		out = append(out, mem.ring[(start+i)%len(mem.ring)])
	}
	return out
}
