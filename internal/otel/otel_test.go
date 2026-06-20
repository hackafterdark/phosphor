package otel

import (
	"context"
	"testing"
	"time"

	"github.com/hackafterdark/phosphor/internal/config"
	"github.com/stretchr/testify/require"
)

func TestInit_NoEndpoint(t *testing.T) {
	// When no endpoint is configured, Init should return a no-op shutdown function.
	cfg := config.Observability{
		Endpoint: "",
	}

	shutdown, err := Init(context.Background(), cfg)
	require.NoError(t, err)
	require.NotNil(t, shutdown)

	// The shutdown function should be a no-op.
	err = shutdown(context.Background())
	require.NoError(t, err)
}

func TestInit_WithEndpoint(t *testing.T) {
	// When an endpoint is configured, Init should create a real tracer provider.
	cfg := config.Observability{
		Endpoint:     "http://localhost:4317",
		Protocol:     "grpc",
		SamplingRate: 1.0,
		ServiceName:  "phosphor-test",
	}

	shutdown, err := Init(context.Background(), cfg)
	require.NoError(t, err)
	require.NotNil(t, shutdown)

	// The shutdown function should work.
	err = shutdown(context.Background())
	require.NoError(t, err)
}

func TestInit_HTTPProtocol(t *testing.T) {
	// Test HTTP/protobuf protocol.
	cfg := config.Observability{
		Endpoint:     "http://localhost:4318",
		Protocol:     "http/protobuf",
		SamplingRate: 1.0,
		ServiceName:  "phosphor-test",
	}

	shutdown, err := Init(context.Background(), cfg)
	require.NoError(t, err)
	require.NotNil(t, shutdown)

	err = shutdown(context.Background())
	require.NoError(t, err)
}

func TestTracer(t *testing.T) {
	// Tracer should always return a valid tracer (even if no-op).
	tr := Tracer()
	require.NotNil(t, tr)
}

func TestStartSpan_NoEndpoint(t *testing.T) {
	// When no endpoint is configured, StartSpan should still work (no-op span).
	cfg := config.Observability{
		Endpoint: "",
	}

	shutdown, err := Init(context.Background(), cfg)
	require.NoError(t, err)
	defer shutdown(context.Background())

	ctx, span := StartSpan(context.Background(), "test.span")
	require.NotNil(t, span)
	span.End()
	_ = ctx
}

func TestDurationAttribute(t *testing.T) {
	// DurationAttribute should return a valid attribute.
	d := DurationAttribute(100 * time.Millisecond) // 100ms
	require.Equal(t, "duration_ms", string(d.Key))
	require.Equal(t, 100.0, d.Value.AsFloat64())
}

func TestDurationUsAttribute(t *testing.T) {
	// DurationUsAttribute should return a valid attribute.
	d := DurationUsAttribute(1000 * time.Millisecond) // 1000ms = 1s in microseconds
	require.Equal(t, "duration_us", string(d.Key))
	require.Equal(t, int64(1000000), d.Value.AsInt64())
}

func TestRecordError_NilSpan(t *testing.T) {
	// RecordError should not panic with nil span.
	RecordError(nil, nil)
	RecordError(nil, context.DeadlineExceeded)
}

func TestSetErrorStatus_NilSpan(t *testing.T) {
	// SetErrorStatus should not panic with nil span.
	SetErrorStatus(nil, "test error")
}

func TestInitMetrics_NoEndpoint(t *testing.T) {
	// When no endpoint is configured, InitMetrics should return a no-op shutdown function.
	cfg := config.Observability{
		Endpoint: "",
	}

	shutdown, err := InitMetrics(cfg)
	require.NoError(t, err)
	require.NotNil(t, shutdown)

	err = shutdown(context.Background())
	require.NoError(t, err)
}

func TestRecordToolCall_NoMetrics(t *testing.T) {
	// When metrics are not initialized, RecordToolCall should not panic.
	RecordToolCall("bash")
	RecordToolCallDuration("bash", 100.0)
	RecordLLMRequest("openai", "gpt-4")
	RecordLLMRequestDuration("openai", "gpt-4", 100.0)
	RecordLLMTokens("openai", "gpt-4", 1000, "input")
	RecordAgentTurnDuration("session-1", 100.0)
	RecordErrorMetric("test_error")
	RecordHook("test_hook")
	RecordLSPRequest("gopls")
	RecordMCPRequest("test_mcp")
}

func TestGetMetrics_NoInit(t *testing.T) {
	// GetMetrics should return nil when metrics are not initialized.
	m := GetMetrics()
	require.Nil(t, m)
}

func TestStartGenAISpan_NoEndpoint(t *testing.T) {
	// When no endpoint is configured, StartGenAISpan should still work (no-op span).
	cfg := config.Observability{
		Endpoint: "",
	}
	shutdown, err := Init(context.Background(), cfg)
	require.NoError(t, err)
	defer shutdown(context.Background())

	// Test with all GenAI attributes.
	inputTokens := int64(100)
	outputTokens := int64(50)
	temp := 0.7
	ctx, span := StartGenAISpan(context.Background(), "chat gpt-4o", GenAIAttributes{
		OperationName:      "chat",
		ProviderName:       "openai",
		RequestModel:       "gpt-4o",
		ResponseModel:      "gpt-4o",
		AgentName:          "Phosphor Agent",
		FinishReason:       "stop",
		RequestTemperature: &temp,
		UsageInputTokens:   &inputTokens,
		UsageOutputTokens:  &outputTokens,
	})
	require.NotNil(t, span)
	span.End()
	_ = ctx
}

func TestSetGenAIAttributes_NilSpan(t *testing.T) {
	// SetGenAIAttributes should not panic with nil span.
	SetGenAIAttributes(nil, GenAIAttributes{
		OperationName: "chat",
		ProviderName:  "openai",
	})
}

func TestRecordGenAIMetrics_NoMetrics(t *testing.T) {
	// When metrics are not initialized, GenAI record functions should not panic.
	RecordGenAITokenUsage("openai", "gpt-4", 100, "input")
	RecordGenAIOperationDuration("openai", "gpt-4", 1.5)
	RecordGenAITimeToFirstChunk("openai", "gpt-4", 0.1)
}
