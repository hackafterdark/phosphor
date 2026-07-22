// Package tools provides the semantic_search agent tool.
package tools

import (
	"context"
	"fmt"
	"strings"

	"charm.land/fantasy"
	"github.com/hackafterdark/phosphor/internal/embeddings"
	"github.com/hackafterdark/phosphor/pkg/config"
	"github.com/hackafterdark/phosphor/pkg/otel"
	"go.opentelemetry.io/otel/attribute"
	_ "modernc.org/sqlite/vec"
)

const SemanticSearchToolName = "semantic_search"

type SemanticSearchParams struct {
	Query string `json:"query" description:"Natural language query to search the codebase semantically"`
	Count int    `json:"count,omitempty" description:"Number of results to return (default: 5, max: 20)"`
}

func NewSemanticSearchTool(cfg config.Config, workingDir string) fantasy.AgentTool {
	return fantasy.NewAgentTool(
		SemanticSearchToolName,
		semanticSearchDescription(),
		func(ctx context.Context, params SemanticSearchParams, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
			ctx, span := otel.StartSpan(ctx, "execute_tool semantic_search")
			defer span.End()
			span.SetAttributes(
				attribute.String("gen_ai.tool.name", SemanticSearchToolName),
				attribute.String("gen_ai.tool.call.id", call.ID),
				attribute.String("gen_ai.tool.call.arguments", call.Input),
			)

			if params.Query == "" {
				return fantasy.NewTextErrorResponse("Query is required"), nil
			}

			if params.Count <= 0 {
				params.Count = 5
			} else if params.Count > 20 {
				params.Count = 20
			}

			idx := cfg.CodebaseIndex
if !idx.Enabled && !idx.AutoUpdate {
			return fantasy.NewTextErrorResponse("Codebase indexing is not enabled"), nil
		}

			modelCfg, ok := cfg.Models[config.SelectedModelTypeEmbedding]
			if !ok {
				return fantasy.NewTextErrorResponse("No embedding model configured"), nil
			}

			providerCfg := cfg.GetProviderForModel(config.SelectedModelTypeEmbedding)
			if providerCfg == nil {
				return fantasy.NewTextErrorResponse("No provider found for embedding model"), nil
			}

			client := embeddings.NewEmbeddingClient(
				strings.TrimRight(providerCfg.BaseURL, "/"),
				modelCfg.Model,
				providerCfg.APIKey,
			)
			store, err := embeddings.NewStore(workingDir)
			if err != nil {
				return fantasy.NewTextErrorResponse(fmt.Sprintf("Failed to open index: %v", err)), nil
			}
			defer store.Close()

			vector, err := client.Embed(params.Query)
			if err != nil {
				return fantasy.NewTextErrorResponse(fmt.Sprintf("Failed to embed query: %v", err)), nil
			}

			results, err := store.Search(ctx, vector, params.Count)
			if err != nil {
				return fantasy.NewTextErrorResponse(fmt.Sprintf("Search failed: %v", err)), nil
			}

			if len(results) == 0 {
				return fantasy.NewTextResponse("No matching chunks found."), nil
			}

			var sb strings.Builder
			fmt.Fprintf(&sb, "Found %d matching chunk(s):\n\n", len(results))
			for i, r := range results {
				content := truncate(r.Content, 200)
				fmt.Fprintf(&sb, "%d. %s\n   Distance: %.4f\n   Content: %s\n\n",
					i+1, r.FilePath, r.Distance, content)
			}

			return fantasy.NewTextResponse(sb.String()), nil
		},
	)
}

func semanticSearchDescription() string {
	return `Searches the codebase index for chunks semantically similar to a natural language query. The codebase must already be indexed.`
}

func truncate(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "..."
}