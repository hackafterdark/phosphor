// Package embeddings provides codebase indexing with progress tracking.
package embeddings

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"

	"github.com/hackafterdark/phosphor/pkg/pubsub"
)

// Indexer walks a workspace, chunks files, embeds them, and tracks progress.
type Indexer struct {
	client       *EmbeddingClient
	store        *Store
	state        *IndexState
	maxChunkSize int
	chunkOverlap int
	broker       *pubsub.Broker[IndexProgress]
	cancelCh     chan struct{}
	processed    atomic.Int64
	totalChunks  atomic.Int64
}

// IndexProgress is the pub/sub event type for indexer progress.
type IndexProgress struct {
	Status        IndexStatus
	ChunksIndexed int
	TotalChunks   int
	FileName      string
	Error         string
}

// NewIndexer creates an indexer with the given client, store, and state.
func NewIndexer(client *EmbeddingClient, store *Store, state *IndexState, maxChunkSize, chunkOverlap int, broker *pubsub.Broker[IndexProgress]) *Indexer {
	return &Indexer{
		client:       client,
		store:        store,
		state:        state,
		maxChunkSize: maxChunkSize,
		chunkOverlap: chunkOverlap,
		broker:       broker,
		cancelCh:     make(chan struct{}),
	}
}

// IndexWorkspace walks the workspace directory and indexes all text files.
// Uses content-based change detection: only files with changed content or
// files not yet indexed will be processed. Progress resumes from existing
// chunks in the store, so quitting mid-index and restarting will pick up
// from where it left off.
func (i *Indexer) IndexWorkspace(ctx context.Context, rootDir string, excludedPatterns []string) error {
	// Migrate schema: ensure vec_chunks has id TEXT column.
	i.store.Migrate(ctx)

	// Check what's already indexed so we can resume.
	existingCount, err := i.store.Count(ctx)
	if err != nil {
		slog.Warn("Failed to count existing chunks, starting fresh", "error", err)
		existingCount = 0
	}

	// Load ignore patterns from .gitignore and .phosphorignore.
	ignorePatterns := loadIgnorePatterns(rootDir)
	// Merge user-provided excluded paths.
	allPatterns := append(ignorePatterns, excludedPatterns...)

	// Always skip these directories.
	skipDirs := map[string]bool{
		".git": true, ".phosphor": true, "node_modules": true, "vendor": true, "__pycache__": true,
	}

	// Single pass: collect files to index with their content hashes,
	// comparing against stored hashes to detect changes.
	var filesToIndex []string
	var newChunks int64

	walkErr := filepath.Walk(rootDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			name := info.Name()
			if skipDirs[name] {
				return filepath.SkipDir
			}
			return nil
		}
		if ShouldSkip(path) || !IsTextFile(path) {
			return nil
		}
		relPath, err := filepath.Rel(rootDir, path)
		if err == nil && isIgnored(relPath, allPatterns) {
			return nil
		}

		// Read file and compute content hash.
		data, err := os.ReadFile(path)
		if err != nil {
			slog.Warn("Failed to read file", "path", path, "error", err)
			return nil
		}
		currentHash := ContentHash(data)

		// Check if file was previously indexed and whether content changed.
		storedHash, err := i.store.GetFileHash(ctx, path)
		if err != nil {
			slog.Warn("Failed to get stored hash", "path", path, "error", err)
		}

		// Skip if content hash matches stored hash.
		if storedHash != "" && storedHash == currentHash {
			return nil
		}

		// Fallback: if file is already indexed but has no stored hash (pre-hash data),
		// skip it since its chunks are already in the store.
		if storedHash == "" {
			isIndexed, err := i.store.IsFileIndexed(ctx, path)
			if err == nil && isIndexed {
				return nil
			}
		}

		chunks := Chunk(string(data), i.maxChunkSize, i.chunkOverlap)
		newChunks += int64(len(chunks))
		filesToIndex = append(filesToIndex, path)
		return nil
	})
	if walkErr != nil {
		i.state.Update(IndexStatusError, 0, 0, walkErr.Error())
		return fmt.Errorf("walk failed: %w", walkErr)
	}

	// Total chunks = already indexed + new chunks to index.
	total := int64(existingCount) + newChunks
	i.processed.Store(int64(existingCount))
	i.totalChunks.Store(total)
	i.state.Update(IndexStatusIndexing, int(i.processed.Load()), int(i.totalChunks.Load()), "")

	// Process each file that needs indexing.
	batchSize := 32
	for _, file := range filesToIndex {
		select {
		case <-i.cancelCh:
			i.state.Update(IndexStatusIdle, int(i.processed.Load()), int(i.totalChunks.Load()), "cancelled")
			return fmt.Errorf("cancelled")
		default:
		}

		data, err := os.ReadFile(file)
		if err != nil {
			slog.Warn("Failed to read file", "file", file, "error", err)
			continue
		}
		contentHash := ContentHash(data)
		chunks := Chunk(string(data), i.maxChunkSize, i.chunkOverlap)

		for j := 0; j < len(chunks); j += batchSize {
			end := min(j+batchSize, len(chunks))
			batch := chunks[j:end]

			vectors, err := i.client.EmbedBatch(batch)
			if err != nil {
				i.state.Update(IndexStatusError, int(i.processed.Load()), int(i.totalChunks.Load()), err.Error())
				return err
			}

for k, vector := range vectors {
			idx := j + k
			chunk := batch[k]
			err := i.store.Insert(context.Background(), file, contentHash, chunk, idx*i.maxChunkSize, len(chunk), vector)
			if err != nil {
				slog.Warn("Failed to store embedding", "file", file, "chunk", idx, "error", err)
				continue
			}
		}

			i.processed.Add(int64(end - j))
			i.state.Update(IndexStatusIndexing, int(i.processed.Load()), int(i.totalChunks.Load()), "")

			if i.broker != nil {
				i.broker.Publish(pubsub.UpdatedEvent, IndexProgress{
					Status:        IndexStatusIndexing,
					ChunksIndexed: int(i.processed.Load()),
					TotalChunks:   int(i.totalChunks.Load()),
					FileName:      file,
				})
			}
		}
	}

	i.state.Update(IndexStatusComplete, int(i.processed.Load()), int(i.totalChunks.Load()), "")
	return nil
}

// State returns the indexer's progress state.
func (i *Indexer) State() *IndexState {
	return i.state
}

// loadIgnorePatterns reads .gitignore and .phosphorignore from the workspace root
// and returns the combined list of glob patterns.
func loadIgnorePatterns(rootDir string) []string {
	var patterns []string
	for _, file := range []string{".gitignore", ".phosphorignore", ".phosphorindexignore"} {
		path := filepath.Join(rootDir, file)
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			// Skip comments and empty lines.
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			// Handle negation: patterns starting with "!" are inclusion patterns,
			// but for our purposes we only care about exclusion patterns.
			if strings.HasPrefix(line, "!") {
				continue
			}
			// Remove leading/trailing whitespace handled by TrimSpace above.
			patterns = append(patterns, line)
		}
		f.Close()
	}
	return patterns
}

// isIgnored returns true if the relative path matches any of the given glob patterns.
func isIgnored(relPath string, patterns []string) bool {
	for _, pattern := range patterns {
		// Handle negation of directory patterns (e.g. "!/foo" means "don't ignore /foo").
		// For simplicity, we skip negation patterns (they're inclusion overrides).
		if strings.HasPrefix(pattern, "!") {
			continue
		}
		// Remove leading slash for matching.
		matchPattern := strings.TrimPrefix(pattern, "/")
		matched, err := filepath.Match(matchPattern, relPath)
		if err != nil {
			continue
		}
		if matched {
			return true
		}
		// Also check if the pattern matches any parent directory.
		dir := relPath
		for {
			matched, err := filepath.Match(matchPattern, dir)
			if err != nil {
				break
			}
			if matched {
				return true
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	return false
}