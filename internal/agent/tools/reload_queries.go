package tools

import (
	"context"
	"fmt"

	"charm.land/fantasy"
	"github.com/hackafterdark/phosphor/internal/agent/parser"
	"github.com/hackafterdark/phosphor/internal/otel"
	"go.opentelemetry.io/otel/attribute"
)

const ReloadQueriesToolName = "reload_queries"

const reloadQueriesDescription = "Reload custom query capabilities from the workspace .phosphor/queries directory."

// ReloadQueriesParams defines the parameters for the reload_queries tool.
type ReloadQueriesParams struct{}

// NewReloadQueriesTool creates a new reload queries tool.
func NewReloadQueriesTool(workingDir string) fantasy.AgentTool {
	return fantasy.NewAgentTool(
		ReloadQueriesToolName,
		reloadQueriesDescription,
		func(ctx context.Context, params ReloadQueriesParams, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
			ctx, span := otel.StartSpan(ctx, "execute_tool reload_queries")
			defer span.End()
			span.SetAttributes(
				attribute.String("gen_ai.tool.name", ReloadQueriesToolName),
				attribute.String("gen_ai.tool.call.id", call.ID),
				attribute.String("gen_ai.tool.call.arguments", call.Input),
			)

			if err := parser.ReloadQueries(workingDir); err != nil {
				return fantasy.NewTextErrorResponse(fmt.Sprintf("Failed to reload queries: %s", err)), nil
			}

			caps := parser.GetCapabilities()
			return fantasy.NewTextResponse(fmt.Sprintf("Successfully reloaded queries. Total capabilities loaded: %d.", len(caps))), nil
		},
	)
}
