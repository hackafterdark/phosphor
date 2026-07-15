package session

import (
	"context"
	"testing"
	"time"

	"github.com/hackafterdark/phosphor/pkg/db"
	"github.com/stretchr/testify/require"
)

func TestListStatelessSessions(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()
	t.Cleanup(func() {
		require.NoError(t, db.Release(dataDir))
		db.ResetPool()
	})

	conn, err := db.Connect(t.Context(), dataDir)
	require.NoError(t, err)

	svc := NewService(db.New(conn), conn)
	ctx := context.Background()

	// Create a stateless session for openai-api.
	stateless1, err := svc.Create(ctx, "openai stateless 1")
	require.NoError(t, err)
	require.NoError(t, svc.UpdateStateless(ctx, stateless1.ID, true, "openai-api"))

	// Create another stateless session for acp.
	stateless2, err := svc.Create(ctx, "acp stateless 1")
	require.NoError(t, err)
	require.NoError(t, svc.UpdateStateless(ctx, stateless2.ID, true, "acp"))

	// Create a non-stateless session (should not appear).
	_, err = svc.Create(ctx, "regular session")
	require.NoError(t, err)

	// List all stateless sessions.
	all, err := svc.ListStatelessSessions(ctx, "")
	require.NoError(t, err)
	require.Len(t, all, 2)

	// Filter by service.
	openai, err := svc.ListStatelessSessions(ctx, "openai-api")
	require.NoError(t, err)
	require.Len(t, openai, 1)
	require.Equal(t, stateless1.ID, openai[0].ID)

	acp, err := svc.ListStatelessSessions(ctx, "acp")
	require.NoError(t, err)
	require.Len(t, acp, 1)
	require.Equal(t, stateless2.ID, acp[0].ID)
}

func TestCountPrunableMessages(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()
	t.Cleanup(func() {
		require.NoError(t, db.Release(dataDir))
		db.ResetPool()
	})

	conn, err := db.Connect(t.Context(), dataDir)
	require.NoError(t, err)

	q := db.New(conn)
	ctx := context.Background()

	// Create a stateless session.
	stateless, err := q.CreateSession(ctx, db.CreateSessionParams{
		ID:    "test-prune-session",
		Title: "prune test",
	})
	require.NoError(t, err)
	require.NoError(t, q.UpdateStatelessSession(ctx, db.UpdateStatelessSessionParams{
		ID:          stateless.ID,
		IsStateless: 1,
		Service:     "openai-api",
	}))

	// Insert messages directly via db.
	oldMsg1, err := q.CreateMessage(ctx, db.CreateMessageParams{
		ID: "old-1", SessionID: stateless.ID, Role: "user", Parts: "{}",
	})
	require.NoError(t, err)
	// Manually update created_at to be old.
	_, err = conn.ExecContext(ctx, "UPDATE messages SET created_at = ?, updated_at = ? WHERE id = ?",
		time.Now().Add(-48*time.Hour).Unix(), time.Now().Add(-48*time.Hour).Unix(), oldMsg1.ID)
	require.NoError(t, err)

	oldMsg2, err := q.CreateMessage(ctx, db.CreateMessageParams{
		ID: "old-2", SessionID: stateless.ID, Role: "user", Parts: "{}",
	})
	require.NoError(t, err)
	_, err = conn.ExecContext(ctx, "UPDATE messages SET created_at = ?, updated_at = ? WHERE id = ?",
		time.Now().Add(-48*time.Hour).Unix(), time.Now().Add(-48*time.Hour).Unix(), oldMsg2.ID)
	require.NoError(t, err)

	newMsg, err := q.CreateMessage(ctx, db.CreateMessageParams{
		ID: "new-1", SessionID: stateless.ID, Role: "user", Parts: "{}",
	})
	require.NoError(t, err)
	// newMsg already has current timestamp.
	_ = newMsg

	svc := NewService(q, conn)

	// Cutoff at 24h ago.
	cutoff := time.Now().Add(-24 * time.Hour)

	count, err := svc.CountPrunableMessages(ctx, stateless.ID, cutoff)
	require.NoError(t, err)
	require.Equal(t, 2, count)

	// Cutoff in the far future should prune everything.
	farFuture := time.Now().Add(24 * time.Hour)
	count, err = svc.CountPrunableMessages(ctx, stateless.ID, farFuture)
	require.NoError(t, err)
	require.Equal(t, 3, count)
}

func TestPruneMessages(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()
	t.Cleanup(func() {
		require.NoError(t, db.Release(dataDir))
		db.ResetPool()
	})

	conn, err := db.Connect(t.Context(), dataDir)
	require.NoError(t, err)

	q := db.New(conn)
	ctx := context.Background()

	// Create a stateless session.
	stateless, err := q.CreateSession(ctx, db.CreateSessionParams{
		ID:    "test-prune-session",
		Title: "prune test",
	})
	require.NoError(t, err)
	require.NoError(t, q.UpdateStatelessSession(ctx, db.UpdateStatelessSessionParams{
		ID:          stateless.ID,
		IsStateless: 1,
		Service:     "openai-api",
	}))

	// Insert messages directly via db.
	oldMsg1, err := q.CreateMessage(ctx, db.CreateMessageParams{
		ID: "old-1", SessionID: stateless.ID, Role: "user", Parts: "{}",
	})
	require.NoError(t, err)
	_, err = conn.ExecContext(ctx, "UPDATE messages SET created_at = ?, updated_at = ? WHERE id = ?",
		time.Now().Add(-48*time.Hour).Unix(), time.Now().Add(-48*time.Hour).Unix(), oldMsg1.ID)
	require.NoError(t, err)

	oldMsg2, err := q.CreateMessage(ctx, db.CreateMessageParams{
		ID: "old-2", SessionID: stateless.ID, Role: "user", Parts: "{}",
	})
	require.NoError(t, err)
	_, err = conn.ExecContext(ctx, "UPDATE messages SET created_at = ?, updated_at = ? WHERE id = ?",
		time.Now().Add(-48*time.Hour).Unix(), time.Now().Add(-48*time.Hour).Unix(), oldMsg2.ID)
	require.NoError(t, err)

	_, err = q.CreateMessage(ctx, db.CreateMessageParams{
		ID: "new-1", SessionID: stateless.ID, Role: "user", Parts: "{}",
	})
	require.NoError(t, err)

	svc := NewService(q, conn)

	// Cutoff at 24h ago.
	cutoff := time.Now().Add(-24 * time.Hour)

	deleted, err := svc.PruneMessages(ctx, stateless.ID, cutoff)
	require.NoError(t, err)
	require.Equal(t, 2, deleted)

	// Verify only the new message remains.
	remaining, err := q.ListMessagesBySession(ctx, stateless.ID)
	require.NoError(t, err)
	require.Len(t, remaining, 1)
	require.True(t, remaining[0].CreatedAt >= cutoff.Unix())
}

func TestPruneMessagesNoOpWhenNonePrunable(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()
	t.Cleanup(func() {
		require.NoError(t, db.Release(dataDir))
		db.ResetPool()
	})

	conn, err := db.Connect(t.Context(), dataDir)
	require.NoError(t, err)

	q := db.New(conn)
	ctx := context.Background()

	// Create a stateless session with no messages.
	stateless, err := q.CreateSession(ctx, db.CreateSessionParams{
		ID:    "test-empty-prune",
		Title: "empty prune test",
	})
	require.NoError(t, err)
	require.NoError(t, q.UpdateStatelessSession(ctx, db.UpdateStatelessSessionParams{
		ID:          stateless.ID,
		IsStateless: 1,
		Service:     "openai-api",
	}))

	svc := NewService(q, conn)

	cutoff := time.Now().Add(-24 * time.Hour)

	deleted, err := svc.PruneMessages(ctx, stateless.ID, cutoff)
	require.NoError(t, err)
	require.Equal(t, 0, deleted)
}
