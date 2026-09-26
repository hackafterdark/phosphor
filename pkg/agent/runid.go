package agent

import "context"

// runIDContextKey is the unexported context key used to carry a
// caller-supplied RunID from the workspace HTTP boundary
// (backend.SendMessage) down into coordinator.Run without forcing a
// breaking change to the Coordinator.Run signature. The value is
// then copied onto SessionAgentCall.RunID by the coordinator so the
// agent's terminal RunComplete event can echo it back to the
// originating caller.
type runIDContextKey struct{}

// stripLastToolCallContextKey is an unexported context key that signals
// the agent to skip the last assistant tool call when building the
// conversation history. Used as a fallback when the stored tool call
// input is malformed and causes a persistent 400 Bad Request.
type stripLastToolCallContextKey struct{}

// toolObservationErrorKey is an unexported context key that carries the
// original validation error from the coordinator to the agent. When
// stripping is triggered, the agent uses this error to inject a
// "Tool Observation" message so the model understands why the call failed.
type toolObservationErrorKey struct{}

// WithStripLastToolCall returns ctx tagged so the agent skips the last
// assistant tool call. Used as a recovery path for malformed JSON in
// stored tool call inputs.
func WithStripLastToolCall(ctx context.Context) context.Context {
	return context.WithValue(ctx, stripLastToolCallContextKey{}, true)
}

// WithToolObservationError returns a context tagged with the original
// validation error, along with the tool name. The agent uses this to
// inject a "Tool Observation" message instead of silently stripping.
func WithToolObservationError(ctx context.Context, toolName string, err error) context.Context {
	return context.WithValue(ctx, toolObservationErrorKey{}, toolObservationInfo{ToolName: toolName, Err: err})
}

// ToolObservationErrorFromContext returns the tool observation info
// set by [WithToolObservationError], or zero values if none was set.
func ToolObservationErrorFromContext(ctx context.Context) toolObservationInfo {
	if v, ok := ctx.Value(toolObservationErrorKey{}).(toolObservationInfo); ok {
		return v
	}
	return toolObservationInfo{}
}

// toolObservationInfo carries the original validation error and tool name
// from the coordinator to the agent for Tool Observation injection.
type toolObservationInfo struct {
	ToolName string
	Err      error
}

// IsStripLastToolCall returns true if the context requests stripping
// the last assistant tool call.
func IsStripLastToolCall(ctx context.Context) bool {
	_, ok := ctx.Value(stripLastToolCallContextKey{}).(bool)
	return ok
}

// WithRunID returns ctx tagged with a per-request RunID. It is the
// boundary helper for callers that need their SendMessage→Run
// terminal event to be uniquely correlatable (e.g. `phosphor run`
// against a session that may be busy). Empty runIDs are stored
// as-is; downstream code treats an empty RunID as "caller did not
// supply one" and falls back to SessionID-only correlation.
func WithRunID(ctx context.Context, runID string) context.Context {
	return context.WithValue(ctx, runIDContextKey{}, runID)
}

// RunIDFromContext returns the RunID set by [WithRunID], or "" if
// none was set or the value is not a string. Exported because the
// coordinator and tests in other packages need to read it; safe to
// call on any context.
func RunIDFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(runIDContextKey{}).(string); ok {
		return v
	}
	return ""
}

// reflectionTurnsContextKey is the unexported context key that carries how
// many self-critique re-runs the current user turn has already consumed.
// sessionAgent.Run re-runs a turn by recursing into itself, so a plain local
// counter resets to zero on every level and the maxReflectionTurns guard could
// never see the running total the turn had actually spent.
type reflectionTurnsContextKey struct{}

// defaultMaxReflectionTurns bounds the self-critique re-runs when no cap is
// configured. Without one, a model that keeps emitting reflection blocks
// recursed through sessionAgent.Run unbounded, because the guard that was
// meant to stop it only engaged on a positive configured cap.
const defaultMaxReflectionTurns = 3

// effectiveMaxReflectionTurns resolves the configured cap, falling back to
// defaultMaxReflectionTurns when it is unset or non-positive.
func effectiveMaxReflectionTurns(configured int) int {
	if configured <= 0 {
		return defaultMaxReflectionTurns
	}
	return configured
}

// withReflectionTurns returns ctx carrying the number of self-critique re-runs
// consumed so far, for the recursive call into sessionAgent.Run.
func withReflectionTurns(ctx context.Context, turns int) context.Context {
	return context.WithValue(ctx, reflectionTurnsContextKey{}, turns)
}

// reflectionTurnsFromContext returns the count set by [withReflectionTurns], or
// zero when the turn has not re-run for a reflection yet.
func reflectionTurnsFromContext(ctx context.Context) int {
	if v, ok := ctx.Value(reflectionTurnsContextKey{}).(int); ok {
		return v
	}
	return 0
}
