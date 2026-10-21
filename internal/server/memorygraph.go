package server

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/hackafterdark/phosphor/internal/home"
	"github.com/hackafterdark/phosphor/internal/memory"
)

// memoryGraphWorkspace is the workspace directory used to discover the
// project memory vault. Empty falls back to the process working directory.
var memoryGraphWorkspace string

// SetMemoryGraphWorkspace sets the workspace used to discover the memory
// vault for the graph viewer endpoints.
func SetMemoryGraphWorkspace(dir string) {
	memoryGraphWorkspace = dir
}

// memoryGraphDBPath locates the memory index database: project vaults found
// by walking up from the workspace directory, then the global vault.
func memoryGraphDBPath() string {
	start := memoryGraphWorkspace
	if start == "" {
		if cwd, err := os.Getwd(); err == nil {
			start = cwd
		}
	}
	rel := filepath.Join(
		memory.PhosphorDirName, memory.VaultDirName,
		memory.IndexDirName, memory.IndexFileName,
	)
	if start != "" {
		for d := start; ; {
			candidate := filepath.Join(d, rel)
			if _, err := os.Stat(candidate); err == nil {
				return candidate
			}
			parent := filepath.Dir(d)
			if parent == d {
				break
			}
			d = parent
		}
	}
	candidate := filepath.Join(
		home.Dir(), memory.PhosphorDirName, memory.VaultDirName,
		memory.IndexDirName, memory.IndexFileName,
	)
	if _, err := os.Stat(candidate); err == nil {
		return candidate
	}
	return ""
}

// cosmosPlaceholder marks where the bundled cosmos.gl runtime is injected
// into the viewer page.
const cosmosPlaceholder = "<!--PHOSPHOR_COSMOS_JS-->"

// hydrateCosmos replaces the cosmos placeholder with the bundled runtime
// wrapped in a script tag.
func hydrateCosmos(page string) string {
	// Escape the close-tag sequence so a literal inside the bundle cannot
	// terminate the injected script tag early.
	js := strings.ReplaceAll(cosmosGraphJS, "</script>", `<\/script>`)
	return strings.Replace(page, cosmosPlaceholder, "<script>"+js+"</script>", 1)
}

// viewerPage returns the viewer HTML with the bundled cosmos.gl runtime
// inlined, so both the live viewer and exported reports render with no
// network access and no CDN dependency.
var viewerPage = sync.OnceValues(func() (string, error) {
	if !strings.Contains(memoryGraphHTML, cosmosPlaceholder) {
		return "", errors.New("viewer page has no " + cosmosPlaceholder + " placeholder")
	}
	return hydrateCosmos(memoryGraphHTML), nil
})

// HandleMemoryGraphView serves the embedded memory graph viewer page.
func HandleMemoryGraphView(w http.ResponseWriter, _ *http.Request) {
	page, err := viewerPage()
	if err != nil {
		http.Error(w, "Failed to hydrate viewer page: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	io.WriteString(w, page)
}

// HandleMemoryGraphData builds the graph for the current memory vault on
// every request, so the viewer's polling sees fresh entries.
func HandleMemoryGraphData(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	dbPath := memoryGraphDBPath()
	if dbPath == "" {
		http.Error(w, "No memory vault found", http.StatusNotFound)
		return
	}
	graph, err := buildMemoryGraph(r.Context(), dbPath)
	if err != nil {
		http.Error(w, "Failed to build memory graph: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(graph); err != nil {
		slog.Error("Failed to encode memory graph", "error", err)
	}
}

// NewMemoryGraphHandler returns an http.Handler serving the viewer page at
// / (and /view.html) and the graph data at /graph.json.
//
// The Host header is validated against loopback names: the server binds
// loopback-only, and rejecting foreign Host values closes the DNS-rebinding
// path where a malicious page resolves its own hostname to 127.0.0.1 and
// reads /graph.json as same-origin.
func NewMemoryGraphHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/view.html", HandleMemoryGraphView)
	mux.HandleFunc("/graph.json", HandleMemoryGraphData)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			HandleMemoryGraphView(w, r)
			return
		}
		http.NotFound(w, r)
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		if h, err := netip.ParseAddr(host); err == nil && h.IsLoopback() {
			mux.ServeHTTP(w, r)
			return
		}
		if host == "localhost" {
			mux.ServeHTTP(w, r)
			return
		}
		http.Error(w, "Forbidden: host not allowed", http.StatusForbidden)
	})
}

// MemoryGraphDBPath returns the path of the discovered memory vault index,
// or "" when no vault exists.
func MemoryGraphDBPath() string {
	return memoryGraphDBPath()
}

// RenderMemoryGraphReport freezes the current graph into a single
// self-contained HTML report. The page opens offline with no server and no
// polling; its snapshot mode reads the embedded payload instead.
func RenderMemoryGraphReport(ctx context.Context, dbPath string) ([]byte, error) {
	graph, err := buildMemoryGraph(ctx, dbPath)
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(graph)
	if err != nil {
		return nil, err
	}
	// Escape </ sequences so a "</script>" inside any entry body cannot
	// terminate the payload tag early.
	payload = bytes.ReplaceAll(payload, []byte("</"), []byte(`<\/`))
	// Insert the payload into the bare template before hydrating cosmos.gl:
	// the bundle itself contains a literal "</head>" inside a string, so
	// replacing after injection would splice the payload into the bundle.
	if !strings.Contains(memoryGraphHTML, "</head>") {
		return nil, fmt.Errorf("viewer page has no </head>; cannot embed snapshot payload")
	}
	head := "<script>window.__PHOSPHOR_SNAPSHOT__ = true;\n" +
		"window.__PHOSPHOR_GRAPH__ = " + string(payload) + ";</script>\n</head>"
	page := strings.Replace(memoryGraphHTML, "</head>", head, 1)
	return []byte(hydrateCosmos(page)), nil
}

type graphNode struct {
	ID      string   `json:"id"`
	Type    string   `json:"type"`
	Status  string   `json:"status"`
	Thread  string   `json:"thread"`
	Tags    []string `json:"tags"`
	Summary string   `json:"summary"`
	Body    string   `json:"body,omitempty"`
	Trust   float64  `json:"trust"`
	Recall  int64    `json:"recall"`
	Hot     float64  `json:"hot"`
	Created string   `json:"created"`
	Updated string   `json:"updated"`
}

type graphEdge struct {
	Source string `json:"source"`
	Target string `json:"target"`
	Kind   string `json:"kind"`
}

type memoryGraph struct {
	Generated string           `json:"generated"`
	DB        string           `json:"db"`
	Nodes     []*graphNode     `json:"nodes"`
	Edges     []graphEdge      `json:"edges"`
	Verdicts  map[string]int64 `json:"verdicts"`
	Arrivals  map[string]int64 `json:"arrivals"`
}

// maxTagHubs bounds the number of tag hub nodes drawn, matching the
// reference builder.
const maxTagHubs = 12

// buildMemoryGraph reads the vault index read-only and clusters entries by
// thread and tag, mirroring the tools/memorygraph builder.
func buildMemoryGraph(ctx context.Context, dbPath string) (*memoryGraph, error) {
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(dbPath)+"?mode=ro")
	if err != nil {
		return nil, err
	}
	defer db.Close()

	tagsOf := map[string][]string{}
	rows, err := db.QueryContext(ctx,
		"SELECT et.entry_id, t.term FROM entry_tags et JOIN tags t ON t.id = et.tag_id")
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var e, term string
		if err := rows.Scan(&e, &term); err != nil {
			rows.Close()
			return nil, err
		}
		tagsOf[e] = append(tagsOf[e], term)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	graph := &memoryGraph{
		Generated: time.Now().UTC().Format(time.RFC3339),
		DB:        dbPath,
		Nodes:     []*graphNode{},
		Edges:     []graphEdge{},
		Verdicts:  map[string]int64{},
		Arrivals:  map[string]int64{},
	}
	seen := map[string]bool{}

	entryRows, err := db.QueryContext(ctx,
		"SELECT id, type, status, thread, trust, recall_count, hot_score, "+
			"created_ts, updated_ts, supersedes, from_decision, summary, body FROM entries")
	if err != nil {
		return nil, err
	}
	for entryRows.Next() {
		var (
			id, typ, status, thread, summary, body string
			trust, hot                             float64
			recall                                 int64
			created, updated                       sql.NullString
			supersedes, fromDecision               sql.NullString
		)
		if err := entryRows.Scan(
			&id, &typ, &status, &thread, &trust, &recall, &hot,
			&created, &updated, &supersedes, &fromDecision, &summary, &body,
		); err != nil {
			entryRows.Close()
			return nil, err
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		if typ == "" {
			typ = "unknown"
		}
		if status == "" {
			status = "active"
		}
		tags := tagsOf[id]
		if tags == nil {
			tags = []string{}
		}
		graph.Nodes = append(graph.Nodes, &graphNode{
			ID:      id,
			Type:    typ,
			Status:  status,
			Thread:  thread,
			Tags:    tags,
			Summary: truncateRunes(summary, 120),
			Body:    truncateRunes(body, 4000),
			Trust:   trust,
			Recall:  recall,
			Hot:     hot,
			Created: created.String,
			Updated: updated.String,
		})
		if supersedes.String != "" {
			graph.Edges = append(graph.Edges, graphEdge{Source: id, Target: supersedes.String, Kind: "supersedes"})
		}
		if fromDecision.String != "" {
			graph.Edges = append(graph.Edges, graphEdge{Source: id, Target: fromDecision.String, Kind: "decision"})
		}
	}
	entryRows.Close()
	if err := entryRows.Err(); err != nil {
		return nil, err
	}

	// supersedes/decision targets can point at pruned entries; drop edges
	// whose endpoints have no node so the viewer never receives a dangling
	// link index.
	kept := graph.Edges[:0]
	for _, e := range graph.Edges {
		if seen[e.Source] && seen[e.Target] {
			kept = append(kept, e)
		}
	}
	graph.Edges = kept

	threads := map[string]int64{}
	tagCounts := map[string]int64{}
	tagOrder := []string{}
	for _, n := range graph.Nodes {
		if n.Thread != "" {
			threads[n.Thread]++
		}
		for _, t := range n.Tags {
			if _, ok := tagCounts[t]; !ok {
				tagOrder = append(tagOrder, t)
			}
			tagCounts[t]++
		}
	}

	// Tag hub nodes: the twelve most common tags, highest count first.
	sort.SliceStable(tagOrder, func(i, j int) bool {
		return tagCounts[tagOrder[i]] > tagCounts[tagOrder[j]]
	})
	for i, t := range tagOrder {
		if i >= maxTagHubs {
			break
		}
		tid := "tag:" + t
		if seen[tid] {
			continue
		}
		seen[tid] = true
		graph.Nodes = append(graph.Nodes, &graphNode{
			ID: tid, Type: "tag", Status: "group", Tags: []string{},
			Summary: t, Recall: tagCounts[t], Hot: float64(tagCounts[t]),
		})
	}

	// Thread hub nodes, attached once per distinct thread.
	for _, t := range threadOrder(graph.Nodes) {
		tid := "thread:" + t
		if seen[tid] {
			continue
		}
		seen[tid] = true
		graph.Nodes = append(graph.Nodes, &graphNode{
			ID: tid, Type: "thread", Status: "group", Thread: t,
			Tags: []string{}, Summary: t, Recall: threads[t], Hot: float64(threads[t]),
		})
	}

	for _, n := range graph.Nodes {
		if n.Type == "tag" || n.Type == "thread" {
			continue
		}
		if n.Thread != "" {
			graph.Edges = append(graph.Edges, graphEdge{Source: n.ID, Target: "thread:" + n.Thread, Kind: "thread"})
		}
		for i, t := range n.Tags {
			if i >= 3 {
				break
			}
			tid := "tag:" + t
			if seen[tid] {
				graph.Edges = append(graph.Edges, graphEdge{Source: n.ID, Target: tid, Kind: "tag"})
			}
		}
	}

	for _, n := range graph.Nodes {
		if n.Type == "thread" {
			continue
		}
		graph.Verdicts[n.Type]++
		if n.Created != "" {
			graph.Arrivals[truncateRunes(n.Created, 10)]++
		}
	}
	return graph, nil
}

// threadOrder returns the distinct threads in first-appearance order.
func threadOrder(nodes []*graphNode) []string {
	seen := map[string]bool{}
	var out []string
	for _, n := range nodes {
		if n.Thread != "" && !seen[n.Thread] {
			seen[n.Thread] = true
			out = append(out, n.Thread)
		}
	}
	return out
}

func truncateRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}
