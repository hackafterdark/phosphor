package agent

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"charm.land/fantasy"
	"github.com/hackafterdark/phosphor/pkg/message"
	"github.com/stretchr/testify/require"
)

// TestTruncatedToolCallContinuationsCarryThroughRecursion pins the reason the
// count travels on the context: sessionAgent.Run recovers from a tool call
// whose arguments were truncated mid-string by recursing into itself, so the
// continuation budget has to survive the nesting or the cap resets per level.
func TestTruncatedToolCallContinuationsCarryThroughRecursion(t *testing.T) {
	t.Parallel()

	base := context.Background()
	require.Equal(t, 0, truncatedToolCallContinuationsFromContext(base), "a turn that never recovered has spent nothing")

	nested := base
	for range 4 {
		nested = withTruncatedToolCallContinuations(nested, truncatedToolCallContinuationsFromContext(nested)+1)
	}
	require.Equal(t, 4, truncatedToolCallContinuationsFromContext(nested), "each recursive level must see the running total")

	// A sibling branch off the same root must not inherit another branch's spend.
	sibling := withTruncatedToolCallContinuations(base, 9)
	require.Equal(t, 9, truncatedToolCallContinuationsFromContext(sibling))
	require.Equal(t, 4, truncatedToolCallContinuationsFromContext(nested))
}

// truncatedCallModel replays the confirmed live shape: one step whose write
// call streams arguments that end mid-string (finish reason tool-calls),
// followed by plain text/stop steps for the recovery turns.
type truncatedCallModel struct {
	calls atomic.Int32
}

func (m *truncatedCallModel) Provider() string { return "fake" }
func (m *truncatedCallModel) Model() string    { return "fake-model" }

func (m *truncatedCallModel) Generate(ctx context.Context, call fantasy.Call) (*fantasy.Response, error) {
	return &fantasy.Response{
		Content:      fantasy.ResponseContent{fantasy.TextContent{Text: "done"}},
		FinishReason: fantasy.FinishReasonStop,
	}, nil
}

func (m *truncatedCallModel) Stream(ctx context.Context, call fantasy.Call) (fantasy.StreamResponse, error) {
	n := m.calls.Add(1)
	if n == 1 {
		return func(yield func(fantasy.StreamPart) bool) {
			if !yield(fantasy.StreamPart{
				Type:          fantasy.StreamPartTypeToolCall,
				ID:            "chatcmpl-tool-truncated",
				ToolCallName:  "probe",
				ToolCallInput: `{"content": "import io`,
			}) {
				return
			}
			yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeFinish, FinishReason: fantasy.FinishReasonToolCalls})
		}, nil
	}
	return func(yield func(fantasy.StreamPart) bool) {
		if !yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeTextStart, ID: "1"}) {
			return
		}
		if !yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeTextDelta, ID: "1", Delta: "done"}) {
			return
		}
		if !yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeTextEnd, ID: "1"}) {
			return
		}
		yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeFinish, FinishReason: fantasy.FinishReasonStop})
	}, nil
}

func (m *truncatedCallModel) GenerateObject(ctx context.Context, call fantasy.ObjectCall) (*fantasy.ObjectResponse, error) {
	return nil, context.Canceled
}

func (m *truncatedCallModel) StreamObject(ctx context.Context, call fantasy.ObjectCall) (fantasy.ObjectStreamResponse, error) {
	return nil, context.Canceled
}

type probeToolInput struct {
	Content  string `json:"content"`
	FilePath string `json:"file_path"`
}

func newTruncatedCallTestAgent(t *testing.T) (*sessionAgent, fakeEnv, *truncatedCallModel) {
	t.Helper()
	env := testEnv(t)
	model := &truncatedCallModel{}
	probe := fantasy.NewAgentTool(
		"probe",
		"probe tool",
		func(ctx context.Context, input probeToolInput, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
			return fantasy.NewTextResponse("probe ran"), nil
		},
	)
	sa := testSessionAgent(env, model, model, "system", probe).(*sessionAgent)
	return sa, env, model
}

// TestRun_TruncatedToolCallContinuesDespiteNormalFinishReason is the
// regression test for the fifth silent dead-end: a provider that reports a
// normal tool_use finish while the call's streamed arguments were truncated
// mid-string. Fantasy's validation gate rejects the call before Run, the tool
// never executes, and the step's finish reason is entirely unremarkable — no
// existing continuation check keys off tool *results*, so the turn dead-ends.
// The one property that must survive future refactors: the retry fires even
// though the failing step's finish reason was the plain, successful
// FinishReasonToolUse — a "simplification" onto a FinishReason check would
// silently reintroduce the bug.
func TestRun_TruncatedToolCallContinuesDespiteNormalFinishReason(t *testing.T) {
	// Not parallel: this test owns the global slog default via captureLogs
	// and must not interleave with other parallel captures.
	buf, restore := captureLogs(t)
	defer restore()
	sa, env, model := newTruncatedCallTestAgent(t)

	sess, err := env.sessions.Create(t.Context(), "session")
	require.NoError(t, err)

	accept := sa.BeginAccepted(sess.ID)
	result, err := sa.Run(t.Context(), SessionAgentCall{
		SessionID: sess.ID,
		Prompt:    "continue",
		Accepted:  accept,
	})
	require.NoError(t, err)
	require.NotNil(t, result)

	logs := buf.String()
	require.True(t, strings.Contains(logs, "Step's tool call was truncated mid-argument by the provider despite a normal finish reason, queueing an automatic continuation") &&
		strings.Contains(logs, "attempt=1"))
	// Two steps in the original turn (truncated call, recovery text), plus
	// one step in the continuation run (the minimal mock does not re-issue
	// the call, so the continuation itself completes without spending the
	// cap's second attempt).
	require.Equal(t, int32(3), model.calls.Load(), "the continuation must re-enter the model exactly once")

	// The step that carried the truncated call reported the normal
	// tool_use finish reason — the flag, not the finish reason, triggered the
	// retry (see the note above about why a FinishReason gate cannot work).
	msgs, err := env.messages.List(t.Context(), sess.ID)
	require.NoError(t, err)
	var sawTruncatedCallStep bool
	for _, m := range msgs {
		if m.Role != message.Assistant {
			continue
		}
		for _, tc := range m.ToolCalls() {
			if tc.Name == "probe" {
				sawTruncatedCallStep = true
				require.Equal(t, message.FinishReasonToolUse, m.FinishReason(),
					"the failing step itself must have ended on the plain tool_use reason — this mechanism exists precisely because no finish reason flags this failure")
			}
		}
	}
	require.True(t, sawTruncatedCallStep, "the truncated probe call must be present in the transcript")
}

// TestRun_TruncatedToolCallBudgetExhaustionCompletesAsIs pins the cap: with
// the budget already spent, the turn completes without another recursion.
func TestRun_TruncatedToolCallBudgetExhaustionCompletesAsIs(t *testing.T) {
	// Not parallel: this test owns the global slog default via captureLogs
	// and must not interleave with other parallel captures.
	buf, restore := captureLogs(t)
	defer restore()
	sa, env, model := newTruncatedCallTestAgent(t)

	sess, err := env.sessions.Create(t.Context(), "session")
	require.NoError(t, err)

	accept := sa.BeginAccepted(sess.ID)
	ctx := withTruncatedToolCallContinuations(t.Context(), defaultMaxTruncatedToolCallContinuations)
	result, err := sa.Run(ctx, SessionAgentCall{
		SessionID: sess.ID,
		Prompt:    "continue",
		Accepted:  accept,
	})
	require.NoError(t, err)
	require.NotNil(t, result)

	logs := buf.String()
	require.Contains(t, logs, "Truncated-tool-call continuation budget exhausted, completing the turn as-is")
	require.Equal(t, int32(2), model.calls.Load(), "no recursion beyond the two steps of the turn itself")
}
