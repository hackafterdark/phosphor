// Package workspaceindex provides FTS5-based workspace search.
package workspaceindex

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

// Store manages the SQLite database with FTS5 tables.
type Store struct {
	db *sql.DB
}

// NewStore opens or creates the workspace index database at
// .phosphor/workspace_index.db inside the workspace directory.
func NewStore(workspaceDir string) (*Store, error) {
	dbPath := filepath.Join(workspaceDir, ".phosphor", "workspace_index.db")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return nil, fmt.Errorf("create data directory: %w", err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open store db: %w", err)
	}

	if err := db.PingContext(context.Background()); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping db: %w", err)
	}

	db.Exec("PRAGMA journal_mode=WAL")

	store := &Store{db: db}
	if err := store.init(); err != nil {
		db.Close()
		return nil, fmt.Errorf("init schema: %w", err)
	}

	return store, nil
}

func (s *Store) init() error {
	stmts := []string{
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
	}

	for _, stmt := range stmts {
		if _, err := s.db.Exec(stmt); err != nil {
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
	if _, err := s.db.ExecContext(ctx, "DELETE FROM symbols_fts WHERE path = ?", path); err != nil {
		return fmt.Errorf("delete symbols: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, "DELETE FROM docs_fts WHERE path = ?", path); err != nil {
		return fmt.Errorf("delete docs: %w", err)
	}
	return nil
}

func (s *Store) InsertSymbol(ctx context.Context, path, name, qualifiedName, signature, documentation string) error {
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
	Signature     string
	Documentation string
	Content       string
}

func (s *Store) SearchSymbols(ctx context.Context, query string, limit int) ([]SearchResult, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT path, name, qualified_name, signature, documentation FROM symbols_fts WHERE symbols_fts MATCH ? LIMIT ?`,
		query, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("search symbols: %w", err)
	}
	defer rows.Close()

	var results []SearchResult
	for rows.Next() {
		var r SearchResult
		if err := rows.Scan(&r.Path, &r.Name, &r.QualifiedName, &r.Signature, &r.Documentation); err != nil {
			return nil, fmt.Errorf("scan row: %w", err)
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

func (s *Store) SearchDocs(ctx context.Context, query string, limit int) ([]SearchResult, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT path, content FROM docs_fts WHERE docs_fts MATCH ? LIMIT ?`,
		query, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("search docs: %w", err)
	}
	defer rows.Close()

	var results []SearchResult
	for rows.Next() {
		var r SearchResult
		if err := rows.Scan(&r.Path, &r.Content); err != nil {
			return nil, fmt.Errorf("scan row: %w", err)
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

func (s *Store) SearchAll(ctx context.Context, query string, limit int) ([]SearchResult, error) {
	symbols, err := s.SearchSymbols(ctx, query, limit)
	if err != nil {
		return nil, err
	}
	docs, err := s.SearchDocs(ctx, query, limit)
	if err != nil {
		return nil, err
	}
	return append(symbols, docs...), nil
}

func (s *Store) Clear(ctx context.Context) error {
	stmts := []string{
		"DROP TABLE IF EXISTS symbols_fts",
		"DROP TABLE IF EXISTS docs_fts",
		"DROP TABLE IF EXISTS file_hashes",
	}
	for _, stmt := range stmts {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("clear table: %w", err)
		}
	}
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
}

// GetProgress returns the current indexing progress.
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
	return &IndexProgress{
		FilesIndexed:   files,
		SymbolsIndexed: symbols,
		DocsIndexed:    docs,
		Complete:       files > 0,
	}, nil
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
	// Simple glob-style matching. For production, use path.Match.
	matched, _ := filepath.Match(pattern, filepath.Base(path))
	return matched
}
