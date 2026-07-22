package embeddings

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func makeTestVec(offset float32) []float32 {
	v := make([]float32, 384)
	for i := range v {
		v[i] = float32(i)*0.01 + offset
	}
	return v
}

func TestStore_Search(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	vecA := makeTestVec(0)
	vecB := makeTestVec(3.83)
	vecC := makeTestVec(0.005)

	require.NoError(t, store.Insert(ctx, "/file_a.go", "hash_a", "content A", 0, 100, vecA))
	require.NoError(t, store.Insert(ctx, "/file_b.go", "hash_b", "content B", 0, 200, vecB))
	require.NoError(t, store.Insert(ctx, "/file_c.go", "hash_c", "content C", 0, 150, vecC))

	query := makeTestVec(0.0025)
	results, err := store.Search(ctx, query, 2)
	require.NoError(t, err)
	require.Len(t, results, 2)

	require.Equal(t, "/file_c.go", results[0].FilePath)
	require.Equal(t, "/file_a.go", results[1].FilePath)
}

func TestStore_Resume(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	require.NoError(t, err)

	ctx := context.Background()

	vec := makeTestVec(0)
	require.NoError(t, store.Insert(ctx, "/file_a.go", "hash_a", "content A", 0, 100, vec))

	count, err := store.Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, count)

	store.Close()

	store2, err := NewStore(tmpDir)
	require.NoError(t, err)
	defer store2.Close()

	count2, err := store2.Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, count2)

	hash, err := store2.GetFileHash(ctx, "/file_a.go")
	require.NoError(t, err)
	require.NotEmpty(t, hash)
}

func TestStore_Clear(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()

	vec := makeTestVec(0)
	require.NoError(t, store.Insert(ctx, "/file.go", "hash", "content", 0, 50, vec))

	count, err := store.Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, count)

	require.NoError(t, store.Clear(ctx))

	count2, err := store.Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 0, count2)
}

func TestStore_Backfill(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()

	vec := makeTestVec(0)
	require.NoError(t, store.Insert(ctx, "/file.go", "hash", "content", 0, 50, vec))

	require.NoError(t, store.backfillHashes(ctx))

	hash, err := store.GetFileHash(ctx, "/file.go")
	require.NoError(t, err)
	require.NotEmpty(t, hash)
}

func TestContentHash(t *testing.T) {
	h := ContentHash([]byte("hello world"))
	require.NotEmpty(t, h)
	require.Equal(t, 64, len(h))
}

func TestChunk(t *testing.T) {
	chunks := Chunk("0123456789", 4, 2)
	require.Equal(t, 5, len(chunks))
}

func TestNewStore_CreatesDirectory(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	require.NoError(t, err)
	defer store.Close()

	dbPath := tmpDir + "/.phosphor/codebase_index.db"
	_, err = os.Stat(dbPath)
	require.NoError(t, err)
}

func TestStore_Search_Query(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()

	vecA := makeTestVec(0)
	vecB := makeTestVec(3.83)

	require.NoError(t, store.Insert(ctx, "/file_a.go", "hash_a", "content A", 0, 100, vecA))
	require.NoError(t, store.Insert(ctx, "/file_b.go", "hash_b", "content B", 0, 200, vecB))

	query := makeTestVec(0.0025)
	results, err := store.Search(ctx, query, 2)
	require.NoError(t, err)
	require.Len(t, results, 2)

	for _, r := range results {
		require.NotEmpty(t, r.FilePath)
		require.NotEmpty(t, r.Content)
		require.NotEmpty(t, r.ID)
	}
}

func TestStore_GetFileHash(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()

	vec := makeTestVec(0)
	require.NoError(t, store.Insert(ctx, "/test.go", "abc123", "content", 0, 100, vec))

	hash, err := store.GetFileHash(ctx, "/test.go")
	require.NoError(t, err)
	require.NotEmpty(t, hash)

	hash2, err := store.GetFileHash(ctx, "/nonexistent.go")
	require.NoError(t, err)
	require.Empty(t, hash2)
}

func TestStore_IsFileIndexed(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()

	vec := makeTestVec(0)
	require.NoError(t, store.Insert(ctx, "/test.go", "hash", "content", 0, 100, vec))

	indexed, err := store.IsFileIndexed(ctx, "/test.go")
	require.NoError(t, err)
	require.True(t, indexed)

	indexed2, err := store.IsFileIndexed(ctx, "/notfound.go")
	require.NoError(t, err)
	require.False(t, indexed2)
}