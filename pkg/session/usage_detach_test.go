package session

import (
	"context"
	"testing"
	"time"

	"github.com/hackafterdark/phosphor/pkg/db"
	"github.com/stretchr/testify/require"
)

func toInt64(v any) int64 {
	switch val := v.(type) {
	case int64:
		return val
	case float64:
		return int64(val)
	default:
		return 0
	}
}

// TestTokenUsageSurvivesSessionDeletion verifies that deleting a session no
// longer erases its recorded token usage. The token_usage.session_id foreign
// key uses ON DELETE SET NULL, so the row remains with a NULL session_id and
// the lifetime report totals stay intact while the active totals drop.
func TestTokenUsageSurvivesSessionDeletion(t *testing.T) {
	dataDir := t.TempDir()
	t.Cleanup(func() {
		require.NoError(t, db.Release(dataDir))
		db.ResetPool()
	})

	conn, err := db.Connect(t.Context(), dataDir)
	require.NoError(t, err)

	queries := db.New(conn)
	sessions := NewService(queries, conn)

	created, err := sessions.Create(t.Context(), "detach test")
	require.NoError(t, err)

	err = sessions.RecordTokenUsage(t.Context(), created.ID, "gpt-4", "openai", 1000, 50, 250, 0.01)
	require.NoError(t, err)

	total, err := queries.GetTotalStats(t.Context())
	require.NoError(t, err)
	require.Equal(t, int64(1000), toInt64(total.TotalPromptTokens), "lifetime prompt tokens before delete")
	require.Equal(t, int64(1000), toInt64(total.ActivePromptTokens), "active prompt tokens before delete")
	require.Equal(t, int64(1), total.TotalSessionsWithUsage, "one session with usage before delete")

	require.NoError(t, sessions.Delete(t.Context(), created.ID))

	total, err = queries.GetTotalStats(t.Context())
	require.NoError(t, err)
	require.Equal(t, int64(1000), toInt64(total.TotalPromptTokens), "lifetime prompt tokens survive session deletion")
	require.Equal(t, int64(50), toInt64(total.TotalCompletionTokens), "lifetime completion tokens survive session deletion")
	require.Equal(t, int64(0), toInt64(total.ActivePromptTokens), "active prompt tokens drop after deletion")
	require.Equal(t, int64(0), toInt64(total.ActiveCompletionTokens), "active completion tokens drop after deletion")
	require.Equal(t, int64(0), total.TotalSessionsWithUsage, "no live sessions with usage after deletion")

	var remaining int64
	err = conn.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM token_usage`).Scan(&remaining)
	require.NoError(t, err)
	require.Equal(t, int64(1), remaining, "usage row is retained, not cascaded away")

	var reasoning int64
	require.NoError(t, conn.QueryRowContext(t.Context(), `SELECT COALESCE(SUM(reasoning_tokens), 0) FROM token_usage`).Scan(&reasoning))
	require.Equal(t, int64(250), reasoning, "reasoning tokens are persisted alongside the usage row")

	var orphaned int64
	err = conn.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM token_usage WHERE session_id IS NULL`).Scan(&orphaned)
	require.NoError(t, err)
	require.Equal(t, int64(1), orphaned, "session_id set to NULL by the foreign key")
}

// TestGetUsageByModelSurvivesSessionDeletion verifies the per-model report
// reads token_usage instead of messages so it keeps counting after pruning.
func TestGetUsageByModelSurvivesSessionDeletion(t *testing.T) {
	dataDir := t.TempDir()
	t.Cleanup(func() {
		require.NoError(t, db.Release(dataDir))
		db.ResetPool()
	})

	conn, err := db.Connect(t.Context(), dataDir)
	require.NoError(t, err)

	queries := db.New(conn)
	sessions := NewService(queries, conn)

	created, err := sessions.Create(t.Context(), "model report")
	require.NoError(t, err)
	require.NoError(t, sessions.RecordTokenUsage(t.Context(), created.ID, "gpt-4", "openai", 1000, 50, 0, 0.01))

	rows, err := queries.GetUsageByModel(t.Context())
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "gpt-4", rows[0].Model)
	require.Equal(t, int64(1), rows[0].MessageCount)

	require.NoError(t, sessions.Delete(t.Context(), created.ID))

	rows, err = queries.GetUsageByModel(context.Background())
	require.NoError(t, err)
	require.Len(t, rows, 1, "model usage survives session deletion")
	require.Equal(t, int64(1), rows[0].MessageCount)
}

// TestSubagentUsageDetachedOnParentPrune verifies that pruned sub-agent usage
// stops counting toward the active totals (its session_id is nulled) while its
// lifetime contribution and sub-agent attribution are preserved.
func TestSubagentUsageDetachedOnParentPrune(t *testing.T) {
	dataDir := t.TempDir()
	t.Cleanup(func() {
		require.NoError(t, db.Release(dataDir))
		db.ResetPool()
	})

	conn, err := db.Connect(t.Context(), dataDir)
	require.NoError(t, err)

	queries := db.New(conn)
	sessions := NewService(queries, conn)

	parent, err := sessions.Create(t.Context(), "parent")
	require.NoError(t, err)
	child, err := sessions.CreateTaskSession(t.Context(), "tool-call-1", parent.ID, "task")
	require.NoError(t, err)

	require.NoError(t, sessions.RecordTokenUsage(t.Context(), parent.ID, "gpt-4", "openai", 1000, 100, 0, 0.01))
	require.NoError(t, sessions.RecordTokenUsage(t.Context(), child.ID, "gpt-4", "openai", 500, 50, 0, 0.005))

	// The sub-agent row must be flagged at record time even though it is still
	// attached to a live session.
	var flagged int64
	require.NoError(t, conn.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM token_usage WHERE is_subagent = 1 AND session_id = ?`, child.ID).Scan(&flagged))
	require.Equal(t, int64(1), flagged, "sub-agent row flagged while still attached to its live session")

	total, err := queries.GetTotalStats(t.Context())
	require.NoError(t, err)
	require.Equal(t, int64(1500), toInt64(total.ActivePromptTokens), "both rows active while sessions live")

	count, err := sessions.BulkDeleteSessions(t.Context(), time.Now().Add(24*time.Hour))
	require.NoError(t, err)
	require.Equal(t, 1, count, "only the top-level session is pruned")

	total, err = queries.GetTotalStats(t.Context())
	require.NoError(t, err)
	require.Equal(t, int64(1500), toInt64(total.TotalPromptTokens), "lifetime prompt tokens preserved")
	require.Equal(t, int64(0), toInt64(total.ActivePromptTokens), "sub-agent usage no longer counts as active once its parent is pruned")

	var orphaned int64
	require.NoError(t, conn.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM token_usage WHERE session_id IS NULL`).Scan(&orphaned))
	require.Equal(t, int64(2), orphaned, "parent row detached by the FK and sub-agent row detached by the prune helper")

	var orphanedSubagents int64
	require.NoError(t, conn.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM token_usage WHERE is_subagent = 1 AND session_id IS NULL`).Scan(&orphanedSubagents))
	require.Equal(t, int64(1), orphanedSubagents, "sub-agent attribution survives after its session_id is nulled")
}
