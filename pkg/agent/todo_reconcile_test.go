package agent

import (
	"testing"

	"github.com/hackafterdark/phosphor/pkg/session"
	"github.com/stretchr/testify/require"
)

// TestReconcileStaleTodosDemotesInProgress verifies that a todo left
// in_progress when a run ends is demoted to pending and persisted, so the
// UI and future prompts never see stale active work.
func TestReconcileStaleTodosDemotesInProgress(t *testing.T) {
	t.Parallel()
	env := testEnv(t)
	sess, err := env.sessions.Create(t.Context(), "reconcile")
	require.NoError(t, err)
	sess.Todos = []session.Todo{
		{Content: "Done thing", Status: session.TodoStatusCompleted},
		{Content: "Stuck thing", Status: session.TodoStatusInProgress, ActiveForm: "Stucking it"},
		{Content: "Queued thing", Status: session.TodoStatusPending},
	}
	_, err = env.sessions.Save(t.Context(), sess)
	require.NoError(t, err)

	sa := testSessionAgent(env, nil, nil, "system").(*sessionAgent)
	sa.reconcileStaleTodos(t.Context(), sess.ID)

	got, err := env.sessions.Get(t.Context(), sess.ID)
	require.NoError(t, err)
	require.Equal(t, session.TodoStatusCompleted, got.Todos[0].Status)
	require.Equal(t, session.TodoStatusPending, got.Todos[1].Status)
	require.Equal(t, session.TodoStatusPending, got.Todos[2].Status)
}

// TestReconcileStaleTodosLeavesCleanListAlone verifies a session with no
// in_progress todos is not modified or rewritten.
func TestReconcileStaleTodosLeavesCleanListAlone(t *testing.T) {
	t.Parallel()
	env := testEnv(t)
	sess, err := env.sessions.Create(t.Context(), "reconcile-clean")
	require.NoError(t, err)
	sess.Todos = []session.Todo{
		{Content: "Done thing", Status: session.TodoStatusCompleted},
		{Content: "Queued thing", Status: session.TodoStatusPending},
	}
	_, err = env.sessions.Save(t.Context(), sess)
	require.NoError(t, err)

	sa := testSessionAgent(env, nil, nil, "system").(*sessionAgent)
	sa.reconcileStaleTodos(t.Context(), sess.ID)

	got, err := env.sessions.Get(t.Context(), sess.ID)
	require.NoError(t, err)
	require.Equal(t, sess.Todos, got.Todos)
}
