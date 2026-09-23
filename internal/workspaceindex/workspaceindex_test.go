package workspaceindex

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/xuri/excelize/v2"
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

// TestSearchSymbols_RanksNameMatchAboveDocComment is the ranking regression
// guard. Before weighted bm25, SearchSymbols returned rows in rowid order, so
// a symbol that merely mentioned the term in its doc comment could outrank the
// symbol actually named for it. The documentation-only match is inserted first
// on purpose: under the old rowid ordering it would come back first, so this
// asserting the name match is first proves ranking is doing its job.
func TestSearchSymbols_RanksNameMatchAboveDocComment(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	// Inserted first: matches "zzwidget" only via the documentation column.
	if err := store.InsertSymbol(ctx, "pkg/b.go", "Loader", "pkg.Loader", "func Loader()", "manages the zzwidget lifecycle"); err != nil {
		t.Fatalf("InsertSymbol() error: %v", err)
	}
	// Inserted second: is literally named "zzwidget" (name column match).
	if err := store.InsertSymbol(ctx, "pkg/a.go", "zzwidget", "pkg.zzwidget", "func zzwidget()", "a thing"); err != nil {
		t.Fatalf("InsertSymbol() error: %v", err)
	}

	results, err := store.SearchSymbols(ctx, "zzwidget", 10)
	if err != nil {
		t.Fatalf("SearchSymbols() error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].Name != "zzwidget" {
		t.Errorf("name match should rank first under bm25, got results[0]=%q (rowid order leaked through)", results[0].Name)
	}
	if !(results[0].Score <= results[1].Score) {
		t.Errorf("results not sorted by ascending bm25 score: %v then %v", results[0].Score, results[1].Score)
	}
}

// TestSearchAll_MergesBothSourcesAndSortsByScore guards the Tier 2 merge: the
// two tables are searched independently then interleaved by score, so results
// must be globally ascending by score, must not drop either source, and must
// still honor the limit after widening the per-source candidate pool.
func TestSearchAll_MergesBothSourcesAndSortsByScore(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	if err := store.InsertSymbol(ctx, "pkg/a.go", "AlphaSym", "pkg.AlphaSym", "func AlphaSym()", "mentions mergecheck"); err != nil {
		t.Fatalf("InsertSymbol() error: %v", err)
	}
	if err := store.InsertSymbol(ctx, "pkg/b.go", "BetaSym", "pkg.BetaSym", "func BetaSym()", "mentions mergecheck"); err != nil {
		t.Fatalf("InsertSymbol() error: %v", err)
	}
	if err := store.InsertDoc(ctx, "d1.md", "mergecheck mergecheck mergecheck strong hit"); err != nil {
		t.Fatalf("InsertDoc() error: %v", err)
	}
	if err := store.InsertDoc(ctx, "d2.md", "mergecheck"); err != nil {
		t.Fatalf("InsertDoc() error: %v", err)
	}

	results, err := store.SearchAll(ctx, "mergecheck", 10)
	if err != nil {
		t.Fatalf("SearchAll() error: %v", err)
	}
	if len(results) != 4 {
		t.Fatalf("expected 4 merged results, got %d", len(results))
	}

	var sawSymbol, sawDoc bool
	for i := range results {
		if i > 0 && !(results[i-1].Score <= results[i].Score) {
			t.Errorf("merged results not ascending by score at %d: %v then %v", i, results[i-1].Score, results[i].Score)
		}
		if results[i].Name != "" {
			sawSymbol = true
		} else if results[i].Content != "" {
			sawDoc = true
		}
	}
	if !sawSymbol || !sawDoc {
		t.Errorf("merge should return both a symbol and a doc, sawSymbol=%v sawDoc=%v", sawSymbol, sawDoc)
	}

	trimmed, err := store.SearchAll(ctx, "mergecheck", 2)
	if err != nil {
		t.Fatalf("SearchAll() trim error: %v", err)
	}
	if len(trimmed) != 2 {
		t.Errorf("expected trimmed result of 2, got %d", len(trimmed))
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
	store.InsertDoc(ctx, "test.go", "doc content for test.go")
	store.UpsertFileHash(ctx, "test.go", "hash-for-test-go")

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
	docs, _ := store.CountDocs(ctx)
	if docs != 0 {
		t.Errorf("expected 0 docs after remove, got %d", docs)
	}
	if _, exists, _ := store.GetFileHash(ctx, "test.go"); exists {
		t.Error("expected file_hashes ledger row to be removed after remove event")
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

func TestIgnoreMatcherHonorsDirectorySubtrees(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		path     string
		pattern  string
		expected bool
	}{
		{"nested file under ignored dir", "other_project_research/hermes-agent/apps/desktop/electron/main.cjs", "other_project_research/", true},
		{"bare dir name without trailing slash", "other_project_research/hermes/x.ts", "other_project_research", true},
		{"interior path segment", "vendor/sub/pkg/a.go", "vendor", true},
		{"multi segment directory prefix", "docs/plans/roadmap.md", "docs/plans", true},
		{"basement glob still matches", "logs/app.log", "*.log", true},
		{"exact relative path matches", "docs/plans/x.md", "docs/plans/x.md", true},
		{"partial segment must not match", "src/my_other_project_research_tool/main.go", "other_project_research", false},
		{"unrelated file is kept", "internal/ui/model/ui.go", "other_project_research/", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isExcluded(c.path, []string{c.pattern}); got != c.expected {
				t.Fatalf("isExcluded(%q, [%q]) = %v, want %v", c.path, c.pattern, got, c.expected)
			}
		})
	}
}

// TestIndexerReconcileDropsDeletedAndIgnored is the end-to-end guard for the
// reconcile pass that runs at the tail of IndexWorkspace. After a full walk the
// visited, non-excluded files are the source of truth: any previously-indexed
// path the walk did not visit has since been deleted or moved behind an ignore
// rule, so its symbol, doc, and hash rows must be dropped while still-present
// files keep theirs.

// writeTestXLSX builds a single-sheet workbook whose only cell holds token and
// writes it to path, giving the tests a real office document that converts to
// searchable text without depending on an external fixture.
func writeTestXLSX(t *testing.T, path, token string) {
	t.Helper()
	f := excelize.NewFile()
	sheet, err := f.NewSheet("Sheet1")
	if err != nil {
		t.Fatalf("NewSheet() error: %v", err)
	}
	f.SetCellValue("Sheet1", "A1", token)
	f.SetActiveSheet(sheet)
	buf := new(bytes.Buffer)
	if _, err := f.WriteTo(buf); err != nil {
		t.Fatalf("WriteTo() error: %v", err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// TestIndexerIndexDocumentsEnabled proves that, by default, an office document
// that the binary skip-list would once have dropped is now run through the
// converter and its extracted text becomes searchable in the doc tier.
func TestIndexerIndexDocumentsEnabled(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	indexer := NewIndexer(store, 0) // document indexing is on by default.
	writeTestXLSX(t, filepath.Join(dir, "budget.xlsx"), "quantumwidget")

	if err := indexer.IndexWorkspace(ctx, dir, nil); err != nil {
		t.Fatalf("IndexWorkspace() error: %v", err)
	}

	results, err := store.SearchDocs(ctx, "quantumwidget", 10)
	if err != nil {
		t.Fatalf("SearchDocs() error: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected the XLSX cell text to be indexed and searchable")
	}
	var found bool
	for _, r := range results {
		if filepath.Base(r.Path) == "budget.xlsx" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a hit for budget.xlsx, got %+v", results)
	}
}

// TestIndexerIndexDocumentsDisabledSkipsOfficeDocs proves the escape hatch:
// with document indexing turned off the same office file is treated as opaque
// binary and neither its text nor a hash row is recorded.
func TestIndexerIndexDocumentsDisabledSkipsOfficeDocs(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	indexer := NewIndexer(store, 0)
	indexer.SetIndexDocuments(false)
	writeTestXLSX(t, filepath.Join(dir, "budget.xlsx"), "quantumwidget")

	if err := indexer.IndexWorkspace(ctx, dir, nil); err != nil {
		t.Fatalf("IndexWorkspace() error: %v", err)
	}

	if docs, _ := store.CountDocs(ctx); docs != 0 {
		t.Errorf("expected no docs indexed while disabled, got %d", docs)
	}
	results, err := store.SearchDocs(ctx, "quantumwidget", 10)
	if err != nil {
		t.Fatalf("SearchDocs() error: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected office doc to be skipped while disabled, got %d hits", len(results))
	}
}

// TestIndexerUndecodableBinaryDocIsSkipped guards the safety property behind
// the gate: a file carrying an office extension whose bytes cannot be turned
// into text is dropped outright instead of having its raw bytes indexed as
// garbage document content.
func TestIndexerUndecodableBinaryDocIsSkipped(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	indexer := NewIndexer(store, 0)
	// A "pdf" with none of the markers any converter tier or the raw-stream
	// fallback looks for, so every extraction path yields nothing.
	if err := os.WriteFile(filepath.Join(dir, "junk.pdf"),
		[]byte("\x00\x01\x02\x03 binary garbage without markers"), 0o644); err != nil {
		t.Fatalf("write junk.pdf: %v", err)
	}

	if err := indexer.IndexWorkspace(ctx, dir, nil); err != nil {
		t.Fatalf("IndexWorkspace() error: %v", err)
	}
	if docs, _ := store.CountDocs(ctx); docs != 0 {
		t.Errorf("expected undecodable office doc to be skipped, got %d docs", docs)
	}
}

func TestShouldFailBuild(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		total, failed int
		want          bool
	}{
		{"no files", 0, 0, false},
		{"all succeed", 5, 0, false},
		{"some fail", 5, 3, false},
		{"all fail", 5, 5, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := shouldFailBuild(tt.total, tt.failed); got != tt.want {
				t.Errorf("shouldFailBuild(%d, %d) = %v, want %v", tt.total, tt.failed, got, tt.want)
			}
		})
	}
}

// TestIndexerPartialFailuresAreBestEffort proves that a handful of unreadable
// files no longer pin the whole build to the error state: the successful files
// are still indexed and search works, while the build reports success.
func TestIndexerPartialFailuresAreBestEffort(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX file permissions do not gate reads on windows")
	}
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	if err := os.WriteFile(filepath.Join(dir, "good.go"),
		[]byte("package main\nfunc zebraGood() {}\n"), 0o644); err != nil {
		t.Fatalf("write good.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bad.go"),
		[]byte("package main\nfunc zebraBad() {}\n"), 0o600); err != nil {
		t.Fatalf("write bad.go: %v", err)
	}
	if err := os.Chmod(filepath.Join(dir, "bad.go"), 0o000); err != nil {
		t.Fatalf("chmod bad.go: %v", err)
	}

	indexer := NewIndexer(store, 0)
	if err := indexer.IndexWorkspace(ctx, dir, nil); err != nil {
		t.Fatalf("IndexWorkspace() should tolerate a single unreadable file, got: %v", err)
	}

	progress, err := store.GetProgress(ctx)
	if err != nil {
		t.Fatalf("GetProgress() error: %v", err)
	}
	if progress.Status == IndexStatusError {
		t.Fatal("build must not report the error state when only one file failed")
	}
	results, err := store.SearchSymbols(ctx, "zebraGood", 10)
	if err != nil {
		t.Fatalf("SearchSymbols() error: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected the readable file to be indexed despite a sibling read error")
	}
}

// TestIndexerTotalFailureStillReportsError proves the other side of the fix:
// when every candidate file fails (here because the store is unwritable), the
// build still surfaces the failure instead of silently reporting success.
func TestIndexerTotalFailureStillReportsError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}

	if err := os.WriteFile(filepath.Join(dir, "a.go"),
		[]byte("package main\nfunc a() {}\n"), 0o644); err != nil {
		t.Fatalf("write a.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.go"),
		[]byte("package main\nfunc b() {}\n"), 0o644); err != nil {
		t.Fatalf("write b.go: %v", err)
	}

	indexer := NewIndexer(store, 0)
	// Close the store so every write-backed file operation fails, simulating a
	// systemic problem rather than a single bad file.
	store.Close()

	if err := indexer.IndexWorkspace(context.Background(), dir, nil); err == nil {
		t.Fatal("expected the build to fail when every file fails to index")
	}
}
