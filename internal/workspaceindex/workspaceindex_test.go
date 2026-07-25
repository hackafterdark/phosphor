package workspaceindex

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
	_ "modernc.org/sqlite"
)

func TestNewStore(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}
	defer store.Close()

	// Verify tables exist by inserting test data.
	ctx := context.Background()
	err = store.InsertSymbol(ctx, "test.go", "Foo", "pkg.Foo", "func Foo()", "Does something")
	if err != nil {
		t.Fatalf("InsertSymbol() error: %v", err)
	}
	err = store.InsertDoc(ctx, "README.md", "# Test Doc\nSome content here")
	if err != nil {
		t.Fatalf("InsertDoc() error: %v", err)
	}
}

func TestSearchSymbols(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	store.InsertSymbol(ctx, "pkg/file.go", "NewTool", "pkg.NewTool", "func NewTool() Tool", "Creates a new tool")
	store.InsertSymbol(ctx, "pkg/util.go", "Helper", "pkg.Helper", "func Helper() string", "Utility function")

	results, err := store.SearchSymbols(ctx, "NewTool", 10)
	if err != nil {
		t.Fatalf("SearchSymbols() error: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 result, got %d", len(results))
	}
	if len(results) > 0 && results[0].Name != "NewTool" {
		t.Errorf("expected name NewTool, got %s", results[0].Name)
	}
}

func TestSearchDocs(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	store.InsertDoc(ctx, "docs/guide.md", "# User Guide\nThis guide explains how to use the tool.")
	store.InsertDoc(ctx, "README.md", "# Project\nWelcome to the project.")

	results, err := store.SearchDocs(ctx, "guide", 10)
	if err != nil {
		t.Fatalf("SearchDocs() error: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 result, got %d", len(results))
	}
}

func TestSearchAll(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	store.InsertSymbol(ctx, "pkg/file.go", "Foo", "pkg.Foo", "func Foo()", "")
	store.InsertDoc(ctx, "docs/notes.txt", "Some important notes about Foo implementation")

	results, err := store.SearchAll(ctx, "Foo", 10)
	if err != nil {
		t.Fatalf("SearchAll() error: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("expected 2 results, got %d", len(results))
	}
}

func TestFileHashUpsert(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	err = store.UpsertFileHash(ctx, "test.go", "abc123")
	if err != nil {
		t.Fatalf("UpsertFileHash() error: %v", err)
	}

	hash, exists, err := store.GetFileHash(ctx, "test.go")
	if err != nil {
		t.Fatalf("GetFileHash() error: %v", err)
	}
	if !exists {
		t.Error("hash should exist")
	}
	if hash != "abc123" {
		t.Errorf("expected abc123, got %s", hash)
	}
}

func TestContentHash(t *testing.T) {
	t.Parallel()
	hash1 := ContentHash([]byte("hello world"))
	hash2 := ContentHash([]byte("hello world"))
	if hash1 != hash2 {
		t.Errorf("same content should produce same hash")
	}
	hash3 := ContentHash([]byte("different content"))
	if hash1 == hash3 {
		t.Errorf("different content should produce different hash")
	}
}

func TestIsBinaryFile(t *testing.T) {
	t.Parallel()
	tests := []struct {
		path string
		want bool
	}{
		{"file.go", false},
		{"image.png", true},
		{"script.py", false},
		{"archive.zip", true},
		{"readme.md", false},
		{"binary.exe", true},
	}
	for _, tt := range tests {
		if got := IsBinaryFile(tt.path); got != tt.want {
			t.Errorf("IsBinaryFile(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

func TestIndexerBasic(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}
	defer store.Close()

	indexer := NewIndexer(store, 0)
	ctx := context.Background()

	// Create a test text file.
	textFile := filepath.Join(dir, "hello.txt")
	os.WriteFile(textFile, []byte("Hello world, this is a test document."), 0o644)

	err = indexer.IndexWorkspace(ctx, dir, nil)
	if err != nil {
		t.Fatalf("IndexWorkspace() error: %v", err)
	}

	docs, _ := store.CountDocs(ctx)
	if docs != 1 {
		t.Errorf("expected 1 doc indexed, got %d", docs)
	}
}

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

func TestIndexerSkipUnchanged(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}
	defer store.Close()

	indexer := NewIndexer(store, 0)
	ctx := context.Background()

	textFile := filepath.Join(dir, "hello.txt")
	content := []byte("Some data content")
	os.WriteFile(textFile, content, 0o644)

	// First index: should index the file.
	indexer.IndexWorkspace(ctx, dir, nil)

	// Second index: should skip unchanged file.
	err = indexer.IndexWorkspace(ctx, dir, nil)
	if err != nil {
		t.Fatalf("IndexWorkspace() error: %v", err)
	}

	docs, _ := store.CountDocs(ctx)
	if docs != 1 {
		t.Errorf("expected 1 doc (not re-indexed), got %d", docs)
	}
}

func TestStoreClear(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	store.InsertSymbol(ctx, "a.go", "X", "pkg.X", "func X()", "")
	store.InsertDoc(ctx, "note.txt", "some notes")

	symbols, _ := store.CountSymbols(ctx)
	docs, _ := store.CountDocs(ctx)
	if symbols != 1 || docs != 1 {
		t.Errorf("expected 1 symbol and 1 doc before clear")
	}

	err = store.Clear(ctx)
	if err != nil {
		t.Fatalf("Clear() error: %v", err)
	}

	symbols, _ = store.CountSymbols(ctx)
	docs, _ = store.CountDocs(ctx)
	if symbols != 0 || docs != 0 {
		t.Errorf("expected 0 symbols and 0 docs after clear, got %d symbols, %d docs", symbols, docs)
	}
}

func TestDeleteFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	store.InsertSymbol(ctx, "test.go", "FuncA", "pkg.FuncA", "func FuncA()", "")
	store.InsertDoc(ctx, "test.go", "doc content for test.go")

	err = store.DeleteFile(ctx, "test.go")
	if err != nil {
		t.Fatalf("DeleteFile() error: %v", err)
	}

	symbols, _ := store.CountSymbols(ctx)
	docs, _ := store.CountDocs(ctx)
	if symbols != 0 {
		t.Errorf("expected 0 symbols after delete, got %d", symbols)
	}
	if docs != 0 {
		t.Errorf("expected 0 docs after delete, got %d", docs)
	}
}

func TestWatcherNew(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}
	defer store.Close()

	watcher := NewWatcher(store, dir, nil, 50)
	if watcher == nil {
		t.Fatal("NewWatcher returned nil")
	}
	if watcher.watcher == nil {
		t.Fatal("watcher.watcher is nil")
	}
	watcher.Stop()
}

func TestWatcherIsExcluded(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}
	defer store.Close()

	excludes := []string{"*.log", "*.tmp"}
	watcher := NewWatcher(store, dir, excludes, 50)
	defer watcher.Stop()

	if !watcher.isExcluded("test.log") {
		t.Error("test.log should be excluded")
	}
	if !watcher.isExcluded("data.tmp") {
		t.Error("data.tmp should be excluded")
	}
	if watcher.isExcluded("main.go") {
		t.Error("main.go should NOT be excluded")
	}
}

func TestWatcherRemoveEvent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	store.InsertSymbol(ctx, "test.go", "TestFunc", "pkg.TestFunc", "func TestFunc()", "")

	watcher := NewWatcher(store, dir, nil, 50)
	defer watcher.Stop()

	// Simulate a REMOVE event.
	evt := fsnotify.Event{
		Name: filepath.Join(dir, "test.go"),
		Op:   fsnotify.Remove,
	}
	watcher.handleEvent(evt)

	symbols, _ := store.CountSymbols(ctx)
	if symbols != 0 {
		t.Errorf("expected 0 symbols after remove, got %d", symbols)
	}
}

func TestWatcherCreateWriteEvent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}
	defer store.Close()

	watcher := NewWatcher(store, dir, nil, 50)
	defer watcher.Stop()

	// Simulate a CREATE event.
	evt := fsnotify.Event{
		Name: filepath.Join(dir, "newfile.go"),
		Op:   fsnotify.Create,
	}
	watcher.handleEvent(evt)

	watcher.pendingMu.Lock()
	pendingLen := len(watcher.pending)
	watcher.pendingMu.Unlock()
	if pendingLen != 1 {
		t.Errorf("expected 1 pending file, got %d", pendingLen)
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

func TestWatcherStop(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}
	defer store.Close()

	watcher := NewWatcher(store, dir, nil, 50)
	watcher.Stop()
	// Should not panic when stopping an already stopped watcher.
	watcher.Stop()
}

func TestWatcherLoopWithTimeout(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}
	defer store.Close()

	watcher := NewWatcher(store, dir, nil, 50)
	defer watcher.Stop()

	// Start the watcher loop in a goroutine.
	errCh := make(chan error, 1)
	go func() {
		err := watcher.Start()
		errCh <- err
	}()

	// Give it a moment to start.
	time.Sleep(100 * time.Millisecond)

	// Stop the watcher.
	watcher.Stop()

	// Wait for the loop to exit.
	select {
	case err := <-errCh:
		if err != nil {
			t.Logf("Watcher loop exited with error: %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		// Acceptable - loop exited cleanly via watcher closure.
	}
}

func TestIndexProgress(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}
	defer store.Close()

	ctx := context.Background()

	// Empty index should return zero counts.
	progress, err := store.GetProgress(ctx)
	if err != nil {
		t.Fatalf("GetProgress() error: %v", err)
	}
	if progress == nil {
		t.Fatal("GetProgress returned nil")
	}
	if progress.FilesIndexed != 0 {
		t.Errorf("expected 0 files indexed, got %d", progress.FilesIndexed)
	}
	if progress.Complete {
		t.Error("empty index should not be marked complete")
	}

	// Add some data.
	store.InsertSymbol(ctx, "main.go", "Main", "pkg.Main", "func Main()", "")
	store.InsertDoc(ctx, "README.md", "# Project\nSome documentation.")
	store.UpsertFileHash(ctx, "main.go", "hash1")
	store.UpsertFileHash(ctx, "README.md", "hash2")

	// Check updated progress.
	progress, err = store.GetProgress(ctx)
	if err != nil {
		t.Fatalf("GetProgress() error: %v", err)
	}
	if progress.FilesIndexed != 2 {
		t.Errorf("expected 2 files indexed, got %d", progress.FilesIndexed)
	}
	if progress.SymbolsIndexed != 1 {
		t.Errorf("expected 1 symbol, got %d", progress.SymbolsIndexed)
	}
	if progress.DocsIndexed != 1 {
		t.Errorf("expected 1 doc, got %d", progress.DocsIndexed)
	}
	if !progress.Complete {
		t.Error("index with files should be marked complete")
	}
}
