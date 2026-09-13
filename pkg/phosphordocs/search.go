package phosphordocs

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
	"sync"

	_ "modernc.org/sqlite"
)

// Result is a single ranked hit from Search.
type Result struct {
	// RelPath is the docs-root-relative path of the matching document.
	RelPath string
	// VirtualPath is the path to pass to the View tool to read the document.
	VirtualPath string
	// Title is the document's first heading.
	Title string
	// Snippet is a short, highlighted excerpt around the match.
	Snippet string
	// Score is the BM25 relevance (lower is better, following the FTS5 convention
	// used by the workspace index).
	Score float64
}

const docBM25 = "bm25(docs_fts, 1.0, 6.0, 10.0)"

var searchQuery = fmt.Sprintf(
	"SELECT path, content, %s AS score "+
		"FROM docs_fts WHERE docs_fts MATCH ? "+
		"ORDER BY %s ASC LIMIT ?",
	docBM25, docBM25,
)

var (
	buildOnce sync.Once
	builtDB   *sql.DB
	builtErr  error
)

// store returns the lazily built, process-lifetime in-memory FTS5 index over the
// embedded corpus. It is built once on first use and never written to again, so
// it stays side-effect-free and dies with the process. The single-connection pool
// mirrors the workspace index: a ":memory:" database only persists for the
// lifetime of its connection, so the pool must not recycle it.
func store() (*sql.DB, error) {
	buildOnce.Do(func() {
		db, err := sql.Open("sqlite", ":memory:")
		if err != nil {
			builtErr = fmt.Errorf("open docs index: %w", err)
			return
		}
		db.SetMaxOpenConns(1)
		if err := db.PingContext(context.Background()); err != nil {
			db.Close()
			builtErr = fmt.Errorf("ping docs index: %w", err)
			return
		}
		if err := populate(db); err != nil {
			db.Close()
			builtErr = err
			return
		}
		builtDB = db
	})
	return builtDB, builtErr
}

// populate creates the FTS5 table and inserts every shipped document.
func populate(db *sql.DB) error {
	ctx := context.Background()
	const schema = `CREATE VIRTUAL TABLE IF NOT EXISTS docs_fts USING fts5(path, title, content)`
	if _, err := db.ExecContext(ctx, schema); err != nil {
		return fmt.Errorf("create docs_fts: %w", err)
	}
	const insert = `INSERT INTO docs_fts(path, title, content) VALUES (?, ?, ?)`
	stmt, err := db.PrepareContext(ctx, insert)
	if err != nil {
		return fmt.Errorf("prepare insert: %w", err)
	}
	defer stmt.Close()

	var walkErr error
	fs.WalkDir(contentFS, contentRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			walkErr = err
			return nil
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".md") {
			return nil
		}
		data, err := contentFS.ReadFile(p)
		if err != nil {
			walkErr = err
			return nil
		}
		rel, _ := filepath.Rel(contentRoot, filepath.FromSlash(p))
		rel = filepath.ToSlash(rel)
		relPath := ToVirtual(rel)
		title := titleOf(rel)
		if _, err := stmt.ExecContext(ctx, relPath, title, string(data)); err != nil {
			walkErr = fmt.Errorf("index %s: %w", rel, err)
		}
		return nil
	})
	return walkErr
}

// Search runs a lexical BM25 lookup over the embedded corpus and returns the
// top-ranked documents. The query is treated as a bag of terms OR'd together so a
// partially remembered phrase still returns useful hits; punctuation and FTS
// operator characters are neutralized rather than fed to the MATCH parser.
func Search(ctx context.Context, query string, limit int) ([]Result, error) {
	if limit <= 0 {
		limit = 10
	} else if limit > 50 {
		limit = 50
	}
	match := toMatchQuery(query)
	if match == "" {
		return nil, nil
	}

	db, err := store()
	if err != nil {
		return nil, err
	}

	rows, err := db.QueryContext(ctx, searchQuery, match, limit)
	if err != nil {
		return nil, fmt.Errorf("search docs: %w", err)
	}
	defer rows.Close()

	var results []Result
	for rows.Next() {
		var (
			path, content string
			score         float64
		)
		if err := rows.Scan(&path, &content, &score); err != nil {
			return nil, fmt.Errorf("scan doc row: %w", err)
		}
		rel := RelFromVirtual(path)
		results = append(results, Result{
			RelPath:     rel,
			VirtualPath: path,
			Title:       titleOf(rel),
			Snippet:     snippetOf(content),
			Score:       score,
		})
	}
	return results, rows.Err()
}

// toMatchQuery converts a free-form query into a safe FTS5 MATCH expression: each
// term is quoted (so operators and punctuation cannot break the parser) and the
// terms are OR'd for recall. Returns "" when there is nothing to search.
func toMatchQuery(query string) string {
	var terms []string
	for _, token := range strings.FieldsFunc(query, func(r rune) bool {
		return !(r == '_' || (r >= '0' && r <= '9') ||
			(r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z'))
	}) {
		terms = append(terms, `"`+strings.ReplaceAll(token, `"`, `""`)+`"`)
	}
	return strings.Join(terms, " OR ")
}

// snippetOf returns a compact, single-line excerpt of a document body for search
// results. Highlighting is left to the caller; the corpus is small enough that the
// agent will open the full file after choosing a hit.
func snippetOf(content string) string {
	trimmed := strings.TrimSpace(content)
	if idx := strings.Index(trimmed, "\n"); idx != -1 {
		trimmed = trimmed[min(idx, len(trimmed)):]
		trimmed = strings.TrimSpace(trimmed)
	}
	if len(trimmed) > 280 {
		return trimmed[:280] + "…"
	}
	return trimmed
}
