package agent

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"charm.land/fantasy"
	"github.com/hackafterdark/phosphor/pkg/agent/tools"
	"github.com/hackafterdark/phosphor/pkg/message"
	"github.com/hackafterdark/phosphor/pkg/session"
	"github.com/stretchr/testify/require"
)

// TestRunStartSnapshotAndIdempotency pins the prompt-cache P1 contract: the
// run's dynamic state rides the user message once as a <run_context> block,
// and the message idempotency check matches the base prompt despite the
// block so queued/recursive paths never duplicate it.
func TestRunStartSnapshotAndIdempotency(t *testing.T) {
	t.Parallel()
	sa, env := newCancelTestAgent(t)

	sess, err := env.sessions.Create(t.Context(), "cache-test")
	require.NoError(t, err)
	sess.Todos = []session.Todo{
		{Content: "Fix the cache", Status: session.TodoStatusInProgress, ActiveForm: "Fixing the cache"},
		{Content: "Write tests", Status: session.TodoStatusPending},
	}
	_, err = env.sessions.Save(t.Context(), sess)
	require.NoError(t, err)

	base := "please fix the cache"

	// The snapshot composes the open todo list for the run's user message.
	require.False(t, sa.userMessageExistsFor(t.Context(), sess.ID, base))
	snap := sa.runStartSnapshot(t.Context(), sess.ID, "")
	require.Contains(t, snap, "<run_context>")
	require.Contains(t, snap, "<todo_list>")
	require.Contains(t, snap, "Fixing the cache")

	_, err = sa.createUserMessage(t.Context(), SessionAgentCall{
		SessionID: sess.ID,
		Prompt:    base + "\n\n" + snap,
	})
	require.NoError(t, err)

	// The queued/recursive path: the base prompt alone must match the
	// stored message that carries the block, so no duplicate is created.
	require.True(t, sa.userMessageExistsFor(t.Context(), sess.ID, base))
	_, err = sa.createUserMessage(t.Context(), SessionAgentCall{
		SessionID: sess.ID,
		Prompt:    base,
	})
	require.NoError(t, err)

	msgs, err := env.messages.List(t.Context(), sess.ID)
	require.NoError(t, err)
	var userMsgs int
	for _, m := range msgs {
		if m.Role == message.User {
			userMsgs++
		}
	}
	require.Equal(t, 1, userMsgs)
}

// TestStripRunContext verifies the block is removed cleanly from the base
// prompt text and that untagged text passes through untouched.
func TestStripRunContext(t *testing.T) {
	t.Parallel()
	base := "do the thing"
	with := base + "\n\n<run_context>\n<todo_list>\n1. x (y)\n</todo_list>\n</run_context>"
	require.Equal(t, base, stripRunContext(with))
	require.Equal(t, base, stripRunContext(base))
}

// cacheProbeModel records the exact wire prompt of every provider call and
// drives one scripted todos update on the first step.
type cacheProbeModel struct {
	calls   atomic.Int32
	mu      sync.Mutex
	prompts [][]fantasy.Message
}

func (m *cacheProbeModel) Provider() string { return "fake" }
func (m *cacheProbeModel) Model() string    { return "fake-model" }

func (m *cacheProbeModel) record(call fantasy.Call) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.prompts = append(m.prompts, call.Prompt)
}

func (m *cacheProbeModel) step() (fantasy.ToolCallContent, bool) {
	if m.calls.Add(1) == 1 {
		return fantasy.ToolCallContent{
			ToolCallID: "todos-1",
			ToolName:   tools.TodosToolName,
			Input: `{"todos":[` +
				`{"content":"Fix the cache","status":"completed","active_form":"Fixed the cache"},` +
				`{"content":"Burst the cache","status":"in_progress","active_form":"Bursting the cache"}]}`,
		}, true
	}
	return fantasy.ToolCallContent{}, false
}

func (m *cacheProbeModel) Generate(ctx context.Context, call fantasy.Call) (*fantasy.Response, error) {
	m.record(call)
	if tc, ok := m.step(); ok {
		return &fantasy.Response{
			Content:      fantasy.ResponseContent{tc},
			FinishReason: fantasy.FinishReasonToolCalls,
		}, nil
	}
	return &fantasy.Response{
		Content:      fantasy.ResponseContent{fantasy.TextContent{Text: "done"}},
		FinishReason: fantasy.FinishReasonStop,
	}, nil
}

func (m *cacheProbeModel) Stream(ctx context.Context, call fantasy.Call) (fantasy.StreamResponse, error) {
	m.record(call)
	if tc, ok := m.step(); ok {
		return func(yield func(fantasy.StreamPart) bool) {
			if !yield(fantasy.StreamPart{
				Type:          fantasy.StreamPartTypeToolCall,
				ID:            tc.ToolCallID,
				ToolCallName:  tc.ToolName,
				ToolCallInput: tc.Input,
			}) {
				return
			}
			yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeFinish, FinishReason: fantasy.FinishReasonToolCalls})
		}, nil
	}
	return func(yield func(fantasy.StreamPart) bool) {
		if !yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeTextDelta, Delta: "done"}) {
			return
		}
		yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeFinish, FinishReason: fantasy.FinishReasonStop})
	}, nil
}

func (m *cacheProbeModel) GenerateObject(ctx context.Context, call fantasy.ObjectCall) (*fantasy.ObjectResponse, error) {
	return nil, context.Canceled
}

func (m *cacheProbeModel) StreamObject(ctx context.Context, call fantasy.ObjectCall) (fantasy.ObjectStreamResponse, error) {
	return nil, context.Canceled
}

// TestWirePromptByteStableAcrossTodosUpdate pins the P4 contract: when the
// model updates its todos mid-run, the provider wire prefix (system block
// and every previously sent message) must stay byte-identical. Dynamic
// state rides the transcript tail, so the provider prefix cache survives
// every todos update.
func TestWirePromptByteStableAcrossTodosUpdate(t *testing.T) {
	t.Parallel()
	env := testEnv(t)
	model := &cacheProbeModel{}

	sess, err := env.sessions.Create(t.Context(), "cache-wire")
	require.NoError(t, err)
	sess.Todos = []session.Todo{
		{Content: "Fix the cache", Status: session.TodoStatusInProgress, ActiveForm: "Fixing the cache"},
	}
	_, err = env.sessions.Save(t.Context(), sess)
	require.NoError(t, err)

	// Title generation runs asynchronously after Run returns and tries the
	// small model first; keeping it off the probe keeps the recorded
	// prompts exactly the two run steps this test pins.
	sa := testSessionAgent(env, model, &finishStreamModel{text: "title"}, "system", tools.NewTodosTool(env.sessions)).(*sessionAgent)
	accept := sa.BeginAccepted(sess.ID)
	_, err = sa.Run(t.Context(), SessionAgentCall{
		SessionID: sess.ID,
		Prompt:    "get to work",
		Accepted:  accept,
	})
	require.NoError(t, err)

	model.mu.Lock()
	defer model.mu.Unlock()
	require.Len(t, model.prompts, 2, "one todos step and one closing text step")

	first, second := model.prompts[0], model.prompts[1]
	require.Greater(t, len(second), len(first), "the second prompt carries the assistant turn and tool result")
	require.Equal(t, contentOnly(first), contentOnly(second[:len(first)]),
		"the entire wire prefix sent on step one must be byte-identical on step two")

	// The snapshot rides the run's user message once; the system block
	// never carries todo state.
	var userText string
	for _, msg := range first {
		for _, part := range msg.Content {
			if tp, ok := part.(fantasy.TextPart); ok && msg.Role == fantasy.MessageRoleUser {
				userText += tp.Text
			}
		}
	}
	require.Contains(t, userText, "<run_context>")
	require.Contains(t, userText, "Fixing the cache")

	// The todos update itself must have landed.
	updated, err := env.sessions.Get(t.Context(), sess.ID)
	require.NoError(t, err)
	require.Len(t, updated.Todos, 2)
	require.Equal(t, session.TodoStatusCompleted, updated.Todos[0].Status)
}

// contentOnly strips provider transport metadata (the cache_control
// ephemeral markers, which by design sit on the newest messages and
// advance each step) so prefix comparisons cover message content only.
// Content, not marker placement, is what the provider prefix cache
// matches on.
func contentOnly(msgs []fantasy.Message) []fantasy.Message {
	out := make([]fantasy.Message, len(msgs))
	for i, msg := range msgs {
		msg.ProviderOptions = nil
		parts := make([]fantasy.MessagePart, len(msg.Content))
		for j, part := range msg.Content {
			if tp, ok := part.(fantasy.TextPart); ok {
				tp.ProviderOptions = nil
				parts[j] = tp
			} else {
				parts[j] = part
			}
		}
		msg.Content = parts
		out[i] = msg
	}
	return out
}

// TestContinuationNoteDeliveredForPersistedMessage pins the CodeRabbit
// fix: a recursive continuation queues a recovery note and re-enters Run
// with the same (already persisted) prompt. The note must ride the
// outbound prompt — the run exists precisely to deliver it — and must be
// consumed, never left to leak into a later user turn.
func TestContinuationNoteDeliveredForPersistedMessage(t *testing.T) {
	t.Parallel()
	env := testEnv(t)
	model := &cacheProbeModel{}

	sess, err := env.sessions.Create(t.Context(), "continuation-note")
	require.NoError(t, err)

	sa := testSessionAgent(env, model, model, "system", tools.NewTodosTool(env.sessions)).(*sessionAgent)
	accept := sa.BeginAccepted(sess.ID)
	_, err = sa.Run(t.Context(), SessionAgentCall{
		SessionID: sess.ID,
		Prompt:    "get to work",
		Accepted:  accept,
	})
	require.NoError(t, err)

	// The continuation path: queueNote then Run with the same call.
	// The user message is already persisted, so idempotency skips the
	// snapshot — but the note must still reach the model.
	sa.queueNote(sess.ID, "Continue exactly where you left off.")
	_, err = sa.Run(t.Context(), SessionAgentCall{
		SessionID: sess.ID,
		Prompt:    "get to work",
	})
	require.NoError(t, err)

	// The note was consumed: nothing is left to ride a later turn.
	_, ok := sa.pendingNotes.Get(sess.ID)
	require.False(t, ok, "continuation note must be consumed by the continuing run")

	// The note rode the outbound prompt of the continuing run (title
	// generation also probes the model with the same prompt, so match
	// any continuing-run call).
	model.mu.Lock()
	defer model.mu.Unlock()
	var delivered bool
	for _, call := range model.prompts[2:] {
		var userText string
		for _, msg := range call {
			for _, part := range msg.Content {
				if tp, ok := part.(fantasy.TextPart); ok && msg.Role == fantasy.MessageRoleUser {
					userText += tp.Text
				}
			}
		}
		if strings.Contains(userText, "Continue exactly where you left off.") {
			delivered = true
		}
	}
	require.True(t, delivered, "the continuation note must ride the outbound prompt")

	// No duplicate user message was created by the continuation.
	msgs, err := env.messages.List(t.Context(), sess.ID)
	require.NoError(t, err)
	var userMsgs int
	for _, m := range msgs {
		if m.Role == message.User {
			userMsgs++
		}
	}
	require.Equal(t, 1, userMsgs)
}

// TestQueuedTurnDeliversSnapshotForPersistedMessage pins the queued-turn
// half of the finding: independently queued turns are persisted bare
// before the recursive Run dispatch, so the persisted-message path must
// still deliver the todo and active-goal snapshot on the outbound
// prompt — without forking a duplicate user message.
func TestQueuedTurnDeliversSnapshotForPersistedMessage(t *testing.T) {
	t.Parallel()
	env := testEnv(t)
	model := &cacheProbeModel{}

	sess, err := env.sessions.Create(t.Context(), "queued-snapshot")
	require.NoError(t, err)
	sess.Todos = []session.Todo{
		{Content: "Fix the cache", Status: session.TodoStatusInProgress, ActiveForm: "Fixing the cache"},
	}
	_, err = env.sessions.Save(t.Context(), sess)
	require.NoError(t, err)

	sa := testSessionAgent(env, model, model, "system", tools.NewTodosTool(env.sessions)).(*sessionAgent)

	// The queue-dispatch path: the message is persisted before Run.
	queued := SessionAgentCall{SessionID: sess.ID, Prompt: "queued turn"}
	_, err = sa.createUserMessage(t.Context(), queued)
	require.NoError(t, err)

	_, err = sa.Run(t.Context(), queued)
	require.NoError(t, err)

	// The fresh run delivered its todo snapshot on the outbound prompt.
	model.mu.Lock()
	var delivered bool
	for _, call := range model.prompts {
		var userText string
		for _, msg := range call {
			for _, part := range msg.Content {
				if tp, ok := part.(fantasy.TextPart); ok && msg.Role == fantasy.MessageRoleUser {
					userText += tp.Text
				}
			}
		}
		if strings.Contains(userText, "<run_context>") && strings.Contains(userText, "Fixing the cache") {
			delivered = true
		}
	}
	model.mu.Unlock()
	require.True(t, delivered, "the queued turn must carry its todo snapshot on the outbound prompt")

	// No duplicate user message was created by the appended block.
	msgs, err := env.messages.List(t.Context(), sess.ID)
	require.NoError(t, err)
	var userMsgs int
	for _, m := range msgs {
		if m.Role == message.User {
			userMsgs++
		}
	}
	require.Equal(t, 1, userMsgs)
}
