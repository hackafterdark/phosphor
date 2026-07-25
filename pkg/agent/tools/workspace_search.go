// Package tools provides the workspace_search agent tool.
package tools

import (
	"context"
	"fmt"
	"strings"

	"charm.land/fantasy"
	"github.com/hackafterdark/phosphor/internal/workspaceindex"
	"github.com/hackafterdark/phosphor/pkg/otel"
	"go.opentelemetry.io/otel/attribute"
)

const WorkspaceSearchToolName = "workspace_search"

type WorkspaceSearchParams struct {
	Query string `json:"query" description:"Search query (keywords or identifier)"`
	Table string `json:"table" description:"Which table to search: 'symbols', 'docs', or 'all' (default: 'all')"`
	Limit int   `json:"limit,omitempty" description:"Max results (default: 10, max: 50)"`
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

			if len(results) == 0 {
				return fantasy.NewTextResponse("No matching results found."), nil
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

			return fantasy.NewTextResponse(sb.String()), nil
		},
	)
}

func workspaceSearchDescription() string {
	return `Searches the workspace FTS5 index for code symbols and document text. Instant, zero API calls.`
}