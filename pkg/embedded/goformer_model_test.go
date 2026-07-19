package embedded

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// --- Registry Tests (no model loading required) ---

func TestRegistry_GetEmbedding(t *testing.T) {
	registry := DefaultRegistry()

	entry, ok := registry.GetEmbedding("BGE-small-en-v1.5")
	require.True(t, ok)
	require.Equal(t, "BAAI/bge-small-en-v1.5", entry.RepoID)
	require.Equal(t, 384, entry.Dimensions)

	entry2, ok2 := registry.GetEmbedding("BAAI/bge-small-en-v1.5")
	require.True(t, ok2)
	require.Equal(t, entry.RepoID, entry2.RepoID)
}

func TestRegistry_GetEmbedding_NotFound(t *testing.T) {
	registry := DefaultRegistry()
	_, ok := registry.GetEmbedding("nonexistent-model")
	require.False(t, ok)
}

func TestRegistry_ListEmbeddings(t *testing.T) {
	registry := DefaultRegistry()
	entries := registry.ListEmbeddings()
	require.NotEmpty(t, entries)
	require.Equal(t, "BGE-small-en-v1.5", entries[0].Name)
}

func TestRegistry_DownloadEmbedding_NotFound(t *testing.T) {
	registry := DefaultRegistry()
	downloader := NewModelDownloader(t.TempDir())
	_, err := registry.DownloadEmbedding(downloader, "nonexistent")
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown embedding model")
}

// --- Downloader Tests ---

func TestModelDownloader_DownloadModelDirectory_AlreadyExists(t *testing.T) {
	tmpDir := t.TempDir()
	destDir := filepath.Join(tmpDir, "mock-model")
	require.NoError(t, os.MkdirAll(destDir, 0o755))

	files := []string{"config.json", "tokenizer.json", "model.safetensors"}
	for _, fname := range files {
		require.NoError(t, os.WriteFile(filepath.Join(destDir, fname), []byte("mock"), 0o644))
	}

	downloader := NewModelDownloader(tmpDir)
	ctx := context.Background()

	// Pass repo ID whose base matches destDir name so the existence check finds the files.
	path, err := downloader.DownloadModelDirectory(ctx, "some/mock-model", files)
	require.NoError(t, err)
	require.Equal(t, destDir, path)
}

func TestModelDownloader_DownloadModelDirectory(t *testing.T) {
	if os.Getenv("PHOSPHOR_DOWNLOAD_TESTS") != "1" {
		t.Skip("skipping download test; set PHOSPHOR_DOWNLOAD_TESTS=1 to run")
	}
	if os.Getenv("HF_TOKEN") == "" {
		t.Skip("skipping: HF_TOKEN not set")
	}

	tmpDir := t.TempDir()
	downloader := NewModelDownloader(tmpDir)

	registry := DefaultRegistry()
	entry, ok := registry.GetEmbedding("BGE-small-en-v1.5")
	require.True(t, ok)

	ctx := context.Background()
	path, err := downloader.DownloadModelDirectory(ctx, entry.RepoID, entry.Files)
	require.NoError(t, err)

	for _, fname := range entry.Files {
		_, err := os.Stat(filepath.Join(path, fname))
		require.NoError(t, err, "file %s not found", fname)
	}
}

// --- GoformerModel Tests (require actual model download) ---

func TestNewGoformerModel_Singleton(t *testing.T) {
	if os.Getenv("PHOSPHOR_DOWNLOAD_TESTS") != "1" {
		t.Skip("skipping model loading test; set PHOSPHOR_DOWNLOAD_TESTS=1 to run")
	}
	if os.Getenv("HF_TOKEN") == "" {
		t.Skip("skipping: HF_TOKEN not set")
	}

	ResetGoformerInstance()

	tmpDir := t.TempDir()
	downloader := NewModelDownloader(tmpDir)
	registry := DefaultRegistry()
	entry, ok := registry.GetEmbedding("BGE-small-en-v1.5")
	require.True(t, ok)

	modelPath, err := downloader.DownloadModelDirectory(context.Background(), entry.RepoID, entry.Files)
	require.NoError(t, err)

	model1, err := NewGoformerModel(modelPath)
	require.NoError(t, err)
	model2, err := NewGoformerModel(modelPath)
	require.NoError(t, err)

	// Both calls return wrappers around the same singleton instance.
	require.Same(t, model1.model, model2.model)
}

func TestGoformerModel_Embed(t *testing.T) {
	if os.Getenv("PHOSPHOR_DOWNLOAD_TESTS") != "1" {
		t.Skip("skipping model loading test; set PHOSPHOR_DOWNLOAD_TESTS=1 to run")
	}
	if os.Getenv("HF_TOKEN") == "" {
		t.Skip("skipping: HF_TOKEN not set")
	}

	ResetGoformerInstance()

	tmpDir := t.TempDir()
	downloader := NewModelDownloader(tmpDir)
	registry := DefaultRegistry()
	entry, ok := registry.GetEmbedding("BGE-small-en-v1.5")
	require.True(t, ok)

	modelPath, err := downloader.DownloadModelDirectory(context.Background(), entry.RepoID, entry.Files)
	require.NoError(t, err)

	model, err := NewGoformerModel(modelPath)
	require.NoError(t, err)

	embedding, err := model.Embed("hello world")
	require.NoError(t, err)
	require.NotEmpty(t, embedding)
	require.Equal(t, model.Dims(), len(embedding))
}

func TestGoformerModel_EmbedBatch(t *testing.T) {
	if os.Getenv("PHOSPHOR_DOWNLOAD_TESTS") != "1" {
		t.Skip("skipping model loading test; set PHOSPHOR_DOWNLOAD_TESTS=1 to run")
	}
	if os.Getenv("HF_TOKEN") == "" {
		t.Skip("skipping: HF_TOKEN not set")
	}

	ResetGoformerInstance()

	tmpDir := t.TempDir()
	downloader := NewModelDownloader(tmpDir)
	registry := DefaultRegistry()
	entry, ok := registry.GetEmbedding("BGE-small-en-v1.5")
	require.True(t, ok)

	modelPath, err := downloader.DownloadModelDirectory(context.Background(), entry.RepoID, entry.Files)
	require.NoError(t, err)

	model, err := NewGoformerModel(modelPath)
	require.NoError(t, err)

	texts := []string{"first sentence", "second sentence", "third"}
	embeddings, err := model.EmbedBatch(texts)
	require.NoError(t, err)
	require.Equal(t, len(texts), len(embeddings))
	for i, emb := range embeddings {
		require.Equal(t, model.Dims(), len(emb), "embedding %d has wrong length", i)
	}
}

func TestGoformerModel_ResetInstance(t *testing.T) {
	if os.Getenv("PHOSPHOR_DOWNLOAD_TESTS") != "1" {
		t.Skip("skipping model loading test; set PHOSPHOR_DOWNLOAD_TESTS=1 to run")
	}
	if os.Getenv("HF_TOKEN") == "" {
		t.Skip("skipping: HF_TOKEN not set")
	}

	ResetGoformerInstance()

	tmpDir := t.TempDir()
	downloader := NewModelDownloader(tmpDir)
	registry := DefaultRegistry()
	entry, ok := registry.GetEmbedding("BGE-small-en-v1.5")
	require.True(t, ok)

	modelPath, err := downloader.DownloadModelDirectory(context.Background(), entry.RepoID, entry.Files)
	require.NoError(t, err)

	ResetGoformerInstance()
	model2, err := NewGoformerModel(modelPath)
	require.NoError(t, err)
	require.NotNil(t, model2)
}