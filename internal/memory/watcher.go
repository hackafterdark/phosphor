package memory

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/hackafterdark/phosphor/pkg/otel"
	"go.opentelemetry.io/otel/attribute"
)

// Watcher keeps the derived index honest while a human edits the vault by hand, which
// is the normal condition of a markdown store that doubles as an Obsidian vault. It is
// debounced and content-hash-gated: the event stream only decides when to look, and
// the hash decides whether looking is worth anything.
//
// The watcher is an accelerator, not a correctness dependency. Every read path calls
// SyncIfStale, so a session stays consistent even with the watcher switched off.
type Watcher struct {
	store   *Store
	watcher *fsnotify.Watcher
	dirs    []string

	pendingMu sync.Mutex
	pending   map[string]time.Time
	debounce  time.Duration

	notifyMu sync.Mutex
	notify   []func(changed []string)

	cancel context.CancelFunc
	done   chan struct{}
}

// NewWatcher watches the vaults a store covers.
func NewWatcher(store *Store) (*Watcher, error) {
	if store == nil {
		return nil, fmt.Errorf("cannot watch a nil store")
	}
	impl, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("create vault watcher: %w", err)
	}
	w := &Watcher{
		store:    store,
		watcher:  impl,
		pending:  map[string]time.Time{},
		debounce: 400 * time.Millisecond,
		done:     make(chan struct{}, 1),
	}
	for _, scope := range []Scope{ScopeProject, ScopeGlobal} {
		dir := store.VaultDir(scope)
		if dir == "" || !dirExists(dir) {
			continue
		}
		// Obsidian may nest notes in subfolders, so every directory under the vault is
		// registered; fsnotify reports per-directory, not recursively.
		err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if !d.IsDir() {
				return nil
			}
			if base := filepath.Base(p); strings.HasPrefix(base, ".") {
				return filepath.SkipDir
			}
			if werr := impl.Add(p); werr != nil {
				slog.Warn("Failed to watch memory directory", "path", filepath.ToSlash(p), "error", werr)
			}
			return nil
		})
		if err != nil {
			impl.Close()
			return nil, fmt.Errorf("watch %s: %w", filepath.ToSlash(dir), err)
		}
		w.dirs = append(w.dirs, dir)
	}
	if len(w.dirs) == 0 {
		impl.Close()
		return nil, fmt.Errorf("no vault directories to watch")
	}
	return w, nil
}

// OnChange registers a callback fired after each debounced rescan with the paths that
// changed. The session-start surfacing of "the user edited memories" hangs off this.
func (w *Watcher) OnChange(fn func(changed []string)) {
	w.notifyMu.Lock()
	defer w.notifyMu.Unlock()
	w.notify = append(w.notify, fn)
}

// Start begins watching. It returns once the initial sync has run so a caller can rely
// on the index being current when the first prompt is built.
func (w *Watcher) Start(ctx context.Context) error {
	if err := w.store.Sync(ctx); err != nil {
		// A failed first sync is not fatal: reads still re-check lazily.
		slog.Warn("Initial memory sync failed; reads will retry lazily", "error", err)
	}
	events, errs := w.watcher.Events, w.watcher.Errors
	cancelCtx, cancel := context.WithCancel(ctx)
	w.cancel = cancel
	go func() {
		defer close(w.done)
		for {
			select {
			case <-cancelCtx.Done():
				return
			case event, ok := <-events:
				if !ok {
					return
				}
				w.mark(event.Name)
				// A directory Obsidian created under the vault has to be registered
				// explicitly or notes inside it would never be seen.
				if event.Op.Has(fsnotify.Create) {
					if info, err := os.Stat(event.Name); err == nil && info.IsDir() {
						_ = w.watcher.Add(event.Name)
					}
				}
			case err, ok := <-errs:
				if !ok {
					return
				}
				if err != nil {
					slog.Warn("Vault watcher reported an error", "error", err)
				}
				return
			}
		}
	}()
	go w.flushLoop(cancelCtx)
	return nil
}

func (w *Watcher) mark(path string) {
	if path == "" || !strings.HasSuffix(strings.ToLower(path), ".md") {
		return
	}
	w.pendingMu.Lock()
	defer w.pendingMu.Unlock()
	w.pending[filepath.ToSlash(path)] = time.Now()
}

// flushLoop drains debounced changes into one rescan per quiet period, so a burst of
// writes from a sync plugin or an Obsidian save costs one pass instead of one per file.
func (w *Watcher) flushLoop(ctx context.Context) {
	ticker := time.NewTicker(w.debounce)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.pendingMu.Lock()
			var ready []string
			for path, seen := range w.pending {
				if time.Since(seen) >= w.debounce {
					ready = append(ready, path)
					delete(w.pending, path)
				}
			}
			w.pendingMu.Unlock()
			if len(ready) == 0 {
				continue
			}
			syncCtx, span := otel.StartSpan(ctx, "memory.watcher.rescan")
			if err := w.store.Sync(syncCtx); err != nil {
				otel.RecordError(span, err)
				span.End()
				slog.Warn("Memory rescan failed after a vault change", "error", err, "files", len(ready))
				continue
			}
			span.SetAttributes(attribute.Int("phosphor.memory.files", len(ready)))
			span.End()
			slog.Debug("Memory index refreshed after vault edits", "files", len(ready))
			w.notifyMu.Lock()
			handlers := append([]func([]string){}, w.notify...)
			w.notifyMu.Unlock()
			for _, fn := range handlers {
				fn(ready)
			}
		}
	}
}

// Stop ends watching and releases the OS handles.
func (w *Watcher) Stop() error {
	if w.cancel != nil {
		w.cancel()
	}
	var err error
	if w.watcher != nil {
		err = w.watcher.Close()
	}
	return err
}

// ChangedSince reports the vault files that were edited after a moment, which is what
// lets a session surface "the user changed memories X, Y since the session started"
// instead of silently adopting them mid-conversation.
func ChangedSince(dir string, since time.Time) []string {
	var out []string
	if dir == "" {
		return nil
	}
	filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(p, ".md") {
			return nil
		}
		if info, err := os.Stat(p); err == nil && info.ModTime().After(since) {
			out = append(out, filepath.ToSlash(p))
		}
		return nil
	})
	return out
}
