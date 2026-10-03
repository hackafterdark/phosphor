package otel

import (
	"context"
	"testing"

	"github.com/hackafterdark/phosphor/pkg/config"
	"github.com/stretchr/testify/require"
)

// TestMemoryProbe_SamplingZero mimics the live config shape where
// sampling_rate is unset (0) but an endpoint exists.
func TestMemoryProbe_SamplingZero(t *testing.T) {
	prev := memoryBuffer
	defer func() { memoryBuffer = prev }()
	memoryBuffer = nil

	_, err := Init(context.Background(), config.Observability{
		MemoryBuffer: 500,
		Endpoint:     "127.0.0.1:4317",
		Protocol:     "grpc",
		ServiceName:  "phosphor",
		SamplingRate: 0,
	})
	require.NoError(t, err)
	require.NotNil(t, memoryBuffer)

	ctx, span := StartInvokeAgentSpan(context.Background(), "Phosphor", "sess-zero")
	_, child := StartLLMSpan(ctx, "prov", "model", GenAIAttributes{OperationName: "chat"})
	child.End()
	span.End()

	all := GetSpanSummaries("", 100)
	names := make([]string, 0, len(all))
	for _, s := range all {
		names = append(names, s.Name)
	}
	t.Logf("all=%v", names)
	t.Logf("filtered=%d", len(GetSpanSummaries("sess-zero", 100)))
}
