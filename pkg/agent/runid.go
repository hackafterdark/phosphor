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

// forcedStopContinuationsContextKey is the unexported context key that
// carries how many automatic continuations this turn has already spent
// recovering from a step the inference engine cut off mid-generation by
// sampling a raw ChatML control token (see defangReasoningText and
// defangAssistantText). sessionAgent.Run recovers by recursing into itself,
// so a plain local counter would reset to zero on every level and the cap
// below could never see the running total the turn had actually spent.
type forcedStopContinuationsContextKey struct{}

// defaultMaxForcedStopContinuations bounds those automatic continuations.
// Without a cap, a model that keeps colliding with the same control token on
// every retry would recurse through sessionAgent.Run unbounded.
const defaultMaxForcedStopContinuations = 2

// withForcedStopContinuations returns ctx carrying the number of automatic
// continuations consumed so far, for the recursive call into
// sessionAgent.Run.
func withForcedStopContinuations(ctx context.Context, n int) context.Context {
	return context.WithValue(ctx, forcedStopContinuationsContextKey{}, n)
}

// forcedStopContinuationsFromContext returns the count set by
// [withForcedStopContinuations], or zero when the turn has not yet recovered
// from a forced stop.
func forcedStopContinuationsFromContext(ctx context.Context) int {
	if v, ok := ctx.Value(forcedStopContinuationsContextKey{}).(int); ok {
		return v
	}
	return 0
}

// maxTokensContinuationsContextKey is the unexported context key that
// carries how many automatic continuations this turn has already spent
// recovering from a step that ended on FinishReasonMaxTokens: the model ran
// out of its output/thinking token budget mid-generation, not a raw control
// token forcing an early stop. Kept as its own key, separate from
// forcedStopContinuationsContextKey, because running out of a configured
// token budget can be a deliberate operator constraint (cost control), so
// its retry budget is independently configurable rather than inheriting the
// forced-stop cap. sessionAgent.Run recovers by recursing into itself, so a
// plain local counter would reset to zero on every level.
type maxTokensContinuationsContextKey struct{}

// defaultMaxTokensContinuations bounds those automatic continuations when
// config.AgentConfig.MaxTokensContinuations is unset (zero).
const defaultMaxTokensContinuations = 2

// effectiveMaxTokensContinuations resolves the configured cap: zero falls
// back to defaultMaxTokensContinuations, a positive value is honoured
// exactly, and a negative value disables the cap (unlimited continuations) —
// callers must check for a negative result themselves rather than treat it
// as a literal turn count.
func effectiveMaxTokensContinuations(configured int) int {
	if configured == 0 {
		return defaultMaxTokensContinuations
	}
	return configured
}

// withMaxTokensContinuations returns ctx carrying the number of automatic
// continuations consumed so far, for the recursive call into
// sessionAgent.Run.
func withMaxTokensContinuations(ctx context.Context, n int) context.Context {
	return context.WithValue(ctx, maxTokensContinuationsContextKey{}, n)
}

// maxTokensContinuationsFromContext returns the count set by
// [withMaxTokensContinuations], or zero when the turn has not yet recovered
// from hitting its token budget.
func maxTokensContinuationsFromContext(ctx context.Context) int {
	if v, ok := ctx.Value(maxTokensContinuationsContextKey{}).(int); ok {
		return v
	}
	return 0
}

// unrecognizedToolCallContinuationsContextKey is the unexported context key
// that carries how many automatic continuations this turn has already spent
// recovering from a step that ended normally (FinishReasonEndTurn) but whose
// final text is the inference engine's own native tool-call syntax that its
// tool-call parser (e.g. vLLM's --tool-call-parser qwen3_xml) failed to
// recognize and convert into a real structured tool call — see
// looksLikeUnrecognizedToolCall in agent.go. Kept as its own key: unlike a
// forced ChatML stop or a token-budget cutoff, nothing here was truncated or
// cut off, so this budget is unrelated to either of those.
type unrecognizedToolCallContinuationsContextKey struct{}

// defaultMaxUnrecognizedToolCallContinuations bounds those automatic
// continuations. Not user-configurable, like the forced-stop budget: this
// recovers from a known, narrow parser quirk in a provider Phosphor already
// has other special-cased handling for (sanitizeJSONInput), not a tunable
// cost/operator tradeoff like the token-budget continuation.
const defaultMaxUnrecognizedToolCallContinuations = 2

// withUnrecognizedToolCallContinuations returns ctx carrying the number of
// automatic continuations consumed so far, for the recursive call into
// sessionAgent.Run.
func withUnrecognizedToolCallContinuations(ctx context.Context, n int) context.Context {
	return context.WithValue(ctx, unrecognizedToolCallContinuationsContextKey{}, n)
}

// unrecognizedToolCallContinuationsFromContext returns the count set by
// [withUnrecognizedToolCallContinuations], or zero when the turn has not yet
// recovered from an unrecognized tool call.
func unrecognizedToolCallContinuationsFromContext(ctx context.Context) int {
	if v, ok := ctx.Value(unrecognizedToolCallContinuationsContextKey{}).(int); ok {
		return v
	}
	return 0
}

// isInternalContinuation reports whether ctx marks this sessionAgent.Run call
// as an internal recursive continuation of an already-active turn — the
// self-critique reflection retry, the forced-stop recovery, a token-budget
// continuation, or an unrecognized-tool-call retry — rather than a freshly,
// independently dispatched request.
//
// This matters because the outer Run invocation that is about to
// `return a.Run(ctx, call)` is still, from the accepted-dispatch gate's point
// of view, the active run for this session: its `defer a.activeRequests.Del`
// has not fired yet, since that defer is waiting on this very call to return.
// If the recursive call is treated as a normal new dispatch, the busy check
// sees this session as busy and silently re-queues the call instead of
// running it — and nothing ever drains that queue, because draining only
// happens from inside a live step's PrepareStep, and the step that would have
// driven it has already finished streaming. The turn then dies with no error
// and no further content: exactly the "it just gets stuck" symptom, this time
// caused by the recursion mechanism itself rather than by any model output.
// sessionAgent.Run must skip the busy/queue gate entirely for such a call.
func isInternalContinuation(ctx context.Context) bool {
	return reflectionTurnsFromContext(ctx) > 0 ||
		forcedStopContinuationsFromContext(ctx) > 0 ||
		maxTokensContinuationsFromContext(ctx) > 0 ||
		unrecognizedToolCallContinuationsFromContext(ctx) > 0
}
