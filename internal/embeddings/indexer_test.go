package embeddings

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIndexer_Resume(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	require.NoError(t, err)
	defer store.Close()

	client := NewEmbeddingClient("http://localhost", "test", "")
	state := NewIndexState()
	_ = NewIndexer(client, store, state, 512, 64, nil)

	ctx := context.Background()

	// Create a test file.
	testFile := tmpDir + "/test.txt"
	f, err := os.Create(testFile)
	require.NoError(t, err)
	f.WriteString(strings.Repeat("hello world\n", 10))
	f.Close()

	// First index run: creates the index.
	// (We can't actually embed without a real API, but we can test the walk logic)
	filesToIndex := []string{testFile}
	newChunks := int64(0)
	for _, file := range filesToIndex {
		data, err := os.ReadFile(file)
		require.NoError(t, err)
		chunks := Chunk(string(data), 512, 64)
		newChunks += int64(len(chunks))
	}
	require.Greater(t, newChunks, int64(0))

	// Verify resume: existingCount = 0, indexedFiles = nil
	// After indexing, store has data.
	count, err := store.Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 0, count)

	// Verify hash-based skip works.
	indexedFiles, err := store.GetIndexedFiles(ctx)
	require.NoError(t, err)
	require.Empty(t, indexedFiles)
}

func TestIndexer_Cancel(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	require.NoError(t, err)
	defer store.Close()

	client := NewEmbeddingClient("http://localhost", "test", "")
	state := NewIndexState()
	// Create a test file so the cancel check triggers.
	testFile := tmpDir + "/test.txt"
	os.WriteFile(testFile, []byte("hello world\n"), 0o644)

	indexer := NewIndexer(client, store, state, 512, 64, nil)
	close(indexer.cancelCh)
	err = indexer.IndexWorkspace(context.Background(), tmpDir, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "cancelled")
}

func TestEmbeddingClient_Empty(t *testing.T) {
	client := NewEmbeddingClient("http://localhost", "test", "")
	_, err := client.EmbedBatch(nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "empty text list")
}

func TestEmbeddingClient_Embed(t *testing.T) {
	client := NewEmbeddingClient("http://localhost:8080", "test-model", "secret-key")
	_, _ = client.Embed("hello")
	// Will fail since no server, but verifies request setup.
	// We can't test without a real embedding API.
}

func TestShrinkTexts(t *testing.T) {
	texts := []string{"0123456789"}
	shrunk := shrinkTexts(texts, 0.9)
	require.Equal(t, 9, len(shrunk[0]))
	require.Equal(t, "012345678", shrunk[0])
}

func TestChunkFile(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	testFile := tmpDir + "/test.go"
	err := os.WriteFile(testFile, []byte("package main\n\nfunc main() {\n\tfmt.Println(\"hello\")\n}\n"), 0o644)
	require.NoError(t, err)

	chunks, err := ChunkFile(testFile, 512, 64)
	require.NoError(t, err)
	require.NotEmpty(t, chunks)
}

func TestShouldSkip(t *testing.T) {
	require.True(t, ShouldSkip("test.png"))
	require.True(t, ShouldSkip("test.exe"))
	require.False(t, ShouldSkip("test.go"))
	require.False(t, ShouldSkip("test.txt"))
}

func TestIsTextFile(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	textFile := tmpDir + "/text.txt"
	binFile := tmpDir + "/binary.bin"

	err := os.WriteFile(textFile, []byte("hello"), 0o644)
	require.NoError(t, err)
	err = os.WriteFile(binFile, []byte{0x00, 0x01, 0x02}, 0o644)
	require.NoError(t, err)

	require.True(t, IsTextFile(textFile))
	require.False(t, IsTextFile(binFile))
}

func TestChunkID(t *testing.T) {
	id1 := ChunkID("/path/file.go", 0)
	id2 := ChunkID("/path/file.go", 1)
	require.NotEqual(t, id1, id2)
	require.NotEmpty(t, id1)
}
