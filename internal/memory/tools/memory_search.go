package tools

import (
	"context"
	_ "embed"
	"fmt"
	"strings"
	"text/template"

	"charm.land/fantasy"
	"github.com/hackafterdark/phosphor/internal/memory"
	memoryui "github.com/hackafterdark/phosphor/internal/memory/ui"
	"github.com/hackafterdark/phosphor/pkg/config"
	"github.com/hackafterdark/phosphor/pkg/otel"
	"go.opentelemetry.io/otel/attribute"
)

//go:embed memory_search.md
var memorySearchDescriptionTmpl []byte

// SearchToolName is the exported name of the retrieval tool.
const SearchToolName = memory.SearchToolName

// SearchParams is the argument set of the retrieval tool.
type SearchParams struct {
	Query  string   `json:"query,omitempty" jsonschema:"description=FTS5 keywords or identifiers to recall (BM25 ranked)"`
	Thread string   `json:"thread,omitempty" jsonschema:"description=Restrict recall to one named thread"`
	Type   string   `json:"type,omitempty" jsonschema:"description=Filter by entry type, comma-separated (decision,constraint,...)"`
	Tags   []string `json:"tags,omitempty" jsonschema:"description=Lateral recall: entries carrying these tags, deliberately NOT scoped by thread"`
	Status string   `json:"status,omitempty" jsonschema:"description=active | cold | retired | all (default active)"`
	Scope  string   `json:"scope,omitempty" jsonschema:"description=project | global | all (default all)"`
	Limit  int      `json:"limit,omitempty" jsonschema:"description=Max results (default 10, max 50)"`
}

// NewMemorySearchTool builds the retrieval tool. Reads are always allowed, including
// for non-primary agent contexts: a subagent may recall, it just may not write.
func NewMemorySearchTool(cfg *config.ConfigStore, workingDir string) fantasy.AgentTool {
	return fantasy.NewAgentTool(
		SearchToolName,
		renderMemorySearchDescription(),
		func(ctx context.Context, params SearchParams, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
			ctx, span := otel.StartSpan(ctx, "execute_tool "+SearchToolName)
			defer span.End()
			span.SetAttributes(
				attribute.String("gen_ai.tool.name", SearchToolName),
				attribute.String("gen_ai.tool.call.id", call.ID),
				attribute.String("gen_ai.tool.call.arguments", call.Input),
			)

			if strings.TrimSpace(params.Query) == "" && strings.TrimSpace(params.Thread) == "" && len(params.Tags) == 0 {
				return fantasy.NewTextErrorResponse("Provide a query, a thread, or at least one tag to search on."), nil
			}

			store, err := openStore(cfg, workingDir)
			if err != nil {
				return fantasy.NewTextErrorResponse(fmt.Sprintf("Failed to open the memory vault: %v", err)), nil
			}
			defer store.Close()

			if _, err := store.SyncIfStale(ctx); err != nil {
				return fantasy.NewTextErrorResponse(fmt.Sprintf("Failed to refresh the memory index: %v", err)), nil
			}

			q := memory.SearchQuery{
				Query:  params.Query,
				Thread: strings.TrimSpace(params.Thread),
				Status: normalizeStatus(params.Status),
				Scope:  normalizeScope(params.Scope),
				Tags:   params.Tags,
			}
			if types := parseTypes(params.Type); len(types) > 0 {
				q.Types = types
			}
			switch {
			case params.Limit <= 0:
				q.Limit = 10
			case params.Limit > 50:
				q.Limit = 50
			default:
				q.Limit = params.Limit
			}

			hits, err := store.Search(ctx, q)
			if err != nil {
				return fantasy.NewTextErrorResponse(fmt.Sprintf("Memory search failed: %v", err)), nil
			}
			if len(hits) == 0 {
				return fantasy.NewTextResponse(missNote()), nil
			}

			// The card is rendered through the memory UI package rather than formatted here so
			// that the bytes the model reads and the bytes the TUI later re-reads out of the
			// stored result are the same bytes from one author. Its ids are in it because the
			// closing line tells the reader to fetch by id, and a card that withholds the id
			// it just asked for is a dead end.
			return fantasy.NewTextResponse(memoryui.Card(len(hits), memoryui.FromHits(hits, memoryui.RoleRecalled))), nil
		},
	)
}

func normalizeStatus(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "active", "cold", "retired", "pending", "all", "merged", "":
		return strings.ToLower(strings.TrimSpace(s))
	default:
		return "active"
	}
}

func normalizeScope(s string) memory.Scope {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "project":
		return memory.ScopeProject
	case "global":
		return memory.ScopeGlobal
	default:
		return "" // empty searches both scopes.
	}
}

func parseTypes(csv string) []memory.Type {
	if strings.TrimSpace(csv) == "" {
		return nil
	}
	var out []memory.Type
	for _, part := range strings.Split(csv, ",") {
		if t := strings.TrimSpace(part); t != "" {
			out = append(out, memory.NormalType(t))
		}
	}
	return out
}

func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\r\n", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.TrimSpace(s)
	const limit = 280
	if len(s) > limit {
		return s[:limit] + "…"
	}
	return s
}

func plural(n int) string {
	if n == 1 {
		return "y"
	}
	return "ies"
}

// missNote keeps a zero-result recall an honest, bounded claim: a miss in the vault
// means "no distillate here", never "this is not true".
func missNote() string {
	return `No matching memories. That is "nothing recorded about this yet", not "this is not true" — the archive only holds what a prior turn chose to write. Continue and record the decision or fact when it lands.`
}

func renderMemorySearchDescription() string {
	tmpl, err := template.New("memory_search").Parse(string(memorySearchDescriptionTmpl))
	if err != nil {
		return "Recall decisions, constraints, requirements and references recorded in earlier sessions by keyword, thread, or tag."
	}
	var sb strings.Builder
	if err := tmpl.Execute(&sb, nil); err != nil {
		return string(memorySearchDescriptionTmpl)
	}
	return sb.String()
}
