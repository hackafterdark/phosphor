package agent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestRun_InternalContinuationBypassesBusyGate is the regression test for the
// self-deadlock this session found live: sessionAgent.Run recovers from
// reflection, a forced ChatML stop, or a token-budget cutoff by recursing
// into itself with the same SessionAgentCall (same Accepted handle). The
// outer invocation that is about to `return a.Run(...)` is still, from the
// accepted-dispatch gate's point of view, the active run for this session
// (its own deferred activeRequests cleanup has not fired yet), so without
// isInternalContinuation the busy check would see the session as busy and
// silently re-queue the recursive call instead of running it — and nothing
// ever drains that queue, since draining only happens from inside a live
// step's PrepareStep, and the step that would drive it has already finished
// streaming. The turn then dies with no error and no further content.
//
// Each subtest simulates exactly that window (an active request already
// registered, plus an accepted follow-up carrying the same continuation
// marker the real recursive call would carry) and asserts the call runs to
// completion instead of being swallowed into the queue.
func TestRun_InternalContinuationBypassesBusyGate(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		ctx  func(context.Context) context.Context
	}{
		{"reflection", func(ctx context.Context) context.Context { return withReflectionTurns(ctx, 1) }},
		{"forced_stop", func(ctx context.Context) context.Context { return withForcedStopContinuations(ctx, 1) }},
		{"max_tokens", func(ctx context.Context) context.Context { return withMaxTokensContinuations(ctx, 1) }},
		{"unrecognized_tool_call", func(ctx context.Context) context.Context { return withUnrecognizedToolCallContinuations(ctx, 1) }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			sa, env := newStreamTestAgent(t)

			sess, err := env.sessions.Create(t.Context(), "session")
			require.NoError(t, err)

			// Simulate the outer Run invocation still holding this session's
			// active-request slot, exactly as it would in the real recursion
			// window (its defer a.activeRequests.Del has not fired yet).
			sa.activeRequests.Set(sess.ID, func() {})

			accept := sa.BeginAccepted(sess.ID)
			result, err := sa.Run(c.ctx(t.Context()), SessionAgentCall{
				SessionID: sess.ID,
				Prompt:    "continue",
				Accepted:  accept,
			})
			require.NoError(t, err)
			require.NotNil(t, result, "an internal continuation must run to completion, not be swallowed into the queue")
			require.Equal(t, 0, sa.QueuedPrompts(sess.ID),
				"an internal continuation must bypass the busy gate rather than re-queue itself behind its own outer call")
		})
	}
}
