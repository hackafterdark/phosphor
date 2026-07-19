package embedded

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/gomlx/go-huggingface/hub"
)

// ModelDownloader handles downloading models from HuggingFace.
type ModelDownloader struct {
	mu        sync.Mutex
	paths     map[string]string
	downloads map[string]bool
	modelsDir string // global models directory (not per-workspace)
}

// NewModelDownloader creates a new downloader instance.
// If modelsDir is empty, defaults to the global Phosphor data directory.
func NewModelDownloader(modelsDir string) *ModelDownloader {
	if modelsDir == "" {
		modelsDir = getGlobalModelsDir()
	}
	return &ModelDownloader{
		paths:     make(map[string]string),
		downloads: make(map[string]bool),
		modelsDir: modelsDir,
	}
}

// getGlobalModelsDir returns the cross-platform global models directory.
func getGlobalModelsDir() string {
	globalData := os.Getenv("PHOSPHOR_GLOBAL_DATA")
	if globalData != "" {
		return filepath.Join(globalData, "models")
	}

	xdg := os.Getenv("XDG_DATA_HOME")
	if xdg != "" {
		return filepath.Join(xdg, "phosphor", "models")
	}

	// Windows: %LOCALAPPDATA%/phosphor/models
	localAppData := os.Getenv("LOCALAPPDATA")
	if localAppData != "" {
		return filepath.Join(localAppData, "phosphor", "models")
	}

	// Fallback: ~/.phosphor/models
	homeDir := os.Getenv("HOME")
	if homeDir == "" {
		homeDir = os.Getenv("USERPROFILE")
	}
	if homeDir != "" {
		return filepath.Join(homeDir, ".phosphor", "models")
	}
	return ".phosphor/models"
}

// DownloadModel downloads a model from HuggingFace to the global models directory.
// If the model already exists locally, it returns the cached path.
func (d *ModelDownloader) DownloadModel(ctx context.Context, repoID, filename string) (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	modelKey := repoID + ":" + filename

	if path, ok := d.paths[modelKey]; ok {
		return path, nil
	}

	destPath := filepath.Join(d.modelsDir, filename)

	if _, err := os.Stat(destPath); err == nil {
		d.paths[modelKey] = destPath
		return destPath, nil
	}

	slog.Info("Downloading model from HuggingFace", "repo", repoID, "file", filename)

	hfCacheDir := filepath.Join(d.modelsDir, "hf-cache")
	repo := hub.New(repoID).WithCacheDir(hfCacheDir)
	if token := os.Getenv("HF_TOKEN"); token != "" {
		repo.WithAuth(token)
	}

	downloadedPath, err := repo.DownloadFile(filename)
	if err != nil {
		return "", fmt.Errorf("failed to download model %s/%s: %w", repoID, filename, err)
	}

	if err := os.MkdirAll(d.modelsDir, 0o755); err != nil {
		return "", fmt.Errorf("failed to create models directory: %w", err)
	}

	if downloadedPath != destPath {
		if err := copyFile(downloadedPath, destPath); err != nil {
			return "", fmt.Errorf("failed to copy model to %s: %w", destPath, err)
		}
	}

	d.paths[modelKey] = destPath
	d.downloads[modelKey] = true
	slog.Info("Model downloaded successfully", "path", destPath)
	return destPath, nil
}

// GetLocalModelPath returns the cached path for a previously downloaded model.
func (d *ModelDownloader) GetLocalModelPath(repoID, filename string) (string, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	path, ok := d.paths[repoID+":"+filename]
	return path, ok
}

// ModelsDir returns the target models directory.
func (d *ModelDownloader) ModelsDir() string {
	return d.modelsDir
}

// copyFile copies a file from src to dst.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}

// DownloadModelDirectory downloads all files for an embedding model directory.
// Safetensors models consist of multiple files (config.json, tokenizer.json,
// model.safetensors, etc.) that must all live in the same directory.
func (d *ModelDownloader) DownloadModelDirectory(ctx context.Context, repoID string, filenames []string) (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	dirName := filepath.Base(repoID)
	if dirName == "" {
		dirName = repoID
	}
	destDir := filepath.Join(d.modelsDir, dirName)

	// Check if directory already exists with all files present.
	exists := true
	for _, fname := range filenames {
		if _, err := os.Stat(filepath.Join(destDir, fname)); os.IsNotExist(err) {
			exists = false
			break
		}
	}
	if exists {
		return destDir, nil
	}

	slog.Info("Downloading embedding model directory from HuggingFace", "repo", repoID)

	hfCacheDir := filepath.Join(d.modelsDir, "hf-cache")
	repo := hub.New(repoID).WithCacheDir(hfCacheDir)
	if token := os.Getenv("HF_TOKEN"); token != "" {
		repo.WithAuth(token)
	}

	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return "", fmt.Errorf("failed to create model directory %s: %w", destDir, err)
	}

	for _, fname := range filenames {
		downloadedPath, err := repo.DownloadFile(fname)
		if err != nil {
			return "", fmt.Errorf("failed to download %s from %s: %w", fname, repoID, err)
		}
		destPath := filepath.Join(destDir, fname)
		if downloadedPath != destPath {
			if err := copyFile(downloadedPath, destPath); err != nil {
				return "", fmt.Errorf("failed to copy %s to %s: %w", fname, destPath, err)
			}
		}
	}

	slog.Info("Embedding model directory downloaded", "path", destDir)
	return destDir, nil
}

// ResolveAndDownload resolves the repoID against the model registry.
// If the repoID matches a model in the registry (by name or repo ID), it downloads that model.
// If it's not in the registry, it falls back to repoID and a derived filename.
func ResolveAndDownload(ctx context.Context, downloader *ModelDownloader, registry *Registry, repoID string) (string, error) {
	if registry == nil {
		registry = DefaultRegistry()
	}
	if entry, ok := registry.Get(repoID); ok {
		return downloader.DownloadModel(ctx, entry.RepoID, entry.Filename)
	}

	// Fallback: derive filename from the last part of repoID.
	filename := filepath.Base(repoID)
	if !strings.HasSuffix(filename, ".gguf") {
		filename += ".gguf"
	}
	return downloader.DownloadModel(ctx, repoID, filename)
}
