package tools

import (
	"context"
	"testing"

	"charm.land/fantasy"
	"github.com/hackafterdark/phosphor/pkg/db"
	"github.com/hackafterdark/phosphor/pkg/session"
	"github.com/stretchr/testify/require"
)

// TestTodosToolResponseCarriesOpenList pins the prompt-cache P1 contract:
// the freshest open todo list rides the tool result instead of the system
// prompt, so the transcript stays append-only.
func TestTodosToolResponseCarriesOpenList(t *testing.T) {
	t.Parallel()
	conn, err := db.Connect(t.Context(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { conn.Close() })
	sessions := session.NewService(db.New(conn), conn)
	sess, err := sessions.Create(t.Context(), "todos-response")
	require.NoError(t, err)

	tool := NewTodosTool(sessions)
	ctx := context.WithValue(t.Context(), SessionIDContextKey, sess.ID)
	resp, err := tool.Run(ctx, fantasy.ToolCall{
		ID:   "call-1",
		Name: TodosToolName,
		Input: `{"todos":[` +
			`{"content":"Fix the cache","status":"in_progress","active_form":"Fixing the cache"},` +
			`{"content":"All done","status":"completed","active_form":"Done"}]}`,
	})
	require.NoError(t, err)
	require.Contains(t, resp.Content, "<todo_list>")
	require.Contains(t, resp.Content, "Fixing the cache")
	require.NotContains(t, resp.Content, "All done")
}
