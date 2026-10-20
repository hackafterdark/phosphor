package server

import (
	"context"
	"database/sql"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"time"
	"unicode/utf8"

	"github.com/hackafterdark/phosphor/internal/home"
	"github.com/hackafterdark/phosphor/internal/memory"
	"github.com/labstack/echo/v5"
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

// HandleMemoryGraphView serves the embedded memory graph viewer page.
func HandleMemoryGraphView(ctx *echo.Context) error {
	return ctx.HTML(http.StatusOK, memoryGraphHTML)
}

// HandleMemoryGraphData builds the graph for the current memory vault on
// every request, so the viewer's polling sees fresh entries.
func HandleMemoryGraphData(ctx *echo.Context) error {
	dbPath := memoryGraphDBPath()
	if dbPath == "" {
		return ctx.String(http.StatusNotFound, "No memory vault found")
	}
	graph, err := buildMemoryGraph(ctx.Request().Context(), dbPath)
	if err != nil {
		return ctx.String(http.StatusInternalServerError, "Failed to build memory graph: "+err.Error())
	}
	return ctx.JSON(http.StatusOK, graph)
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
