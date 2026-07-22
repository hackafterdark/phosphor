// Package embeddings provides a SQLite-backed vector store using the
// sqlite-vec extension for efficient ANN search over codebase embeddings.
package embeddings

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
	_ "modernc.org/sqlite/vec"
)

// Store wraps a separate SQLite database for codebase embeddings.
type Store struct {
	db *sql.DB
}

// NewStore opens or creates the codebase index database at
// .phosphor/codebase_index.db inside the workspace directory.
func NewStore(workspaceDir string) (*Store, error) {
	dbPath := filepath.Join(workspaceDir, ".phosphor", "codebase_index.db")
	// Ensure the .phosphor directory exists.
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return nil, fmt.Errorf("create data directory: %w", err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open store db: %w", err)
	}

	// Enable WAL mode so readers (semantic_search) can query
	// while the indexer is writing.
	db.Exec("PRAGMA journal_mode=WAL")

	// The vec0 extension is auto-registered by importing modernc.org/sqlite/vec.

// Create the vector virtual table.
	db.Exec(`
		CREATE VIRTUAL TABLE IF NOT EXISTS vec_chunks USING vec0(
			vector float[384]
		)
	`)
	// Create the metadata table.
	db.Exec(`
		CREATE TABLE IF NOT EXISTS chunk_meta (
			id           TEXT PRIMARY KEY,
			file_path    TEXT NOT NULL,
			content_hash TEXT,
			offset       INT  NOT NULL,
			length       INT  NOT NULL,
			content      TEXT
		)
	`)
	db.Exec("CREATE INDEX IF NOT EXISTS idx_meta_file ON chunk_meta(file_path)")
	db.Exec("CREATE INDEX IF NOT EXISTS idx_meta_hash ON chunk_meta(content_hash)")
	// Ensure existing databases get the content_hash column.
	db.Exec("ALTER TABLE chunk_meta ADD COLUMN content_hash TEXT")

	store := &Store{db: db}

	// Backfill content_hash for rows that don't have one.
	if err := store.backfillHashes(context.Background()); err != nil {
		slog.Warn("Failed to backfill content hashes", "error", err)
	}

	return store, nil
}

// backfillHashes computes and stores content_hash for rows where it is NULL.
func (s *Store) backfillHashes(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, "SELECT id, content FROM chunk_meta WHERE content_hash IS NULL")
	if err != nil {
		return fmt.Errorf("query null hashes: %w", err)
	}
	defer rows.Close()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin backfill tx: %w", err)
	}
	upsert, err := tx.PrepareContext(ctx, "UPDATE chunk_meta SET content_hash = ? WHERE id = ?")
	if err != nil {
		tx.Rollback()
		return fmt.Errorf("prepare upsert: %w", err)
	}
	defer upsert.Close()

	for rows.Next() {
		var id, content string
		if err := rows.Scan(&id, &content); err != nil {
			continue
		}
		hash := contentHash(content)
		upsert.ExecContext(ctx, hash, id)
	}
	rows.Close()
	upsert.Close()

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit backfill: %w", err)
	}
	return rows.Err()
}

// contentHash computes a SHA-256 hash of the given content string.
func contentHash(content string) string {
	h := sha256.Sum256([]byte(content))
	return hex.EncodeToString(h[:])
}

// Insert stores an embedding vector and its metadata.
func (s *Store) Insert(ctx context.Context, filePath, contentHash, content string, offset, length int, vector []float32) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}

// Insert vector into vec_chunks.
	_, err = tx.ExecContext(ctx,
		"INSERT INTO vec_chunks(vector) VALUES (vec_f32(?))",
		vectorToBlob(vector))
	if err != nil {
		tx.Rollback()
		return fmt.Errorf("insert vector: %w", err)
	}

	// Insert metadata, linking to the vec_chunks rowid.
	rowRow := tx.QueryRow("SELECT last_insert_rowid()")
	var rowId int64
	rowRow.Scan(&rowId)
	_, err = tx.ExecContext(ctx,
		"INSERT INTO chunk_meta(id, file_path, content_hash, offset, length, content) VALUES (?, ?, ?, ?, ?, ?)",
		fmt.Sprintf("%d", rowId), filePath, contentHash, offset, length, content)
	if err != nil {
		tx.Rollback()
		return fmt.Errorf("insert meta: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// Migrate ensures the vec_chunks table exists.
// Also ensures chunk_meta has all required columns.
func (s *Store) Migrate(ctx context.Context) error {
	db := s.db
	// Ensure chunk_meta has content_hash column.
	_, err := db.ExecContext(ctx, "ALTER TABLE chunk_meta ADD COLUMN content_hash TEXT")
	if err != nil {
		// Column might already exist — ignore this error.
	}
	return nil
}

// Search finds the top-k nearest neighbors for a query vector.
func (s *Store) Search(ctx context.Context, query []float32, k int) ([]SearchResult, error) {
	queryBlob := vectorToBlob(query)
rows, err := s.db.QueryContext(ctx, `
		SELECT chunk_meta.id, chunk_meta.file_path, chunk_meta.offset, chunk_meta.content, sub.distance
		FROM chunk_meta
		JOIN (SELECT rowid, distance FROM vec_chunks WHERE vector MATCH vec_f32(?) LIMIT ?) sub
		ON chunk_meta.id = CAST(sub.rowid AS TEXT)
		ORDER BY sub.distance ASC
	`, queryBlob, k)
	if err != nil {
		return nil, fmt.Errorf("search query: %w", err)
	}
	defer rows.Close()

	var results []SearchResult
	for rows.Next() {
		var r SearchResult
		var dist sql.NullFloat64
		if err := rows.Scan(&r.ID, &r.FilePath, &r.Offset, &r.Content, &dist); err != nil {
			return nil, fmt.Errorf("scan row: %w", err)
		}
		if dist.Valid {
			r.Distance = dist.Float64
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

// Clear drops and recreates all index tables.
func (s *Store) Clear(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, "DROP TABLE IF EXISTS vec_chunks"); err != nil {
		return fmt.Errorf("drop vec_chunks: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, "DROP TABLE IF EXISTS chunk_meta"); err != nil {
		return fmt.Errorf("drop chunk_meta: %w", err)
	}

	// Recreate tables with correct schema.
	if _, err := s.db.ExecContext(ctx, `
		CREATE VIRTUAL TABLE vec_chunks USING vec0(vector float[384])
	`); err != nil {
		return fmt.Errorf("recreate vec_chunks: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `
		CREATE TABLE chunk_meta (
			id           TEXT PRIMARY KEY,
			file_path    TEXT NOT NULL,
			content_hash TEXT,
			offset       INT  NOT NULL,
			length       INT  NOT NULL,
			content      TEXT
		)
	`); err != nil {
		return fmt.Errorf("recreate chunk_meta: %w", err)
	}
	s.db.ExecContext(ctx, "CREATE INDEX IF NOT EXISTS idx_meta_file ON chunk_meta(file_path)")
	s.db.ExecContext(ctx, "CREATE INDEX IF NOT EXISTS idx_meta_hash ON chunk_meta(content_hash)")

	return nil
}

// Count returns the total number of indexed chunks.
func (s *Store) Count(ctx context.Context) (int, error) {
	row := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM chunk_meta")
	var count int
	if err := row.Scan(&count); err != nil {
		return 0, fmt.Errorf("count query: %w", err)
	}
	return count, nil
}

// IsChunkIndexed returns true if a chunk with the given ID already exists.
func (s *Store) IsChunkIndexed(ctx context.Context, chunkID string) (bool, error) {
	row := s.db.QueryRowContext(ctx, "SELECT 1 FROM chunk_meta WHERE id = ?", chunkID)
	return row.Err() == nil, nil
}

// GetIndexedFiles returns the set of file paths that already have chunks in the index.
func (s *Store) GetIndexedFiles(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT DISTINCT file_path FROM chunk_meta")
	if err != nil {
		return nil, fmt.Errorf("query indexed files: %w", err)
	}
	defer rows.Close()

	var files []string
	for rows.Next() {
		var fp string
		if err := rows.Scan(&fp); err != nil {
			continue
		}
		files = append(files, fp)
	}
	return files, rows.Err()
}

// GetFileHash returns the stored content hash for a file path, or empty string if not found.
func (s *Store) GetFileHash(ctx context.Context, filePath string) (string, error) {
	row := s.db.QueryRowContext(ctx, "SELECT content_hash FROM chunk_meta WHERE file_path = ? LIMIT 1", filePath)
	var hash sql.NullString
	err := row.Scan(&hash)
	if err != nil {
		// "no rows in result set" means the file is not indexed.
		if strings.Contains(err.Error(), "no rows in result set") {
			return "", nil
		}
		return "", fmt.Errorf("query file hash: %w", err)
	}
	if hash.Valid {
		return hash.String, nil
	}
	return "", nil
}

// IsFileIndexed returns true if the file already has chunks in the store.
func (s *Store) IsFileIndexed(ctx context.Context, filePath string) (bool, error) {
	row := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM chunk_meta WHERE file_path = ?", filePath)
	var count int
	if err := row.Scan(&count); err != nil {
		return false, fmt.Errorf("query file index: %w", err)
	}
	return count > 0, nil
}

// Close releases the database connection.
func (s *Store) Close() error {
	return s.db.Close()
}

// SearchResult holds a single semantic search hit.
type SearchResult struct {
	ID        string
	FilePath  string
	Offset    int
	Content   string
	Distance  float64
}

// vectorToBlob converts a float32 slice to a byte blob for sqlite-vec.
func vectorToBlob(v []float32) []byte {
	blob := make([]byte, len(v)*4)
	for i, f := range v {
		bits := math.Float32bits(f)
		binary.LittleEndian.PutUint32(blob[i*4:], bits)
	}
	return blob
}