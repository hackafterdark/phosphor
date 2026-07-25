// Package workspaceindex provides FTS5-based workspace search.
package workspaceindex

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Indexer walks a workspace, extracts symbols and documents, and stores them in FTS5.
type Indexer struct {
	store           *Store
	skipDirs        map[string]bool
	excludePatterns []string
	maxFileSize     int // 0 means unlimited
}

// NewIndexer creates a new indexer with the given store and max file size.
func NewIndexer(store *Store, maxFileSize int) *Indexer {
	return &Indexer{
		store: store,
		skipDirs: map[string]bool{
			".git": true, ".phosphor": true, "node_modules": true,
			"vendor": true, "__pycache__": true, ".DS_Store": true,
		},
		maxFileSize: maxFileSize,
	}
}

// IndexWorkspace walks the workspace directory and indexes all files.
func (i *Indexer) IndexWorkspace(ctx context.Context, rootDir string, excludePatterns []string) error {
	// Load ignore patterns from .gitignore, .phosphorignore, .phosphorindexignore.
	ignorePatterns := loadIgnorePatterns(rootDir)
	// Merge user-provided exclude patterns.
	i.excludePatterns = append(ignorePatterns, excludePatterns...)
	return filepath.WalkDir(rootDir, func(path string, d os.DirEntry, err error) error {
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
		return i.processFile(ctx, rootDir, path)
	})
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

	// Skip binary files.
	if IsBinaryFile(path) {
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

	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".go", ".ts", ".tsx", ".js", ".jsx", ".py", ".rs", ".java", ".c", ".cpp", ".h":
		if err := i.indexCodeSymbols(ctx, relPath, data); err != nil {
			return fmt.Errorf("index code symbols: %w", err)
		}
	default:
		// Try document conversion first; fall back to raw text.
		text, err := ConvertDocument(data, ext)
		if err == nil && text != "" {
			if err := i.store.InsertDoc(ctx, relPath, text); err != nil {
				return fmt.Errorf("index document: %w", err)
			}
		} else {
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
