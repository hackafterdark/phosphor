// Package workspaceindex provides FTS5-based workspace search.
package workspaceindex

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// indexPragmas are applied to every connection via the DSN _pragma query
// parameters, mirroring the main database's pragmas. busy_timeout and
// synchronous are what make the always-on watcher and the background build
// tolerate concurrent writers instead of failing on SQLITE_BUSY; the
// single-writer connection (see NewStore) does the rest.
var indexPragmas = map[string]string{
	"journal_mode": "WAL",
	"synchronous":  "NORMAL",
	"busy_timeout": "30000",
	"page_size":    "4096",
	"temp_store":   "MEMORY",
	"cache_size":   "-8000",
	"mmap_size":    "134217728",
}

// indexDSN builds a modernc/sqlite DSN that applies indexPragmas on every
// pooled connection and acquires write locks up front (BEGIN IMMEDIATE).
func indexDSN(dbPath string) string {
	params := url.Values{}
	for name, value := range indexPragmas {
		params.Add("_pragma", fmt.Sprintf("%s(%s)", name, value))
	}
	params.Set("_txlock", "immediate")
	return fmt.Sprintf("file:%s?%s", dbPath, params.Encode())
}

// IndexStatus is the coarse lifecycle state of a build run.
type IndexStatus string

const (
	IndexStatusIdle     IndexStatus = "idle"
	IndexStatusIndexing IndexStatus = "indexing"
	IndexStatusComplete IndexStatus = "complete"
	IndexStatusError    IndexStatus = "error"
)

// Store manages the SQLite database with FTS5 tables.
type Store struct {
	db *sql.DB

	// wmu serializes write operations so the file watcher, the
	// background build, and tool-triggered writes never interleave
	// their SQLite transactions. Complements SetMaxOpenConns(1).
	wmu sync.Mutex

	// bmu guards the in-memory build state used for live progress.
	// The durable parts (last build time, total file count, completed
	// flag) are mirrored to the meta table so they survive restarts.
	bmu               sync.RWMutex
	buildStatus       IndexStatus
	currentFile       string
	filesIndexed      int
	totalFiles        int
	lastBuilt         time.Time
	buildCompleted    bool
	updatedSinceBuild int
}

// NewStore opens or creates the workspace index database at
// .phosphor/workspace_index.db inside the workspace directory.
func NewStore(workspaceDir string) (*Store, error) {
	dbPath := filepath.Join(workspaceDir, ".phosphor", "workspace_index.db")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return nil, fmt.Errorf("create data directory: %w", err)
	}

	db, err := sql.Open("sqlite", indexDSN(dbPath))
	if err != nil {
		return nil, fmt.Errorf("open store db: %w", err)
	}

	if err := db.PingContext(context.Background()); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping db: %w", err)
	}

	// Serialize writes onto a single connection, mirroring the main
	// database: SQLite serializes writes at the file level anyway and
	// letting pool connections interleave under a busy watcher has
	// previously caused WAL desync in this project.
	db.SetMaxOpenConns(1)

	store := &Store{db: db, buildStatus: IndexStatusIdle}
	if err := store.init(); err != nil {
		db.Close()
		return nil, fmt.Errorf("init schema: %w", err)
	}
	store.loadMeta(context.Background())

	return store, nil
}

func (s *Store) init() error {
	return s.createSchema(context.Background())
}

// schemaStmts returns the canonical CREATE statements for the index tables.
// They are shared by [Store.init] and [Store.Clear] so a cleared store is left
// in exactly the same schema state as a freshly opened one.
func (s *Store) schemaStmts() []string {
	return []string{
		`CREATE VIRTUAL TABLE IF NOT EXISTS symbols_fts USING fts5(
			path, name, qualified_name, signature, documentation
		)`,
		`CREATE VIRTUAL TABLE IF NOT EXISTS docs_fts USING fts5(
			path, content
		)`,
		`CREATE TABLE IF NOT EXISTS file_hashes (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			path TEXT NOT NULL UNIQUE,
			content_hash TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS meta (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		)`,
	}
}

func (s *Store) createSchema(ctx context.Context) error {
	for _, stmt := range s.schemaStmts() {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("exec schema: %w", err)
		}
	}
	return nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) UpsertFileHash(ctx context.Context, path, hash string) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	_, err := s.db.ExecContext(ctx,
		"INSERT INTO file_hashes(path, content_hash) VALUES (?, ?) ON CONFLICT(path) DO UPDATE SET content_hash = excluded.content_hash",
		path, hash,
	)
	if err != nil {
		return fmt.Errorf("upsert file hash: %w", err)
	}
	return nil
}

func (s *Store) GetFileHash(ctx context.Context, path string) (string, bool, error) {
	var hash string
	err := s.db.QueryRowContext(ctx, "SELECT content_hash FROM file_hashes WHERE path = ?", path).Scan(&hash)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("get file hash: %w", err)
	}
	return hash, true, nil
}

func (s *Store) DeleteFile(ctx context.Context, path string) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	if _, err := s.db.ExecContext(ctx, "DELETE FROM symbols_fts WHERE path = ?", path); err != nil {
		return fmt.Errorf("delete symbols: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, "DELETE FROM docs_fts WHERE path = ?", path); err != nil {
		return fmt.Errorf("delete docs: %w", err)
	}
	// Drop the ledger row too, so a deleted file stops counting toward the
	// indexed-file total and can be re-indexed cleanly if it reappears.
	if _, err := s.db.ExecContext(ctx, "DELETE FROM file_hashes WHERE path = ?", path); err != nil {
		return fmt.Errorf("delete file hash: %w", err)
	}
	return nil
}

// PruneNotIndexed deletes every indexed file that is not present in keep and
// returns how many were removed. The caller passes the set of relative paths
// its walk considered indexable; anything in the store but not in keep has
// since been deleted or moved behind an ignore rule, so its rows are stale.
func (s *Store) PruneNotIndexed(ctx context.Context, keep map[string]bool) (int, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT path FROM file_hashes")
	if err != nil {
		return 0, fmt.Errorf("list indexed paths: %w", err)
	}
	var stale []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			rows.Close()
			return 0, fmt.Errorf("scan indexed path: %w", err)
		}
		if !keep[p] {
			stale = append(stale, p)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, fmt.Errorf("iterate indexed paths: %w", err)
	}
	rows.Close()

	for _, p := range stale {
		if err := s.DeleteFile(ctx, p); err != nil {
			return 0, err
		}
	}
	return len(stale), nil
}

func (s *Store) InsertSymbol(ctx context.Context, path, name, qualifiedName, signature, documentation string) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	_, err := s.db.ExecContext(ctx,
		"INSERT INTO symbols_fts(path, name, qualified_name, signature, documentation) VALUES (?, ?, ?, ?, ?)",
		path, name, qualifiedName, signature, documentation,
	)
	if err != nil {
		return fmt.Errorf("insert symbol: %w", err)
	}
	return nil
}

func (s *Store) InsertDoc(ctx context.Context, path, content string) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	_, err := s.db.ExecContext(ctx,
		"INSERT INTO docs_fts(path, content) VALUES (?, ?)",
		path, content,
	)
	if err != nil {
		return fmt.Errorf("insert doc: %w", err)
	}
	return nil
}

type SearchResult struct {
	Path          string
	Name          string
	QualifiedName string
	Score         float64
	Signature     string
	Documentation string
	Content       string
}

// bm25 column weights drive FTS5 relevance. bm25 scores more-relevant rows
// lower, so queries ORDER BY the weighted bm25() ascending. A higher weight
// means a match in that column counts for more. Weights are positional to
// the fts5 table columns (symbols: path,name,qualified_name,signature,
// documentation; docs: path,content). Ranking is applied in the query rather
// than via the rank= table option so the weights live here and also apply to
// databases created before this change.
const (
	symbolBM25 = "bm25(symbols_fts, 1.0, 10.0, 6.0, 2.0, 3.0)"
	docBM25    = "bm25(docs_fts, 1.0, 10.0)"
)

var (
	searchSymbolsQuery = fmt.Sprintf(
		"SELECT path, name, qualified_name, signature, documentation, %s AS score "+
			"FROM symbols_fts WHERE symbols_fts MATCH ? ORDER BY %s ASC LIMIT ?",
		symbolBM25, symbolBM25,
	)
	searchDocsQuery = fmt.Sprintf(
		"SELECT path, content, %s AS score "+
			"FROM docs_fts WHERE docs_fts MATCH ? ORDER BY %s ASC LIMIT ?",
		docBM25, docBM25,
	)
)

func (s *Store) SearchSymbols(ctx context.Context, query string, limit int) ([]SearchResult, error) {
	rows, err := s.db.QueryContext(ctx,
		searchSymbolsQuery,
		query, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("search symbols: %w", err)
	}
	defer rows.Close()

	var results []SearchResult
	for rows.Next() {
		var r SearchResult
		if err := rows.Scan(&r.Path, &r.Name, &r.QualifiedName, &r.Signature, &r.Documentation, &r.Score); err != nil {
			return nil, fmt.Errorf("scan row: %w", err)
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

func (s *Store) SearchDocs(ctx context.Context, query string, limit int) ([]SearchResult, error) {
	rows, err := s.db.QueryContext(ctx,
		searchDocsQuery,
		query, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("search docs: %w", err)
	}
	defer rows.Close()

	var results []SearchResult
	for rows.Next() {
		var r SearchResult
		if err := rows.Scan(&r.Path, &r.Content, &r.Score); err != nil {
			return nil, fmt.Errorf("scan row: %w", err)
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

func (s *Store) SearchAll(ctx context.Context, query string, limit int) ([]SearchResult, error) {
	// Widen each source's candidate pool before merging so the interleave is
	// chosen from a broad enough sample of both tables, not just the first
	// 'limit' rows of whichever table the file walk happened to reach first.
	candidates := limit * 3
	symbols, err := s.SearchSymbols(ctx, query, candidates)
	if err != nil {
		return nil, err
	}
	docs, err := s.SearchDocs(ctx, query, candidates)
	if err != nil {
		return nil, err
	}

	// Interleave the two already-ranked sets by bm25 score so a strong
	// document hit is not forced behind a weak symbol hit, then trim.
	merged := append(append([]SearchResult{}, symbols...), docs...)
	sort.SliceStable(merged, func(i, j int) bool { return merged[i].Score < merged[j].Score })
	if len(merged) > limit {
		merged = merged[:limit]
	}
	return merged, nil
}

func (s *Store) Clear(ctx context.Context) error {
	stmts := []string{
		"DROP TABLE IF EXISTS symbols_fts",
		"DROP TABLE IF EXISTS docs_fts",
		"DROP TABLE IF EXISTS file_hashes",
		"DROP TABLE IF EXISTS meta",
	}
	s.wmu.Lock()
	defer s.wmu.Unlock()
	for _, stmt := range stmts {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("clear table: %w", err)
		}
	}
	// Recreate the full schema so the store is immediately usable again. A
	// cleared database must look identical to a freshly opened one, or the
	// next Update/Rebuild would fail against the missing FTS5 and hash tables.
	if err := s.createSchema(ctx); err != nil {
		return err
	}

	s.bmu.Lock()
	s.buildStatus = IndexStatusIdle
	s.currentFile = ""
	s.filesIndexed = 0
	s.totalFiles = 0
	s.lastBuilt = time.Time{}
	s.buildCompleted = false
	s.updatedSinceBuild = 0
	s.bmu.Unlock()
	return nil
}

func (s *Store) CountSymbols(ctx context.Context) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM symbols_fts").Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count symbols: %w", err)
	}
	return count, nil
}

func (s *Store) CountDocs(ctx context.Context) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM docs_fts").Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count docs: %w", err)
	}
	return count, nil
}

// CountFiles returns the number of tracked file hashes.
func (s *Store) CountFiles(ctx context.Context) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM file_hashes").Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count files: %w", err)
	}
	return count, nil
}

// ContentHash computes a SHA-256 hash of the given content.
func ContentHash(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

// IsBinaryFile returns true if the file extension is in the binary skip list.
var binaryExtensions = map[string]bool{
	".exe": true, ".dll": true, ".so": true, ".dylib": true,
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true,
	".zip": true, ".tar": true, ".gz": true, ".rar": true,
	".pdf": true, ".doc": true, ".docx": true, ".ppt": true,
	".pptx": true, ".xls": true, ".xlsx": true, ".bmp": true,
	".ico": true, ".tiff": true, ".webp": true, ".svg": true,
}

func IsBinaryFile(path string) bool {
	return binaryExtensions[strings.ToLower(filepath.Ext(path))]
}

// IndexProgress tracks the current state of the workspace index.
type IndexProgress struct {
	FilesIndexed   int
	SymbolsIndexed int
	DocsIndexed    int
	TotalFiles     int
	Complete       bool
	Status         IndexStatus
	CurrentFile    string
	LastBuilt      time.Time
	Stale          bool
	// UpdatedSinceBuild counts files the watcher has refreshed since the
	// last full build. It is a hint that the index lags the working tree;
	// the walk's reconcile pass (PruneNotIndexed) is what actually removes
	// deletions and newly-ignored files, on both incremental and full runs.
	UpdatedSinceBuild int
}

// GetProgress returns the current indexing progress, combining the
// durable counts with the live in-memory build state.
func (s *Store) GetProgress(ctx context.Context) (*IndexProgress, error) {
	symbols, err := s.CountSymbols(ctx)
	if err != nil {
		return nil, err
	}
	docs, err := s.CountDocs(ctx)
	if err != nil {
		return nil, err
	}
	files, err := s.CountFiles(ctx)
	if err != nil {
		return nil, err
	}

	s.bmu.RLock()
	status := s.buildStatus
	current := s.currentFile
	total := s.totalFiles
	lastBuilt := s.lastBuilt
	completed := s.buildCompleted
	sinceBuild := s.updatedSinceBuild
	building := status == IndexStatusIndexing
	s.bmu.RUnlock()

	// Complete means "has data and no build is mid-flight," so the
	// sidebar still reports data once anything is indexed, but reports
	// not-complete during a run or after a failure.
	complete := (completed || files > 0) && !building && status != IndexStatusError
	stale := files > 0 && !completed && !building
	if total == 0 {
		total = files
	}

	return &IndexProgress{
		FilesIndexed:      files,
		SymbolsIndexed:    symbols,
		DocsIndexed:       docs,
		TotalFiles:        total,
		Complete:          complete,
		Status:            status,
		CurrentFile:       current,
		LastBuilt:         lastBuilt,
		Stale:             stale,
		UpdatedSinceBuild: sinceBuild,
	}, nil
}

// BeginBuild marks a full build as running with the given total file count.
func (s *Store) BeginBuild(total int) {
	s.bmu.Lock()
	s.buildStatus = IndexStatusIndexing
	s.totalFiles = total
	s.filesIndexed = 0
	s.currentFile = ""
	s.buildCompleted = false
	s.updatedSinceBuild = 0
	s.bmu.Unlock()
}

// UpdateBuildProgress records incremental progress for a running build.
func (s *Store) UpdateBuildProgress(indexed int, currentFile string) {
	s.bmu.Lock()
	s.filesIndexed = indexed
	s.currentFile = currentFile
	if s.buildStatus != IndexStatusIndexing {
		s.buildStatus = IndexStatusIndexing
	}
	s.bmu.Unlock()
}

// FinishBuild marks the current build complete and persists its durable
// fields (completion time and total file count) to the meta table.
func (s *Store) FinishBuild(ctx context.Context, total int) {
	s.bmu.Lock()
	s.buildStatus = IndexStatusComplete
	s.buildCompleted = true
	s.filesIndexed = total
	s.currentFile = ""
	s.lastBuilt = time.Now()
	if total > 0 {
		s.totalFiles = total
	}
	s.updatedSinceBuild = 0
	lastBuilt := s.lastBuilt
	totalFiles := s.totalFiles
	s.bmu.Unlock()

	s.setMeta(ctx, metaKeyLastBuilt, strconv.FormatInt(int64(lastBuilt.Unix()), 10))
	s.setMeta(ctx, metaKeyTotalFiles, strconv.Itoa(totalFiles))
	s.setMeta(ctx, metaKeyCompleted, "1")
}

// FailBuild records that the running build ended in error.
func (s *Store) FailBuild(_ context.Context, msg string) {
	s.bmu.Lock()
	s.buildStatus = IndexStatusError
	s.currentFile = msg
	s.bmu.Unlock()
}

// MarkIncrementalUpdate notes that the watcher has refreshed at least one
// file since the last full build.
func (s *Store) MarkIncrementalUpdate() {
	s.bmu.Lock()
	if s.buildCompleted {
		s.updatedSinceBuild++
	}
	s.bmu.Unlock()
}

const (
	metaKeyLastBuilt  = "last_built"
	metaKeyTotalFiles = "total_files"
	metaKeyCompleted  = "build_completed"
)

// loadMeta restores the durable build fields from the meta table.
func (s *Store) loadMeta(ctx context.Context) {
	if v, ok := s.getMeta(ctx, metaKeyLastBuilt); ok {
		if unix, err := strconv.Atoi(v); err == nil {
			s.lastBuilt = time.Unix(int64(unix), 0)
		}
	}
	if v, ok := s.getMeta(ctx, metaKeyTotalFiles); ok {
		if n, err := strconv.Atoi(v); err == nil {
			s.totalFiles = n
		}
	}
	if v, ok := s.getMeta(ctx, metaKeyCompleted); ok && v == "1" {
		s.buildCompleted = true
		s.buildStatus = IndexStatusComplete
	}
}

func (s *Store) getMeta(ctx context.Context, key string) (string, bool) {
	var value string
	err := s.db.QueryRowContext(ctx, "SELECT value FROM meta WHERE key = ?", key).Scan(&value)
	if err != nil {
		return "", false
	}
	return value, true
}

func (s *Store) setMeta(ctx context.Context, key, value string) {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	s.db.ExecContext(ctx,
		"INSERT INTO meta(key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value",
		key, value,
	)
}

func isExcluded(relPath string, patterns []string) bool {
	for _, p := range patterns {
		if match(relPath, p) {
			return true
		}
	}
	return false
}

func match(path, pattern string) bool {
	p := strings.TrimSpace(pattern)
	p = strings.Trim(filepath.ToSlash(p), "/")
	if p == "" {
		return false
	}
	rel := filepath.ToSlash(path)

	// Basename glob, e.g. "*.log".
	if ok, _ := filepath.Match(p, filepath.Base(rel)); ok {
		return true
	}
	// Whole relative path, e.g. "docs/plans/x.md".
	if ok, _ := filepath.Match(p, rel); ok {
		return true
	}
	// Directory subtree, e.g. "other_project_research" or "docs/plans":
	// honor a leading path prefix or any interior path segment, which is
	// what lets a bare directory name exclude everything nested under it.
	if !strings.ContainsAny(p, "*?[") {
		if strings.HasPrefix(rel, p+"/") {
			return true
		}
		segs := strings.Split(rel, "/")
		if slices.Contains(segs[:len(segs)-1], p) {
			return true
		}
	}
	return false
}
