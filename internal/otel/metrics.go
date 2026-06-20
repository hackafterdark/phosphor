// Package otel provides OpenTelemetry metrics for Phosphor.
// Metrics are disabled (no-op) unless an OTLP endpoint is configured.
package otel

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/hackafterdark/phosphor/internal/config"
)

// MeterName is the name of the Phosphor OTel meter.
const MeterName = "github.com/hackafterdark/phosphor"

var meter metric.Meter

func init() {
	meter = otel.Meter(MeterName)
}

// InitMetrics creates and registers all Phosphor metrics.
// Returns a shutdown function that should be deferred.
// When cfg.Endpoint is empty, no metrics are created and the returned
// shutdown function is a no-op.
func InitMetrics(cfg config.Observability) (func(context.Context) error, error) {
	if cfg.Endpoint == "" {
		return func(ctx context.Context) error { return nil }, nil
	}

	// Create metrics (they use no-op callbacks when the meter is no-op).
	_, err := newMetrics()
	if err != nil {
		return nil, fmt.Errorf("otel: init metrics: %w", err)
	}

	return func(ctx context.Context) error { return nil }, nil
}

// metrics holds all registered metric instruments.
type metrics struct {
	// Phosphor-specific metrics
	toolCallsTotal      metric.Int64Counter
	llmRequestsTotal    metric.Int64Counter
	llmTokensTotal      metric.Int64Counter
	toolCallsDuration   metric.Float64Histogram
	llmRequestsDuration metric.Float64Histogram
	agentTurnDuration   metric.Float64Histogram
	errorsTotal         metric.Int64Counter
	hooksTotal          metric.Int64Counter
	lspRequestsTotal    metric.Int64Counter
	mcpRequestsTotal    metric.Int64Counter
	// GenAI semantic convention metrics
	genAITokenUsage metric.Float64Histogram
	genAIOpDuration metric.Float64Histogram
	genAITTFB       metric.Float64Histogram
}

// GetMetrics returns the global metrics instance.
// Callers should only use this after InitMetrics has been called.
func GetMetrics() *metrics {
	return globalMetrics
}

var globalMetrics *metrics

// newMetrics creates and registers all metric instruments.
func newMetrics() (*metrics, error) {
	m := &metrics{}

	var err error

	m.toolCallsTotal, err = meter.Int64Counter(
		"phosphor.tool_calls.total",
		metric.WithDescription("Total number of tool calls"),
		metric.WithUnit("{call}"),
	)
	if err != nil {
		return nil, fmt.Errorf("create tool_calls_total: %w", err)
	}

	m.llmRequestsTotal, err = meter.Int64Counter(
		"phosphor.llm_requests.total",
		metric.WithDescription("Total number of LLM API requests"),
		metric.WithUnit("{request}"),
	)
	if err != nil {
		return nil, fmt.Errorf("create llm_requests_total: %w", err)
	}

	m.llmTokensTotal, err = meter.Int64Counter(
		"phosphor.llm_tokens.total",
		metric.WithDescription("Total number of tokens used"),
		metric.WithUnit("{token}"),
	)
	if err != nil {
		return nil, fmt.Errorf("create llm_tokens_total: %w", err)
	}

	m.toolCallsDuration, err = meter.Float64Histogram(
		"phosphor.tool_calls.duration",
		metric.WithDescription("Tool call latency in milliseconds"),
		metric.WithUnit("ms"),
	)
	if err != nil {
		return nil, fmt.Errorf("create tool_calls_duration: %w", err)
	}

	m.llmRequestsDuration, err = meter.Float64Histogram(
		"phosphor.llm_requests.duration",
		metric.WithDescription("LLM request latency in milliseconds"),
		metric.WithUnit("ms"),
	)
	if err != nil {
		return nil, fmt.Errorf("create llm_requests_duration: %w", err)
	}

	m.agentTurnDuration, err = meter.Float64Histogram(
		"phosphor.agent_turn.duration",
		metric.WithDescription("Full agent turn latency in milliseconds"),
		metric.WithUnit("ms"),
	)
	if err != nil {
		return nil, fmt.Errorf("create agent_turn_duration: %w", err)
	}

	m.errorsTotal, err = meter.Int64Counter(
		"phosphor.errors.total",
		metric.WithDescription("Total number of errors by type"),
		metric.WithUnit("{error}"),
	)
	if err != nil {
		return nil, fmt.Errorf("create errors_total: %w", err)
	}

	m.hooksTotal, err = meter.Int64Counter(
		"phosphor.hooks.total",
		metric.WithDescription("Total number of hook executions"),
		metric.WithUnit("{hook}"),
	)
	if err != nil {
		return nil, fmt.Errorf("create hooks_total: %w", err)
	}

	m.lspRequestsTotal, err = meter.Int64Counter(
		"phosphor.lsp_requests.total",
		metric.WithDescription("Total number of LSP protocol requests"),
		metric.WithUnit("{request}"),
	)
	if err != nil {
		return nil, fmt.Errorf("create lsp_requests_total: %w", err)
	}

	m.mcpRequestsTotal, err = meter.Int64Counter(
		"phosphor.mcp_requests.total",
		metric.WithDescription("Total number of MCP server requests"),
		metric.WithUnit("{request}"),
	)
	if err != nil {
		return nil, fmt.Errorf("create mcp_requests_total: %w", err)
	}

	// GenAI semantic convention metrics
	m.genAITokenUsage, err = meter.Float64Histogram(
		"gen_ai.client.token.usage",
		metric.WithDescription("GenAI client token usage"),
		metric.WithUnit("{token}"),
	)
	if err != nil {
		return nil, fmt.Errorf("create gen_ai.client.token.usage: %w", err)
	}

	m.genAIOpDuration, err = meter.Float64Histogram(
		"gen_ai.client.operation.duration",
		metric.WithDescription("GenAI client operation duration"),
		metric.WithUnit("s"),
	)
	if err != nil {
		return nil, fmt.Errorf("create gen_ai.client.operation.duration: %w", err)
	}

	m.genAITTFB, err = meter.Float64Histogram(
		"gen_ai.client.operation.time_to_first_chunk",
		metric.WithDescription("GenAI client time to first chunk"),
		metric.WithUnit("s"),
	)
	if err != nil {
		return nil, fmt.Errorf("create gen_ai.client.operation.time_to_first_chunk: %w", err)
	}

	globalMetrics = m
	return m, nil
}

// RecordToolCall records a tool call metric.
func RecordToolCall(toolName string) {
	if globalMetrics == nil {
		return
	}
	globalMetrics.toolCallsTotal.Add(context.Background(), 1, metric.WithAttributes(attribute.String("tool.name", toolName)))
}

// RecordToolCallDuration records the duration of a tool call.
func RecordToolCallDuration(toolName string, durationMs float64) {
	if globalMetrics == nil {
		return
	}
	globalMetrics.toolCallsDuration.Record(context.Background(), durationMs, metric.WithAttributes(attribute.String("tool.name", toolName)))
}

// RecordLLMRequest records an LLM API request.
func RecordLLMRequest(provider string, model string) {
	if globalMetrics == nil {
		return
	}
	globalMetrics.llmRequestsTotal.Add(context.Background(), 1,
		metric.WithAttributes(
			attribute.String("llm.provider", provider),
			attribute.String("llm.model", model),
		))
}

// RecordLLMRequestDuration records the duration of an LLM request.
func RecordLLMRequestDuration(provider string, model string, durationMs float64) {
	if globalMetrics == nil {
		return
	}
	globalMetrics.llmRequestsDuration.Record(context.Background(), durationMs,
		metric.WithAttributes(
			attribute.String("llm.provider", provider),
			attribute.String("llm.model", model),
		))
}

// RecordLLMTokens records token usage.
func RecordLLMTokens(provider string, model string, tokens int64, direction string) {
	if globalMetrics == nil {
		return
	}
	globalMetrics.llmTokensTotal.Add(context.Background(), tokens,
		metric.WithAttributes(
			attribute.String("llm.provider", provider),
			attribute.String("llm.model", model),
			attribute.String("llm.token_direction", direction),
		))
}

// RecordAgentTurnDuration records the duration of an agent turn.
func RecordAgentTurnDuration(sessionID string, durationMs float64) {
	if globalMetrics == nil {
		return
	}
	globalMetrics.agentTurnDuration.Record(context.Background(), durationMs,
		metric.WithAttributes(attribute.String("agent.session_id", sessionID)))
}

// RecordErrorMetric records an error metric.
func RecordErrorMetric(errType string) {
	if globalMetrics == nil {
		return
	}
	globalMetrics.errorsTotal.Add(context.Background(), 1,
		metric.WithAttributes(attribute.String("error.type", errType)))
}

// RecordHook records a hook execution.
func RecordHook(hookName string) {
	if globalMetrics == nil {
		return
	}
	globalMetrics.hooksTotal.Add(context.Background(), 1,
		metric.WithAttributes(attribute.String("hook.name", hookName)))
}

// RecordLSPRequest records an LSP protocol request.
func RecordLSPRequest(serverName string) {
	if globalMetrics == nil {
		return
	}
	globalMetrics.lspRequestsTotal.Add(context.Background(), 1,
		metric.WithAttributes(attribute.String("lsp.server", serverName)))
}

// RecordMCPRequest records an MCP server request.
func RecordMCPRequest(serverName string) {
	if globalMetrics == nil {
		return
	}
	globalMetrics.mcpRequestsTotal.Add(context.Background(), 1,
		metric.WithAttributes(attribute.String("mcp.server", serverName)))
}

// RecordGenAITokenUsage records token usage following GenAI semantic conventions.
func RecordGenAITokenUsage(provider string, model string, tokens int64, direction string) {
	if globalMetrics == nil {
		return
	}
	globalMetrics.genAITokenUsage.Record(context.Background(), float64(tokens),
		metric.WithAttributes(
			attribute.String("gen_ai.operation.name", "chat"),
			attribute.String("gen_ai.provider.name", provider),
			attribute.String("gen_ai.request.model", model),
			attribute.String("gen_ai.token.type", direction),
		))
}

// RecordGenAIOperationDuration records operation duration following GenAI semantic conventions.
func RecordGenAIOperationDuration(provider string, model string, durationS float64) {
	if globalMetrics == nil {
		return
	}
	globalMetrics.genAIOpDuration.Record(context.Background(), durationS,
		metric.WithAttributes(
			attribute.String("gen_ai.operation.name", "chat"),
			attribute.String("gen_ai.provider.name", provider),
			attribute.String("gen_ai.request.model", model),
		))
}

// RecordGenAITimeToFirstChunk records time-to-first-chunk for streaming following GenAI semantic conventions.
func RecordGenAITimeToFirstChunk(provider string, model string, ttfbS float64) {
	if globalMetrics == nil {
		return
	}
	globalMetrics.genAITTFB.Record(context.Background(), ttfbS,
		metric.WithAttributes(
			attribute.String("gen_ai.operation.name", "chat"),
			attribute.String("gen_ai.provider.name", provider),
			attribute.String("gen_ai.request.model", model),
		))
}
