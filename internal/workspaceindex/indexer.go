// Package workspaceindex provides FTS5-based workspace search.
package workspaceindex

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
)

// Indexer walks a workspace, extracts symbols and documents, and stores them in FTS5.
type Indexer struct {
	store           *Store
	skipDirs        map[string]bool
	excludePatterns []string
	maxFileSize     int  // 0 means unlimited
	maxConcurrent   int  // 0 means derive from NumCPU
	yieldEvery      int  // 0 means derive a default
	indexDocuments  bool // when true, office documents are extracted into docs_fts
}

// SetLimits configures the build's concurrency and cooperative-yield
// cadence. Zero values fall back to safe defaults. Call before IndexWorkspace.
func (i *Indexer) SetLimits(maxConcurrent, yieldEvery int) {
	i.maxConcurrent = maxConcurrent
	i.yieldEvery = yieldEvery
}

// SetIndexDocuments controls whether binary office documents (PDF, DOCX,
// XLSX, PPTX) are read and run through the document converter into docs_fts.
// It defaults to true; call with false to treat those files as opaque binary
// and skip them. Call before IndexWorkspace.
func (i *Indexer) SetIndexDocuments(enabled bool) {
	i.indexDocuments = enabled
}

// NewIndexer creates a new indexer with the given store and max file size.
func NewIndexer(store *Store, maxFileSize int) *Indexer {
	return &Indexer{
		store: store,
		skipDirs: map[string]bool{
			".git": true, ".phosphor": true, "node_modules": true,
			"vendor": true, "__pycache__": true, ".DS_Store": true,
		},
		maxFileSize:    maxFileSize,
		indexDocuments: true,
	}
}

// IndexWorkspace walks the workspace directory and indexes all files.
//
// It runs in three phases: a quick walk collects the candidate files so the
// store can report a real total, then a bounded worker pool indexes them
// while yielding the processor back to the scheduler periodically, and
// finally a reconcile pass removes any rows whose file the walk did not
// visit -- which is what drops deleted files and files that have become
// ignored since the last build. The bounded pool and the cooperative yields
// are what keep the TUI input loop responsive during a large first-time
// build; the single-writer store (see Store) keeps the concurrent inserts
// serialized.
func (i *Indexer) IndexWorkspace(ctx context.Context, rootDir string, excludePatterns []string) error {
	// Load ignore patterns from .gitignore, .phosphorignore, and
	// .phosphorindexignore, then merge user-provided exclude patterns.
	ignorePatterns := loadIgnorePatterns(rootDir)
	i.excludePatterns = append(ignorePatterns, excludePatterns...)

	var files []string
	// keep records the relative paths the walk considered indexable, so the
	// reconcile pass can tell visited files apart from ones that have since
	// been deleted or moved behind an ignore rule.
	keep := make(map[string]bool)
	walkErr := filepath.WalkDir(rootDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if i.skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		relPath, err := filepath.Rel(rootDir, path)
		if err != nil {
			return err
		}
		if isExcluded(relPath, i.excludePatterns) {
			return nil
		}
		keep[relPath] = true
		files = append(files, path)
		return nil
	})
	if walkErr != nil {
		i.store.FailBuild(ctx, walkErr.Error())
		return walkErr
	}

	i.store.BeginBuild(len(files))

	workers := i.maxConcurrent
	if workers <= 0 {
		workers = min(max(runtime.NumCPU()-1, 1), 4)
	}
	yieldEvery := i.yieldEvery
	if yieldEvery <= 0 {
		yieldEvery = 64
	}

	var (
		jobs      = make(chan string)
		processed atomic.Int64
		errMu     sync.Mutex
		firstErr  error
		wg        sync.WaitGroup
	)
	setErr := func(e error) {
		errMu.Lock()
		defer errMu.Unlock()
		if firstErr == nil {
			firstErr = e
		}
	}
	getErr := func() error {
		errMu.Lock()
		defer errMu.Unlock()
		return firstErr
	}

	for range workers {
		wg.Go(func() {
			for path := range jobs {
				if ctx.Err() != nil {
					continue
				}
				if err := i.processFile(ctx, rootDir, path); err != nil {
					setErr(err)
				}
				count := int(processed.Add(1))
				if count%yieldEvery == 0 {
					runtime.Gosched()
					i.store.UpdateBuildProgress(count, path)
				}
			}
		})
	}

	for _, path := range files {
		select {
		case jobs <- path:
		case <-ctx.Done():
		}
	}
	close(jobs)
	wg.Wait()

	if ctx.Err() != nil {
		i.store.FailBuild(ctx, ctx.Err().Error())
		return ctx.Err()
	}
	if err := getErr(); err != nil {
		i.store.FailBuild(ctx, err.Error())
		return err
	}
	// Reconcile: the walk is the source of truth for what should be indexed,
	// so any previously-indexed path it did not visit has been deleted or
	// newly excluded and its stale rows must go. Skipped implicitly when the
	// walk errored (we already returned) so a partial walk cannot wipe the
	// index.
	if removed, err := i.store.PruneNotIndexed(ctx, keep); err != nil {
		slog.Warn("Workspace index reconciliation failed", "error", err)
	} else if removed > 0 {
		slog.Info("Workspace index reconciled removed files", "count", removed)
	}
	i.store.FinishBuild(ctx, int(processed.Load()))
	return nil
}

// loadIgnorePatterns reads .gitignore, .phosphorignore, and
// .phosphorindexignore from the workspace root and returns the
// combined list of glob patterns.
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
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			if strings.HasPrefix(line, "!") {
				continue
			}
			patterns = append(patterns, line)
		}
		if err := scanner.Err(); err != nil {
			slog.Warn("Failed to read ignore file", "file", path, "error", err)
		}
		f.Close()
	}
	return patterns
}

// processFile hashes, extracts, and indexes a single file.
func (i *Indexer) processFile(ctx context.Context, rootDir, path string) error {
	// Check file size limit.
	if i.maxFileSize > 0 {
		info, err := os.Stat(path)
		if err != nil {
			return fmt.Errorf("stat file %s: %w", path, err)
		}
		if info.Size() > int64(i.maxFileSize) {
			return nil
		}
	}

	// Skip binary files. When document indexing is enabled, let convertible
	// office formats (PDF, DOCX, XLSX, PPTX) through to the converter below so
	// their extracted text lands in docs_fts; every other binary file - and
	// every binary file while the feature is off - is skipped so its raw bytes
	// never reach the search index.
	ext := strings.ToLower(filepath.Ext(path))
	if IsBinaryFile(path) && !(i.indexDocuments && IsConvertibleDocument(ext)) {
		return nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read file %s: %w", path, err)
	}

	contentHash := ContentHash(data)
	relPath, err := filepath.Rel(rootDir, path)
	if err != nil {
		return err
	}

	existingHash, exists, err := i.store.GetFileHash(ctx, relPath)
	if err != nil {
		return err
	}
	if exists && existingHash == contentHash {
		return nil
	}

	if exists {
		if err := i.store.DeleteFile(ctx, relPath); err != nil {
			return fmt.Errorf("delete old entries: %w", err)
		}
	}

	switch ext {
	case ".go", ".ts", ".tsx", ".js", ".jsx", ".py", ".rs", ".java", ".c", ".cpp", ".h":
		if err := i.indexCodeSymbols(ctx, relPath, data); err != nil {
			return fmt.Errorf("index code symbols: %w", err)
		}
	default:
		// Try document conversion first; fall back to raw text.
		text, err := ConvertDocument(data, ext)
		switch {
		case err == nil && text != "":
			if err := i.store.InsertDoc(ctx, relPath, text); err != nil {
				return fmt.Errorf("index document: %w", err)
			}
		case IsBinaryFile(path):
			// An office document whose text we could not extract (conversion
			// failed or yielded nothing). Indexing its raw bytes would only add
			// binary noise, so skip it.
			return nil
		default:
			if err := i.indexDocumentText(ctx, relPath, string(data)); err != nil {
				return fmt.Errorf("index document: %w", err)
			}
		}
	}

	return i.store.UpsertFileHash(ctx, relPath, contentHash)
}

// indexCodeSymbols is implemented in symbol_extract.go

// indexDocumentText indexes raw text content.
func (i *Indexer) indexDocumentText(ctx context.Context, relPath, text string) error {
	if text == "" {
		return nil
	}
	return i.store.InsertDoc(ctx, relPath, text)
}
