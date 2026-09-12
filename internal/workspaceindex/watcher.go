// Package workspaceindex provides file watching for auto-updating the symbol index.
package workspaceindex

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// Watcher monitors a workspace directory for file changes and
// keeps the FTS5 symbol index up-to-date.
type Watcher struct {
	store          *Store
	watcher        *fsnotify.Watcher
	workspaceDir   string
	excludes       []string
	pending        map[string]time.Time
	pendingMu      sync.Mutex
	debounceCh     chan time.Time
	debounceMs     int
	rescan         bool
	indexDocuments bool     // mirrors the indexer's document-extraction setting
	ignorePatterns []string // cached, loaded once at Start
}

// NewWatcher creates a new file watcher for the workspace directory.
func NewWatcher(store *Store, workspaceDir string, excludes []string, debounceMs int) *Watcher {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil
	}
	return &Watcher{
		store:          store,
		watcher:        watcher,
		workspaceDir:   workspaceDir,
		excludes:       excludes,
		pending:        make(map[string]time.Time),
		debounceMs:     debounceMs,
		debounceCh:     make(chan time.Time, 1),
		indexDocuments: true,
	}
}

// Start begins watching the workspace directory for file changes.
func (w *Watcher) Start() error {
	// Pre-load ignore patterns once to avoid per-event file I/O.
	w.ignorePatterns = loadIgnorePatterns(w.workspaceDir)

	if err := w.watcher.Add(w.workspaceDir); err != nil {
		return fmt.Errorf("add workspace dir to watcher: %w", err)
	}
	// fsnotify is not recursive on its own; register every subdirectory
	// so changes deep in the tree (where nearly all files live) are seen.
	w.addTree(w.workspaceDir)

	go w.loop()
	return nil
}

// Stop closes the file watcher.
func (w *Watcher) Stop() {
	if w.watcher != nil {
		w.watcher.Close()
	}
}

// SetIndexDocuments controls whether binary office documents (PDF, DOCX,
// XLSX, PPTX) are extracted into docs_fts when the watcher re-indexes a
// changed file. It defaults to true and mirrors Indexer.SetIndexDocuments so
// incremental updates stay consistent with full builds. Call before Start.
func (w *Watcher) SetIndexDocuments(enabled bool) {
	w.indexDocuments = enabled
}

func (w *Watcher) loop() {
	// Arm the debounce timer. Only fires when there are pending files.
	var debounce *time.Timer
	var debounceCh <-chan time.Time

	for {
		select {
		case evt, ok := <-w.watcher.Events:
			if !ok {
				return
			}
			w.handleEvent(evt)
		case err, ok := <-w.watcher.Errors:
			if !ok {
				return
			}
			slog.Warn("File watcher error", "error", err)
		case <-debounceCh:
			w.processPending()
		}

		// Re-arm the debounce timer if there are pending files.
		// Only start a new timer if one doesn't exist, or restart an existing one.
		w.pendingMu.Lock()
		hasPending := len(w.pending) > 0 || w.rescan
		w.pendingMu.Unlock()

		if hasPending {
			if debounce == nil {
				debounce = time.NewTimer(time.Duration(w.debounceMs) * time.Millisecond)
				debounceCh = debounce.C
			} else {
				debounce.Reset(time.Duration(w.debounceMs) * time.Millisecond)
				debounceCh = debounce.C
			}
		} else {
			if debounce != nil {
				debounce.Stop()
				debounceCh = nil
			}
		}
	}
}

func (w *Watcher) handleEvent(evt fsnotify.Event) {
	if w.isExcluded(evt.Name) {
		return
	}

	switch {
	case evt.Op.Has(fsnotify.Create):
		// A new directory has to be registered with the watcher so its
		// future changes are seen; then ask for a rescan to pick up the
		// files that already landed inside it.
		if info, err := os.Stat(evt.Name); err == nil && info.IsDir() {
			w.addTree(evt.Name)
			w.requestRescan()
			return
		}
		w.requestIndex(evt.Name)
	case evt.Op.Has(fsnotify.Write):
		if info, err := os.Stat(evt.Name); err == nil && info.IsDir() {
			return
		}
		w.requestIndex(evt.Name)
	case evt.Op.Has(fsnotify.Remove):
		relPath, err := filepath.Rel(w.workspaceDir, evt.Name)
		if err != nil {
			return
		}
		// DeleteFile removes the symbol/doc rows and the file_hashes ledger
		// row together, so a deleted file leaves no trace. (A deleted file
		// that later reappears is re-indexed from scratch on its next
		// create/write because its ledger row is gone.)
		if err := w.store.DeleteFile(context.Background(), relPath); err != nil {
			slog.Warn("Failed to remove deleted file from workspace index", "path", relPath, "error", err)
		}
		_ = w.watcher.Remove(evt.Name) // no-op if it was never watched
		w.dropPending(evt.Name)
	}
}

const maxPendingFiles = 2048

func (w *Watcher) requestIndex(path string) {
	w.pendingMu.Lock()
	defer w.pendingMu.Unlock()
	if w.rescan {
		return
	}
	if len(w.pending) >= maxPendingFiles {
		w.rescan = true
		w.pending = make(map[string]time.Time)
		return
	}
	w.pending[path] = time.Now()
}

func (w *Watcher) requestRescan() {
	w.pendingMu.Lock()
	defer w.pendingMu.Unlock()
	w.rescan = true
	w.pending = make(map[string]time.Time)
}

func (w *Watcher) dropPending(path string) {
	w.pendingMu.Lock()
	defer w.pendingMu.Unlock()
	delete(w.pending, path)
}

// addTree registers root and every directory beneath it with the
// underlying watcher, skipping well-known build and dependency folders.
func (w *Watcher) addTree(root string) {
	filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if watcherSkipDirs[d.Name()] {
				return filepath.SkipDir
			}
			_ = w.watcher.Add(path)
		}
		return nil
	})
}

func (w *Watcher) processPending() {
	w.pendingMu.Lock()
	rescan := w.rescan
	pending := w.pending
	w.pending = make(map[string]time.Time)
	w.rescan = false
	w.pendingMu.Unlock()

	if !rescan && len(pending) == 0 {
		return
	}

	ctx := context.Background()
	indexer := NewIndexer(w.store, 0)
	indexer.SetLimits(2, 64)
	indexer.SetIndexDocuments(w.indexDocuments)

	if rescan {
		// A large burst (checkout, bulk save, codegen) is cheaper and
		// safer as one bounded, yielding incremental walk than as thousands
		// of individual replays; content hashing makes already-indexed
		// files near-free.
		if err := indexer.IndexWorkspace(ctx, w.workspaceDir, w.excludes); err != nil {
			slog.Warn("Workspace index rescan failed", "error", err)
			return
		}
		w.store.MarkIncrementalUpdate()
		return
	}

	for path := range pending {
		indexer.processFile(ctx, w.workspaceDir, path)
	}
	w.store.MarkIncrementalUpdate()
}

// watcherSkipDirs are never registered with the file system watcher.
var watcherSkipDirs = map[string]bool{
	".git": true, ".phosphor": true, "node_modules": true, "vendor": true,
	"__pycache__": true, "dist": true, "build": true, "out": true, "bin": true,
	"target": true, ".venv": true, "venv": true, "env": true, "coverage": true,
	".mypy_cache": true, ".pytest_cache": true, ".tox": true, ".next": true,
	".turbo": true, ".cache": true, "tmp": true, "temp": true, ".terragrunt": true,
}

func (w *Watcher) isExcluded(path string) bool {
	for _, pattern := range w.ignorePatterns {
		if match(path, pattern) {
			return true
		}
	}
	for _, pattern := range w.excludes {
		if match(path, pattern) {
			return true
		}
	}
	return false
}
