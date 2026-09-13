//go:build cgo

package workspaceindex

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/fsnotify/fsnotify"
)

// The tests in this file exercise tree-sitter-backed code-symbol extraction,
// which is only available when the binary is built with CGO enabled. They live
// here (rather than in workspaceindex_test.go) so the no-CGO build can compile
// and run the rest of the suite, where indexCodeSymbols degrades to a no-op.

func TestIndexerCodeSymbols(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}
	defer store.Close()

	indexer := NewIndexer(store, 0)
	ctx := context.Background()

	// Create a Go file with a function.
	goFile := filepath.Join(dir, "main.go")
	goContent := `package main

func Hello() string {
	return "world"
}
`
	os.WriteFile(goFile, []byte(goContent), 0o644)

	err = indexer.IndexWorkspace(ctx, dir, nil)
	if err != nil {
		t.Fatalf("IndexWorkspace() error: %v", err)
	}

	symbols, _ := store.CountSymbols(ctx)
	if symbols != 1 {
		t.Errorf("expected 1 symbol indexed, got %d", symbols)
	}

	results, err := store.SearchSymbols(ctx, "Hello", 10)
	if err != nil {
		t.Fatalf("SearchSymbols() error: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 search result, got %d", len(results))
	} else if results[0].Name != "Hello" {
		t.Errorf("expected name 'Hello', got %s", results[0].Name)
	}
}

func TestWatcherProcessPending(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}
	defer store.Close()

	ctx := context.Background()

	// Create a Go file to be indexed.
	goFile := filepath.Join(dir, "pending.go")
	goContent := `package main

func PendingFunc() {}
`
	os.WriteFile(goFile, []byte(goContent), 0o644)

	watcher := NewWatcher(store, dir, nil, 50)
	defer watcher.Stop()

	// Simulate a WRITE event.
	evt := fsnotify.Event{
		Name: goFile,
		Op:   fsnotify.Write,
	}
	watcher.handleEvent(evt)

	// Process pending files.
	watcher.processPending()

	symbols, _ := store.CountSymbols(ctx)
	if symbols != 1 {
		t.Errorf("expected 1 symbol after processing pending, got %d", symbols)
	}
}

func TestIndexerReconcileDropsDeletedAndIgnored(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	indexer := NewIndexer(store, 0)

	// keep.go stays present and indexable for the whole test.
	if err := os.WriteFile(filepath.Join(dir, "keep.go"),
		[]byte("package keep\n\nfunc KeepFunc() {}\n"), 0o644); err != nil {
		t.Fatalf("write keep.go: %v", err)
	}
	// gone.go is indexed on the first pass, then deleted before the second.
	if err := os.WriteFile(filepath.Join(dir, "gone.go"),
		[]byte("package gone\n\nfunc GoneFunc() {}\n"), 0o644); err != nil {
		t.Fatalf("write gone.go: %v", err)
	}
	// secret.go stays on disk the whole time but becomes ignored on pass two.
	secretDir := filepath.Join(dir, "ignored")
	if err := os.MkdirAll(secretDir, 0o755); err != nil {
		t.Fatalf("mkdir ignored: %v", err)
	}
	if err := os.WriteFile(filepath.Join(secretDir, "secret.go"),
		[]byte("package secret\n\nfunc SecretFunc() {}\n"), 0o644); err != nil {
		t.Fatalf("write secret.go: %v", err)
	}

	// First pass: nothing ignored, so all three symbols are indexed.
	if err := indexer.IndexWorkspace(ctx, dir, nil); err != nil {
		t.Fatalf("first IndexWorkspace() error: %v", err)
	}
	for _, name := range []string{"KeepFunc", "GoneFunc", "SecretFunc"} {
		results, err := store.SearchSymbols(ctx, name, 10)
		if err != nil {
			t.Fatalf("search %s: %v", name, err)
		}
		if len(results) != 1 {
			t.Fatalf("before changes: expected %s indexed once, got %d", name, len(results))
		}
	}

	// Delete gone.go from disk so the second walk no longer visits it.
	if err := os.Remove(filepath.Join(dir, "gone.go")); err != nil {
		t.Fatalf("remove gone.go: %v", err)
	}

	// Second pass: ignored/ is now excluded. keep.go must survive; gone.go
	// (deleted) and ignored/secret.go (newly hidden) must both be reconciled.
	if err := indexer.IndexWorkspace(ctx, dir, []string{"ignored/"}); err != nil {
		t.Fatalf("second IndexWorkspace() error: %v", err)
	}

	if results, err := store.SearchSymbols(ctx, "KeepFunc", 10); err != nil || len(results) != 1 {
		t.Errorf("keep.go symbol should survive reconcile, got %d results (err=%v)", len(results), err)
	}
	for _, name := range []string{"GoneFunc", "SecretFunc"} {
		results, err := store.SearchSymbols(ctx, name, 10)
		if err != nil {
			t.Fatalf("search %s: %v", name, err)
		}
		if len(results) != 0 {
			t.Errorf("%s rows should be pruned after reconcile, got %d", name, len(results))
		}
	}

	// The hash ledger must no longer list the two removed files, while the
	// surviving file's ledger row stays intact.
	goneRel, _ := filepath.Rel(dir, filepath.Join(dir, "gone.go"))
	if _, exists, _ := store.GetFileHash(ctx, goneRel); exists {
		t.Errorf("gone.go hash should be pruned, still present at %q", goneRel)
	}
	secretRel, _ := filepath.Rel(dir, filepath.Join(secretDir, "secret.go"))
	if _, exists, _ := store.GetFileHash(ctx, secretRel); exists {
		t.Errorf("ignored/secret.go hash should be pruned, still present at %q", secretRel)
	}
	keepRel, _ := filepath.Rel(dir, filepath.Join(dir, "keep.go"))
	if _, exists, _ := store.GetFileHash(ctx, keepRel); !exists {
		t.Errorf("keep.go hash should survive reconcile, missing at %q", keepRel)
	}

	if files, _ := store.CountFiles(ctx); files != 1 {
		t.Errorf("expected 1 file in ledger after reconcile, got %d", files)
	}
}
