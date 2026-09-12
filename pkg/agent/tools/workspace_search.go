// Package tools provides the workspace_search agent tool.
package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	"charm.land/fantasy"
	"github.com/hackafterdark/phosphor/internal/workspaceindex"
	"github.com/hackafterdark/phosphor/pkg/otel"
	"go.opentelemetry.io/otel/attribute"
)

const WorkspaceSearchToolName = "workspace_search"

type WorkspaceSearchParams struct {
	Query string `json:"query" description:"Search query (keywords or identifier)"`
	Table string `json:"table" description:"Which table to search: 'symbols', 'docs', or 'all' (default: 'all')"`
	Limit int    `json:"limit,omitempty" description:"Max results (default: 10, max: 50)"`
}

func NewWorkspaceSearchTool(workingDir string) fantasy.AgentTool {
	return fantasy.NewAgentTool(
		WorkspaceSearchToolName,
		workspaceSearchDescription(),
		func(ctx context.Context, params WorkspaceSearchParams, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
			ctx, span := otel.StartSpan(ctx, "execute_tool workspace_search")
			defer span.End()
			span.SetAttributes(
				attribute.String("gen_ai.tool.name", WorkspaceSearchToolName),
				attribute.String("gen_ai.tool.call.id", call.ID),
				attribute.String("gen_ai.tool.call.arguments", call.Input),
			)

			if params.Query == "" {
				return fantasy.NewTextErrorResponse("query is required"), nil
			}

			if params.Limit <= 0 {
				params.Limit = 10
			} else if params.Limit > 50 {
				params.Limit = 50
			}

			store, err := workspaceindex.NewStore(workingDir)
			if err != nil {
				return fantasy.NewTextErrorResponse(fmt.Sprintf("Failed to open index: %v", err)), nil
			}
			defer store.Close()

			var results []workspaceindex.SearchResult
			table := strings.ToLower(params.Table)
			switch table {
			case "symbols":
				results, err = store.SearchSymbols(ctx, params.Query, params.Limit)
			case "docs":
				results, err = store.SearchDocs(ctx, params.Query, params.Limit)
			default:
				results, err = store.SearchAll(ctx, params.Query, params.Limit)
			}
			if err != nil {
				return fantasy.NewTextErrorResponse(fmt.Sprintf("Search failed: %v", err)), nil
			}

			progress, _ := store.GetProgress(ctx)

			if len(results) == 0 {
				msg := "No matching results found."
				if note := coverageMissNote(progress); note != "" {
					msg += "\n\n" + note
				}
				return fantasy.NewTextResponse(msg), nil
			}

			var sb strings.Builder
			fmt.Fprintf(&sb, "Found %d result(s):\n\n", len(results))
			for i, r := range results {
				if r.Name != "" {
					fmt.Fprintf(&sb, "%d. %s -> %s\n   %s\n\n",
						i+1, r.Path, r.Name, r.Signature)
				} else {
					content := r.Content
					if len(content) > 200 {
						content = content[:200] + "..."
					}
					fmt.Fprintf(&sb, "%d. %s\n   %s\n\n",
						i+1, r.Path, content)
				}
			}
			if footer := coverageFooter(progress); footer != "" {
				fmt.Fprintf(&sb, "\n%s\n", footer)
			}

			return fantasy.NewTextResponse(sb.String()), nil
		},
	)
}

func workspaceSearchDescription() string {
	return `Searches the workspace FTS5 index for code symbols and document text. Instant, zero API calls.`
}

// coverageFooter reports how much of the codebase is actually in the index,
// so a caller can judge how far to trust a hit.
func coverageFooter(p *workspaceindex.IndexProgress) string {
	if p == nil {
		return ``
	}
	rel := `never`
	if !p.LastBuilt.IsZero() {
		rel = humanizeDuration(time.Since(p.LastBuilt))
	}
	return fmt.Sprintf(`[index coverage: %d/%d files · status %s · last full build %s ago]`,
		p.FilesIndexed, p.TotalFiles, p.Status, rel)
}

// coverageMissNote turns a zero-result search into an honest, bounded claim:
// when coverage is incomplete, `not found` must not be read as `absent`.
func coverageMissNote(p *workspaceindex.IndexProgress) string {
	if p == nil || p.FilesIndexed == 0 {
		return `Index is empty or not enabled; this is not evidence the symbol is absent. Use lsp_references or grep.`
	}
	if p.Stale || p.FilesIndexed < p.TotalFiles {
		return fmt.Sprintf(
			`Treat this as inconclusive: the index covers only %d/%d files (status %s, last full build %v). A miss here does not mean the symbol is absent — fall back to lsp_references or grep, or trigger a rebuild from the Workspace Index menu.`,
			p.FilesIndexed, p.TotalFiles, p.Status, humanizeDuration(durationSince(p.LastBuilt)),
		)
	}
	return ``
}

func durationSince(t time.Time) time.Duration {
	if t.IsZero() {
		return time.Duration(0)
	}
	return time.Since(t)
}

func humanizeDuration(d time.Duration) string {
	switch {
	case d <= 0:
		return `never`
	case d < time.Minute:
		return `moments`
	case d < time.Hour:
		return fmt.Sprintf(`%dm`, int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf(`%dh`, int(d.Hours()))
	default:
		return fmt.Sprintf(`%dd`, int(d.Hours()/24))
	}
}
