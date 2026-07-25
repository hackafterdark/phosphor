// Package workspaceindex provides file watching for auto-updating the symbol index.
package workspaceindex

import (
	"context"
	"fmt"
	"log/slog"
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
	ignorePatterns []string // cached, loaded once at Start
}

// NewWatcher creates a new file watcher for the workspace directory.
func NewWatcher(store *Store, workspaceDir string, excludes []string, debounceMs int) *Watcher {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil
	}
	return &Watcher{
		store:        store,
		watcher:      watcher,
		workspaceDir: workspaceDir,
		excludes:     excludes,
		pending:      make(map[string]time.Time),
		debounceMs:   debounceMs,
		debounceCh:   make(chan time.Time, 1),
	}
}

// Start begins watching the workspace directory for file changes.
func (w *Watcher) Start() error {
	// Pre-load ignore patterns once to avoid per-event file I/O.
	w.ignorePatterns = loadIgnorePatterns(w.workspaceDir)

	if err := w.watcher.Add(w.workspaceDir); err != nil {
		return fmt.Errorf("add workspace dir to watcher: %w", err)
	}

	go w.loop()
	return nil
}

// Stop closes the file watcher.
func (w *Watcher) Stop() {
	if w.watcher != nil {
		w.watcher.Close()
	}
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
		hasPending := len(w.pending) > 0
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

	w.pendingMu.Lock()
	defer w.pendingMu.Unlock()

	if evt.Op.Has(fsnotify.Create) || evt.Op.Has(fsnotify.Write) {
		w.pending[evt.Name] = time.Now()
	} else if evt.Op.Has(fsnotify.Remove) {
		relPath, err := filepath.Rel(w.workspaceDir, evt.Name)
		if err != nil {
			return
		}
		w.store.DeleteFile(context.Background(), relPath)
		w.store.UpsertFileHash(context.Background(), relPath, "")
		delete(w.pending, evt.Name)
	}
}

func (w *Watcher) processPending() {
	w.pendingMu.Lock()
	pending := make(map[string]time.Time, len(w.pending))
	for k, v := range w.pending {
		pending[k] = v
	}
	w.pending = make(map[string]time.Time)
	w.pendingMu.Unlock()

	if len(pending) == 0 {
		return
	}

	ctx := context.Background()
	// Create one indexer and reuse it for all pending files.
	indexer := NewIndexer(w.store, 0)
	for path := range pending {
		indexer.processFile(ctx, w.workspaceDir, path)
	}
}

func (w *Watcher) isExcluded(path string) bool {
	for _, pattern := range w.ignorePatterns {
		if matched, _ := filepath.Match(pattern, filepath.Base(path)); matched {
			return true
		}
	}
	for _, pattern := range w.excludes {
		if matched, _ := filepath.Match(pattern, filepath.Base(path)); matched {
			return true
		}
	}
	return false
}
