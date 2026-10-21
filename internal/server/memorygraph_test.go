package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func seedMemoryDB(t *testing.T) string {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "memory.db")
	db, err := sql.Open("sqlite", dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	schema := []string{
		`CREATE TABLE entries (
			id TEXT PRIMARY KEY, type TEXT, status TEXT, thread TEXT,
			trust REAL, recall_count INTEGER, hot_score REAL,
			created_ts TEXT, updated_ts TEXT, supersedes TEXT, from_decision TEXT,
			summary TEXT, body TEXT
		)`,
		`CREATE TABLE tags (id INTEGER PRIMARY KEY AUTOINCREMENT, term TEXT UNIQUE)`,
		`CREATE TABLE entry_tags (entry_id TEXT, tag_id INTEGER, PRIMARY KEY (entry_id, tag_id))`,
	}
	for _, stmt := range schema {
		_, err := db.Exec(stmt)
		require.NoError(t, err)
	}

	_, err = db.Exec(
		`INSERT INTO entries (id, type, status, thread, trust, recall_count, hot_score, ` +
			`created_ts, updated_ts, supersedes, from_decision, summary, body) VALUES ` +
			`('e1', 'decision', 'active', 'alpha', 0.8, 3, 12.5, '2026-10-01T00:00:00Z', '2026-10-01T00:00:00Z', '', '', 'first', 'body one'),` +
			`('e2', 'decision', 'active', 'alpha', 0.5, 0, 0.0, '2026-10-02T00:00:00Z', '2026-10-02T00:00:00Z', 'e1', '', 'second', 'body two'),` +
			`('e3', 'reference', 'active', '', 0.5, 1, 2.0, '2026-10-02T00:00:00Z', '2026-10-02T00:00:00Z', '', 'e1', 'third', 'body three')`)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO tags (id, term) VALUES (1, 'x'), (2, 'y'), (3, 'z')`)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO entry_tags (entry_id, tag_id) VALUES ('e1', 1), ('e1', 2), ('e3', 3)`)
	require.NoError(t, err)
	return dbPath
}

func TestBuildMemoryGraph(t *testing.T) {
	t.Parallel()
	dbPath := seedMemoryDB(t)
	g, err := buildMemoryGraph(context.Background(), dbPath)
	require.NoError(t, err)

	byID := map[string]*graphNode{}
	for _, n := range g.Nodes {
		byID[n.ID] = n
	}
	// 3 entries + 3 tag hubs + 1 thread hub.
	require.Len(t, g.Nodes, 7)
	require.Contains(t, byID, "tag:x")
	require.Contains(t, byID, "thread:alpha")
	require.Equal(t, int64(2), byID["thread:alpha"].Recall)

	kinds := map[string]int{}
	for _, e := range g.Edges {
		kinds[e.Kind]++
	}
	require.Equal(t, 1, kinds["supersedes"])
	require.Equal(t, 1, kinds["decision"])
	require.Equal(t, 2, kinds["thread"])
	require.Equal(t, 3, kinds["tag"])

	require.Equal(t, int64(2), g.Verdicts["decision"])
	require.Equal(t, int64(1), g.Verdicts["reference"])
	require.Equal(t, int64(3), g.Verdicts["tag"])
	require.Equal(t, int64(2), g.Arrivals["2026-10-02"])
}

func TestMemoryGraphDBPathDiscovery(t *testing.T) {
	ws := t.TempDir()
	vault := filepath.Join(ws, ".phosphor", "memory", ".phosphor-index")
	require.NoError(t, os.MkdirAll(vault, 0o755))
	dbPath := filepath.Join(vault, "memory.db")
	fd, err := os.Create(dbPath)
	require.NoError(t, err)
	require.NoError(t, fd.Close())

	orig := memoryGraphWorkspace
	t.Cleanup(func() { memoryGraphWorkspace = orig })
	memoryGraphWorkspace = ws
	require.Equal(t, dbPath, memoryGraphDBPath())
}

func TestMemoryGraphHandlers(t *testing.T) {
	ws := t.TempDir()
	vault := filepath.Join(ws, ".phosphor", "memory", ".phosphor-index")
	require.NoError(t, os.MkdirAll(vault, 0o755))
	orig := memoryGraphWorkspace
	t.Cleanup(func() { memoryGraphWorkspace = orig })
	memoryGraphWorkspace = ws

	seed := seedMemoryDB(t)
	data, err := os.ReadFile(seed)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(vault, "memory.db"), data, 0o644))

	req := httptest.NewRequest(http.MethodGet, "/graph.json", nil)
	rec := httptest.NewRecorder()
	HandleMemoryGraphData(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	var g memoryGraph
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &g))
	require.Len(t, g.Nodes, 7)

	req = httptest.NewRequest(http.MethodGet, "/view.html", nil)
	rec = httptest.NewRecorder()
	HandleMemoryGraphView(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	page := rec.Body.String()
	require.NotContains(t, page, cosmosPlaceholder)
	require.NotContains(t, page, "@cosmos.gl/graph")
	require.Contains(t, page, cosmosGraphJS[:200])
}

func TestMemoryGraphHandlerHostValidation(t *testing.T) {
	handler := NewMemoryGraphHandler()
	for _, host := range []string{"localhost:8644", "127.0.0.1:8644", "[::1]:8644", "localhost"} {
		req := httptest.NewRequest(http.MethodGet, "/view.html", nil)
		req.Host = host
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code, "host %s should be allowed", host)
	}
	for _, host := range []string{"evil.example.com", "attacker.test:8644"} {
		req := httptest.NewRequest(http.MethodGet, "/graph.json", nil)
		req.Host = host
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		require.Equal(t, http.StatusForbidden, rec.Code, "host %s should be rejected", host)
	}
}
func TestRenderMemoryGraphReport(t *testing.T) {
	t.Parallel()
	dbPath := seedMemoryDB(t)
	page, err := RenderMemoryGraphReport(context.Background(), dbPath)
	require.NoError(t, err)
	html := string(page)
	require.Contains(t, html, "window.__PHOSPHOR_SNAPSHOT__ = true;")
	require.Contains(t, html, "window.__PHOSPHOR_GRAPH__ = ")
	require.NotContains(t, html, cosmosPlaceholder)
	require.NotContains(t, html, "@cosmos.gl/graph")
	require.Contains(t, html, cosmosGraphJS[:200])
	// The bundle must stay byte-intact: the snapshot payload goes into the
	// real </head>, never into the "</head>" literal inside the bundle.
	require.Contains(t, html, cosmosGraphJS)
	require.Contains(t, html, ";</script>\n</head>")

	// The embedded payload must parse back into the same graph.
	_, payload, ok := strings.Cut(html, "window.__PHOSPHOR_GRAPH__ = ")
	require.True(t, ok)
	payload, _, ok = strings.Cut(payload, ";</script>")
	require.True(t, ok)
	payload = strings.ReplaceAll(payload, `<\/`, `</`)
	var g memoryGraph
	require.NoError(t, json.Unmarshal([]byte(payload), &g))
	require.Len(t, g.Nodes, 7)
}
