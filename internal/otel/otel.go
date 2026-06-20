// Package otel provides OpenTelemetry SDK initialization, tracing helpers,
// and metrics for Phosphor. Instrumentation is disabled (no-op) unless an OTLP
// endpoint is configured.
package otel

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"

	"github.com/hackafterdark/phosphor/internal/config"
	"github.com/hackafterdark/phosphor/internal/version"
)

const (
	// TracerName is the name of the Phosphor OTel tracer.
	TracerName = "github.com/hackafterdark/phosphor"
)

// AgentTurnSpanKey is the context key used to store the agent turn span.
// It is exported so that other packages (e.g., internal/agent/tools) can
// store and retrieve the span from context for proper OTel span nesting.
type agentTurnSpanKey string

// AgentTurnSpan is the context key value for the agent turn span.
const AgentTurnSpan agentTurnSpanKey = "agent_turn_span"

var tracer trace.Tracer

func init() {
	tracer = otel.Tracer(TracerName)
}

// Init creates and installs the global TracerProvider and Propagator.
// Returns a shutdown function that should be deferred.
// When cfg.Endpoint is empty, a no-op tracer is used and the returned
// shutdown function is a no-op.
func Init(ctx context.Context, cfg config.Observability) (func(context.Context) error, error) {
	if cfg.Endpoint == "" {
		return func(ctx context.Context) error { return nil }, nil
	}

	res, err := resource.New(ctx,
		resource.WithFromEnv(),
		resource.WithProcess(),
		resource.WithHost(),
		resource.WithAttributes(
			semconv.ServiceName(cfg.ServiceName),
			semconv.ServiceVersion(version.Version),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("otel: create resource: %w", err)
	}

	if len(cfg.ResourceAttributes) > 0 {
		attrs := make([]attribute.KeyValue, 0, len(cfg.ResourceAttributes))
		for k, v := range cfg.ResourceAttributes {
			attrs = append(attrs, attribute.String(k, v))
		}
		allAttrs := make([]attribute.KeyValue, 0, 2+len(attrs))
		allAttrs = append(allAttrs,
			semconv.ServiceName(cfg.ServiceName),
			semconv.ServiceVersion(version.Version),
		)
		allAttrs = append(allAttrs, attrs...)
		res, err = resource.New(ctx,
			resource.WithFromEnv(),
			resource.WithProcess(),
			resource.WithHost(),
			resource.WithAttributes(allAttrs...),
		)
		if err != nil {
			return nil, fmt.Errorf("otel: create resource with attributes: %w", err)
		}
	}

	var exporter *otlptrace.Exporter
	switch cfg.Protocol {
	case "http/protobuf":
		exporter, err = otlptracehttp.New(ctx,
			otlptracehttp.WithEndpoint(cfg.Endpoint),
			otlptracehttp.WithInsecure(),
		)
	default:
		exporter, err = otlptracegrpc.New(ctx,
			otlptracegrpc.WithEndpoint(cfg.Endpoint),
			otlptracegrpc.WithInsecure(),
		)
	}
	if err != nil {
		return nil, fmt.Errorf("otel: create exporter: %w", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter,
			// Increase batch timeout to give parent spans more time to
			// end before child spans are flushed. This reduces the
			// "parent span ID is not in the trace" warnings from the
			// collector when child spans finish before the parent.
			sdktrace.WithBatchTimeout(2000*time.Millisecond),
			sdktrace.WithMaxExportBatchSize(512),
			sdktrace.WithMaxQueueSize(2048),
			sdktrace.WithExportTimeout(10*time.Second),
		),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.ParentBased(
			sdktrace.TraceIDRatioBased(cfg.SamplingRate),
		)),
	)

	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	return tp.Shutdown, nil
}

// Tracer returns the global Phosphor tracer.
func Tracer() trace.Tracer {
	return tracer
}

// StartSpan is a convenience wrapper around tracer.Start.
// If an agent turn span is present in the context (stored via tools.AgentTurnSpanKey),
// it will be used as the parent span so that tool call spans are properly nested.
// The returned span is wrapped to ensure a minimum duration of 1µs, preventing
// "Negative duration detected" warnings from the OTel collector when fast spans
// end at the same nanosecond they started.
func StartSpan(ctx context.Context, name string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	// Check if there's an agent turn span in the context to use as parent.
	if agentSpan := getAgentTurnSpan(ctx); agentSpan != nil {
		// Create a new context with the agent span as the current span.
		// This ensures the new span will be a child of the agent turn span.
		ctx = trace.ContextWithSpan(ctx, agentSpan)
	}
	ctx2, span := tracer.Start(ctx, name, opts...)
	return ctx2, &minDurationSpan{Span: span, start: time.Now()}
}

// minDurationSpan wraps a trace.Span to ensure a minimum duration of 1µs,
// preventing "Negative duration detected" warnings from the OTel collector.
type minDurationSpan struct {
	trace.Span
	start time.Time
}

// End ends the wrapped span. If the span would have a duration less than 1µs,
// it sleeps briefly to ensure the minimum duration is met.
func (s *minDurationSpan) End(opts ...trace.SpanEndOption) {
	elapsed := time.Since(s.start)
	if elapsed < time.Microsecond {
		// Sleep briefly to ensure at least 1µs duration.
		// This prevents the collector from seeing a zero or negative duration.
		time.Sleep(time.Microsecond - elapsed)
	}
	s.Span.End(opts...)
}

// getAgentTurnSpan retrieves the agent turn span from the context.
// It uses the exported AgentTurnSpan key to find the span.
func getAgentTurnSpan(ctx context.Context) trace.Span {
	if span, ok := ctx.Value(AgentTurnSpan).(trace.Span); ok {
		return span
	}
	return nil
}

// ensureParentSpan checks for an agent turn span in the context and ensures
// any new span created will be nested under it.
func ensureParentSpan(ctx context.Context) context.Context {
	if agentSpan := getAgentTurnSpan(ctx); agentSpan != nil {
		return trace.ContextWithSpan(ctx, agentSpan)
	}
	return ctx
}

// RecordError records an error on the span and sets the status to Error.
func RecordError(span trace.Span, err error) {
	if span == nil || err == nil {
		return
	}
	span.RecordError(err)
	span.SetStatus(codes.Error, err.Error())
}

// SetErrorStatus sets the span status to Error with the given message.
func SetErrorStatus(span trace.Span, msg string) {
	if span == nil {
		return
	}
	span.SetStatus(codes.Error, msg)
}

// DurationAttribute returns an attribute with the duration of d in milliseconds.
func DurationAttribute(d time.Duration) attribute.KeyValue {
	return attribute.Float64("duration_ms", float64(d.Milliseconds()))
}

// DurationUsAttribute returns an attribute with the duration of d in microseconds.
func DurationUsAttribute(d time.Duration) attribute.KeyValue {
	return attribute.Int64("duration_us", int64(d.Microseconds()))
}

// StartInvokeAgentSpan creates an "invoke_agent" span following the OTel GenAI
// semantic conventions. The span wraps a full agent turn (LLM call + tool
// executions) and is marked INTERNAL since Phosphor runs locally.
func StartInvokeAgentSpan(ctx context.Context, agentName, conversationID string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	attrs := []attribute.KeyValue{
		attribute.String(string(genAIAttrKeys.OperationName), "invoke_agent"),
		attribute.String(string(genAIAttrKeys.AgentName), agentName),
		attribute.String(string(genAIAttrKeys.ConversationID), conversationID),
	}
	spanOpts := append(opts, trace.WithSpanKind(trace.SpanKindInternal), trace.WithAttributes(attrs...))
	return tracer.Start(ctx, "invoke_agent "+agentName, spanOpts...)
}

// StartLLMSpan creates an LLM call span following the OTel GenAI semantic
// conventions. The span represents a single model API call (e.g. chat completion)
// and is marked CLIENT since it calls an external API.
func StartLLMSpan(ctx context.Context, provider, model string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	// Ensure the span is nested under the agent turn span if present.
	ctx = ensureParentSpan(ctx)
	attrs := []attribute.KeyValue{
		attribute.String(string(genAIAttrKeys.OperationName), "chat"),
		attribute.String(string(genAIAttrKeys.ProviderName), provider),
		attribute.String(string(genAIAttrKeys.RequestModel), model),
	}
	spanOpts := append(opts, trace.WithSpanKind(trace.SpanKindClient), trace.WithAttributes(attrs...))
	return tracer.Start(ctx, "chat "+model, spanOpts...)
}

// StartAttachmentSpan creates a span for processing attachments during an agent turn.
// The span wraps attachment preparation and is marked INTERNAL since Phosphor runs locally.
func StartAttachmentSpan(ctx context.Context, sessionID string, attachmentCount int, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	// Ensure the span is nested under the agent turn span if present.
	ctx = ensureParentSpan(ctx)
	attrs := []attribute.KeyValue{
		attribute.String("attachment.operation", "prepare"),
		attribute.String("session.id", sessionID),
		attribute.Int("attachment.count", attachmentCount),
	}
	spanOpts := append(opts, trace.WithSpanKind(trace.SpanKindInternal), trace.WithAttributes(attrs...))
	return tracer.Start(ctx, "attachment_prepare", spanOpts...)
}

// StartPromptWithAttachmentsSpan creates a span for building the prompt with text attachments.
func StartPromptWithAttachmentsSpan(ctx context.Context, sessionID string, attachmentCount int, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	// Ensure the span is nested under the agent turn span if present.
	ctx = ensureParentSpan(ctx)
	attrs := []attribute.KeyValue{
		attribute.String("prompt.operation", "with_attachments"),
		attribute.String("session.id", sessionID),
		attribute.Int("attachment.count", attachmentCount),
	}
	spanOpts := append(opts, trace.WithSpanKind(trace.SpanKindInternal), trace.WithAttributes(attrs...))
	return tracer.Start(ctx, "prompt_with_attachments", spanOpts...)
}

// --- GenAI Semantic Convention Helpers ---

// genAIAttrKeys provides typed attribute keys for GenAI semantic conventions.
var genAIAttrKeys = struct {
	OperationName      attribute.Key
	ProviderName       attribute.Key
	RequestModel       attribute.Key
	ResponseModel      attribute.Key
	ResponseID         attribute.Key
	ConversationID     attribute.Key
	AgentName          attribute.Key
	AgentID            attribute.Key
	AgentDescription   attribute.Key
	AgentVersion       attribute.Key
	WorkflowName       attribute.Key
	ToolName           attribute.Key
	ToolType           attribute.Key
	ToolCallID         attribute.Key
	ToolCallArgs       attribute.Key
	ToolCallResult     attribute.Key
	DataSourceID       attribute.Key
	OutputType         attribute.Key
	FinishReason       attribute.Key
	ErrorMessage       attribute.Key
	ErrorType          attribute.Key
	InputMessages      attribute.Key
	OutputMessages     attribute.Key
	SystemInstructions attribute.Key
	ToolDefinitions    attribute.Key
	UsageInputTokens   attribute.Key
	UsageOutputTokens  attribute.Key
	UsageReasoning     attribute.Key
	UsageCacheCreate   attribute.Key
	UsageCacheRead     attribute.Key
	RequestTemperature attribute.Key
	RequestTopP        attribute.Key
	RequestTopK        attribute.Key
	RequestMaxTokens   attribute.Key
	RequestFreqPenalty attribute.Key
	RequestPresencePen attribute.Key
}{
	OperationName:      "gen_ai.operation.name",
	ProviderName:       "gen_ai.provider.name",
	RequestModel:       "gen_ai.request.model",
	ResponseModel:      "gen_ai.response.model",
	ResponseID:         "gen_ai.response.id",
	ConversationID:     "gen_ai.conversation.id",
	AgentName:          "gen_ai.agent.name",
	AgentID:            "gen_ai.agent.id",
	AgentDescription:   "gen_ai.agent.description",
	AgentVersion:       "gen_ai.agent.version",
	WorkflowName:       "gen_ai.workflow.name",
	ToolName:           "gen_ai.tool.name",
	ToolType:           "gen_ai.tool.type",
	ToolCallID:         "gen_ai.tool.call.id",
	ToolCallArgs:       "gen_ai.tool.call.arguments",
	ToolCallResult:     "gen_ai.tool.call.result",
	DataSourceID:       "gen_ai.data_source.id",
	OutputType:         "gen_ai.output.type",
	FinishReason:       "gen_ai.response.finish_reason",
	ErrorMessage:       "gen_ai.error.message",
	ErrorType:          "error.type",
	InputMessages:      "gen_ai.input.messages",
	OutputMessages:     "gen_ai.output.messages",
	SystemInstructions: "gen_ai.system.instructions",
	ToolDefinitions:    "gen_ai.tool.definitions",
	UsageInputTokens:   "gen_ai.usage.input_tokens",
	UsageOutputTokens:  "gen_ai.usage.output_tokens",
	UsageReasoning:     "gen_ai.usage.reasoning.output_tokens",
	UsageCacheCreate:   "gen_ai.usage.cache_creation.input_tokens",
	UsageCacheRead:     "gen_ai.usage.cache_read.input_tokens",
	RequestTemperature: "gen_ai.request.temperature",
	RequestTopP:        "gen_ai.request.top_p",
	RequestTopK:        "gen_ai.request.top_k",
	RequestMaxTokens:   "gen_ai.request.max_tokens",
	RequestFreqPenalty: "gen_ai.request.frequency_penalty",
	RequestPresencePen: "gen_ai.request.presence_penalty",
}

// GenAIAttributes holds optional GenAI semantic convention attributes for spans.
type GenAIAttributes struct {
	OperationName      string
	ProviderName       string
	RequestModel       string
	ResponseModel      string
	ResponseID         string
	ConversationID     string
	AgentName          string
	AgentID            string
	AgentDescription   string
	AgentVersion       string
	WorkflowName       string
	ToolName           string
	ToolType           string
	ToolCallID         string
	ToolCallArgs       string
	ToolCallResult     string
	DataSourceID       string
	OutputType         string
	FinishReason       string
	ErrorMessage       string
	ErrorType          string
	InputMessages      string
	OutputMessages     string
	SystemInstructions string
	ToolDefinitions    string
	RequestTemperature *float64
	RequestTopP        *float64
	RequestTopK        *int64
	RequestMaxTokens   *int64
	RequestFreqPenalty *float64
	RequestPresencePen *float64
	UsageInputTokens   *int64
	UsageOutputTokens  *int64
	UsageReasoning     *int64
	UsageCacheCreate   *int64
	UsageCacheRead     *int64
}

// buildGenAIAttrKeys builds a slice of attribute.KeyValue from GenAIAttributes,
// skipping empty/nil values.
func buildGenAIAttrKeys(attrs GenAIAttributes) []attribute.KeyValue {
	var out []attribute.KeyValue
	if attrs.OperationName != "" {
		out = append(out, attribute.String(string(genAIAttrKeys.OperationName), attrs.OperationName))
	}
	if attrs.ProviderName != "" {
		out = append(out, attribute.String(string(genAIAttrKeys.ProviderName), attrs.ProviderName))
	}
	if attrs.RequestModel != "" {
		out = append(out, attribute.String(string(genAIAttrKeys.RequestModel), attrs.RequestModel))
	}
	if attrs.ResponseModel != "" {
		out = append(out, attribute.String(string(genAIAttrKeys.ResponseModel), attrs.ResponseModel))
	}
	if attrs.ResponseID != "" {
		out = append(out, attribute.String(string(genAIAttrKeys.ResponseID), attrs.ResponseID))
	}
	if attrs.ConversationID != "" {
		out = append(out, attribute.String(string(genAIAttrKeys.ConversationID), attrs.ConversationID))
	}
	if attrs.AgentName != "" {
		out = append(out, attribute.String(string(genAIAttrKeys.AgentName), attrs.AgentName))
	}
	if attrs.AgentID != "" {
		out = append(out, attribute.String(string(genAIAttrKeys.AgentID), attrs.AgentID))
	}
	if attrs.AgentDescription != "" {
		out = append(out, attribute.String(string(genAIAttrKeys.AgentDescription), attrs.AgentDescription))
	}
	if attrs.AgentVersion != "" {
		out = append(out, attribute.String(string(genAIAttrKeys.AgentVersion), attrs.AgentVersion))
	}
	if attrs.WorkflowName != "" {
		out = append(out, attribute.String(string(genAIAttrKeys.WorkflowName), attrs.WorkflowName))
	}
	if attrs.ToolName != "" {
		out = append(out, attribute.String(string(genAIAttrKeys.ToolName), attrs.ToolName))
	}
	if attrs.ToolType != "" {
		out = append(out, attribute.String(string(genAIAttrKeys.ToolType), attrs.ToolType))
	}
	if attrs.ToolCallID != "" {
		out = append(out, attribute.String(string(genAIAttrKeys.ToolCallID), attrs.ToolCallID))
	}
	if attrs.ToolCallArgs != "" {
		out = append(out, attribute.String(string(genAIAttrKeys.ToolCallArgs), attrs.ToolCallArgs))
	}
	if attrs.ToolCallResult != "" {
		out = append(out, attribute.String(string(genAIAttrKeys.ToolCallResult), attrs.ToolCallResult))
	}
	if attrs.DataSourceID != "" {
		out = append(out, attribute.String(string(genAIAttrKeys.DataSourceID), attrs.DataSourceID))
	}
	if attrs.OutputType != "" {
		out = append(out, attribute.String(string(genAIAttrKeys.OutputType), attrs.OutputType))
	}
	if attrs.FinishReason != "" {
		out = append(out, attribute.String(string(genAIAttrKeys.FinishReason), attrs.FinishReason))
	}
	if attrs.ErrorMessage != "" {
		out = append(out, attribute.String(string(genAIAttrKeys.ErrorMessage), attrs.ErrorMessage))
	}
	if attrs.ErrorType != "" {
		out = append(out, attribute.String(string(genAIAttrKeys.ErrorType), attrs.ErrorType))
	}
	if attrs.InputMessages != "" {
		out = append(out, attribute.String(string(genAIAttrKeys.InputMessages), attrs.InputMessages))
	}
	if attrs.OutputMessages != "" {
		out = append(out, attribute.String(string(genAIAttrKeys.OutputMessages), attrs.OutputMessages))
	}
	if attrs.SystemInstructions != "" {
		out = append(out, attribute.String(string(genAIAttrKeys.SystemInstructions), attrs.SystemInstructions))
	}
	if attrs.ToolDefinitions != "" {
		out = append(out, attribute.String(string(genAIAttrKeys.ToolDefinitions), attrs.ToolDefinitions))
	}
	if attrs.RequestTemperature != nil {
		out = append(out, attribute.Float64(string(genAIAttrKeys.RequestTemperature), *attrs.RequestTemperature))
	}
	if attrs.RequestTopP != nil {
		out = append(out, attribute.Float64(string(genAIAttrKeys.RequestTopP), *attrs.RequestTopP))
	}
	if attrs.RequestTopK != nil {
		out = append(out, attribute.Int64(string(genAIAttrKeys.RequestTopK), *attrs.RequestTopK))
	}
	if attrs.RequestMaxTokens != nil {
		out = append(out, attribute.Int64(string(genAIAttrKeys.RequestMaxTokens), *attrs.RequestMaxTokens))
	}
	if attrs.RequestFreqPenalty != nil {
		out = append(out, attribute.Float64(string(genAIAttrKeys.RequestFreqPenalty), *attrs.RequestFreqPenalty))
	}
	if attrs.RequestPresencePen != nil {
		out = append(out, attribute.Float64(string(genAIAttrKeys.RequestPresencePen), *attrs.RequestPresencePen))
	}
	if attrs.UsageInputTokens != nil {
		out = append(out, attribute.Int64(string(genAIAttrKeys.UsageInputTokens), *attrs.UsageInputTokens))
	}
	if attrs.UsageOutputTokens != nil {
		out = append(out, attribute.Int64(string(genAIAttrKeys.UsageOutputTokens), *attrs.UsageOutputTokens))
	}
	if attrs.UsageReasoning != nil {
		out = append(out, attribute.Int64(string(genAIAttrKeys.UsageReasoning), *attrs.UsageReasoning))
	}
	if attrs.UsageCacheCreate != nil {
		out = append(out, attribute.Int64(string(genAIAttrKeys.UsageCacheCreate), *attrs.UsageCacheCreate))
	}
	if attrs.UsageCacheRead != nil {
		out = append(out, attribute.Int64(string(genAIAttrKeys.UsageCacheRead), *attrs.UsageCacheRead))
	}
	return out
}

// StartGenAISpan creates a span with standard GenAI semantic convention attributes.
func StartGenAISpan(ctx context.Context, spanName string, attrs GenAIAttributes, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	spanOpts := append(opts, trace.WithAttributes(buildGenAIAttrKeys(attrs)...))
	return tracer.Start(ctx, spanName, spanOpts...)
}

// SetGenAIAttributes sets GenAI semantic convention attributes on an existing span.
func SetGenAIAttributes(span trace.Span, attrs GenAIAttributes) {
	if span == nil {
		return
	}
	span.SetAttributes(buildGenAIAttrKeys(attrs)...)
	if attrs.ErrorMessage != "" {
		span.SetStatus(codes.Error, attrs.ErrorMessage)
	}
}
