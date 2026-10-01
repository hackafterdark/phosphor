package agent

import (
	"strings"
	"testing"

	"github.com/hackafterdark/phosphor/pkg/message"
	"github.com/stretchr/testify/require"
)

// TestRun_ProactiveAutoSummarizeIsNotSelfBlocked is the regression test for a
// confirmed live bug: sessionAgent.Summarize refuses with ErrSessionBusy
// whenever IsSessionBusy is true, but the two proactive auto-summarize call
// sites in Run (before sending a request that would exceed, or already
// exceeds, the context window) invoke it from inside the very Run call that
// registered this session as busy in the first place. Every real dispatch
// goes through the accepted path, which registers busy before either check
// runs, so both calls always saw themselves as a competing request and
// always failed — silently (a Warn log only) — meaning auto-summarize before
// a request never actually ran for any session. Confirmed live: "Auto-
// summarizing before request to prevent context overflow" immediately
// followed by "Failed to auto-summarize before request" /
// "session is currently processing another request", at 81% context usage,
// with no compaction ever happening.
//
// This test drives Run through the real accepted dispatch path (matching
// production) with a session already over the summarize threshold, and
// asserts on the captured log output directly: "Failed to auto-summarize
// before request" (ErrSessionBusy's exact message) must never appear. A
// summary message existing afterward is not by itself sufficient proof this
// specific call site worked — Run has a separate, already-correct reactive
// summarization path (triggered by a StopWhen condition after a step
// completes) that can independently produce one and mask whether this
// proactive check succeeded or was silently skipped.
func TestRun_ProactiveAutoSummarizeIsNotSelfBlocked(t *testing.T) {
	t.Parallel()
	buf, restore := captureLogs(t)
	defer restore()
	sa, env := newStreamTestAgent(t)

	sess, err := env.sessions.Create(t.Context(), "session")
	require.NoError(t, err)

	// Seed enough real message content that estimateMessageTokensForMessage
	// (Run recomputes CurrentTokens from this before either auto-summarize
	// check, overwriting anything set directly on the session) reports well
	// over the default 80% threshold for the test harness's 200000-token
	// context window (see testSessionAgent in common_test.go). ~750000
	// characters / 4 (approxTokenCount) ≈ 187500 tokens.
	_, err = env.messages.Create(t.Context(), sess.ID, message.CreateMessageParams{
		Role: message.User,
		Parts: []message.ContentPart{message.TextContent{
			Text: strings.Repeat("word ", 150000),
		}},
	})
	require.NoError(t, err)

	// Drive Run through the real accepted dispatch path: this is what
	// registers the session busy before either auto-summarize check runs,
	// which is the exact condition that triggered the live bug.
	accept := sa.BeginAccepted(sess.ID)
	result, err := sa.Run(t.Context(), SessionAgentCall{
		SessionID: sess.ID,
		Prompt:    "continue",
		Accepted:  accept,
	})
	require.NoError(t, err)
	require.NotNil(t, result)

	logs := buf.String()
	require.NotContains(t, logs, "Failed to auto-summarize before request",
		"the proactive auto-summarize check must not see this Run invocation's own busy registration as a competing request")
	require.NotContains(t, logs, ErrSessionBusy.Error())
}
