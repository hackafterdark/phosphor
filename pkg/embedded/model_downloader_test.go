package embedded

import (
	"context"
	"os"
	"testing"
)

func TestDownloadModel(t *testing.T) {
	if os.Getenv("PHOSPHOR_DOWNLOAD_TESTS") != "1" {
		t.Skip("skipping download test; set PHOSPHOR_DOWNLOAD_TESTS=1 to run")
	}

	// Use a temporary directory for modelsDir so we don't pollute the system
	tmpDir, err := os.MkdirTemp("", "phosphor-models-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	downloader := NewModelDownloader(tmpDir)
	t.Logf("Downloading to models dir: %s", downloader.ModelsDir())

	// Let's try downloading a very small file or checking registry
	// "Qwen/Qwen3.5-0.8B", filename: "Qwen3.5-0.8B.Q4_K_M.gguf" is too big for a quick test,
	// but we can try a very small repo/file or mock it/test HF connectivity.
	// Actually, let's see if we can resolve/download a small file like a README or config.json from a HF repo.
	ctx := context.Background()
	path, err := downloader.DownloadModel(ctx, "hf-internal-testing/tiny-random-gpt2", "config.json")
	if err != nil {
		t.Fatalf("failed to download: %v", err)
	}

	t.Logf("Downloaded path: %s", path)
	if _, err := os.Stat(path); err != nil {
		t.Errorf("file does not exist at dest path: %v", err)
	}
}

func TestResolveAndDownload(t *testing.T) {
	if os.Getenv("PHOSPHOR_DOWNLOAD_TESTS") != "1" {
		t.Skip("skipping download test; set PHOSPHOR_DOWNLOAD_TESTS=1 to run")
	}

	tmpDir, err := os.MkdirTemp("", "phosphor-models-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	downloader := NewModelDownloader(tmpDir)

	ctx := context.Background()

	// Test case: Unknown model repo ID (fallback behaviour)
	// We download from a tiny test repo. Because the repo ID lacks .gguf,
	// ResolveAndDownload will append .gguf. Let's make sure it successfully downloads.
	// Wait, the tiny-random-gpt2 repo doesn't have a "tiny-random-gpt2.gguf" file.
	// But it has "config.json". Let's test with a repo and custom filename directly if needed,
	// or we can test ResolveAndDownload with a mock registry or by mocking.
	// Wait, we can define a temporary Registry entry with a real small file to download!
	// E.g., repoID "hf-internal-testing/tiny-random-gpt2", filename "config.json".
	customRegistry := &Registry{
		entries: []ModelEntry{
			{
				Name:     "tiny-gpt2",
				RepoID:   "hf-internal-testing/tiny-random-gpt2",
				Filename: "config.json",
			},
		},
	}

	path, err := ResolveAndDownload(ctx, downloader, customRegistry, "tiny-gpt2")
	if err != nil {
		t.Fatalf("failed to resolve and download: %v", err)
	}

	t.Logf("Resolved downloaded path: %s", path)
	if _, err := os.Stat(path); err != nil {
		t.Errorf("file does not exist: %v", err)
	}
}
