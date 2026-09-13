// Package tools provides the phosphor_docs agent tool.
package tools

import (
	"context"
	_ "embed"
	"fmt"
	"strings"

	"charm.land/fantasy"
	"github.com/hackafterdark/phosphor/pkg/otel"
	"github.com/hackafterdark/phosphor/pkg/phosphordocs"
	"go.opentelemetry.io/otel/attribute"
)

//go:embed phosphor_docs.md
var phosphorDocsDescription string

const PhosphorDocsToolName = "phosphor_docs"

type PhosphorDocsParams struct {
	Query string `json:"query,omitempty" description:"Search terms to rank the most relevant docs (e.g. 'how do goals work')."`
	Path  string `json:"path,omitempty" description:"A specific doc to read, using a path returned by a search (e.g. 'commands/GOAL.md' or 'phosphor://docs/commands/GOAL.md')."`
	Limit int    `json:"limit,omitempty" description:"Max search results or read lines (default: search 10, read 200)."`
	List  bool   `json:"list,omitempty" description:"List every available doc instead of searching."`
}

func NewPhosphorDocsTool() fantasy.AgentTool {
	return fantasy.NewAgentTool(
		PhosphorDocsToolName,
		phosphorDocsDescription,
		func(ctx context.Context, params PhosphorDocsParams, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
			ctx, span := otel.StartSpan(ctx, "execute_tool phosphor_docs")
			defer span.End()
			span.SetAttributes(
				attribute.String("gen_ai.tool.name", PhosphorDocsToolName),
				attribute.String("gen_ai.tool.call.id", call.ID),
				attribute.String("gen_ai.tool.call.arguments", call.Input),
			)

			switch {
			case params.Path != "":
				return readDocsFile(ViewParams{FilePath: params.Path, Limit: params.Limit}), nil
			case params.List:
				return listDocsResponse(), nil
			case params.Query != "":
				return searchDocsResponse(ctx, params.Query, params.Limit)
			default:
				return usageResponse(), nil
			}
		},
	)
}

func searchDocsResponse(ctx context.Context, query string, limit int) (fantasy.ToolResponse, error) {
	results, err := phosphordocs.Search(ctx, query, limit)
	if err != nil {
		return fantasy.NewTextErrorResponse(fmt.Sprintf("Doc search failed: %v", err)), nil
	}

	var sb strings.Builder
	if len(results) == 0 {
		fmt.Fprintf(&sb, "No docs matched %q.\n\n", query)
		fmt.Fprintf(&sb, "Browse every available doc by calling this tool with list=true, or open %sREADME.md.\n",
			phosphordocs.DocsPrefix)
		fmt.Fprint(&sb, versionFooter())
		return fantasy.NewTextResponse(sb.String()), nil
	}

	fmt.Fprintf(&sb, "Matched %d doc(s) for %q. Read one by passing its path:\n\n", len(results), query)
	for i, r := range results {
		fmt.Fprintf(&sb, "%d. %s\n   path: %s\n   %s\n\n", i+1, r.Title, r.RelPath, r.Snippet)
	}
	fmt.Fprint(&sb, versionFooter())
	return fantasy.NewTextResponse(sb.String()), nil
}

func listDocsResponse() fantasy.ToolResponse {
	docs := phosphordocs.List()
	var sb strings.Builder
	fmt.Fprintf(&sb, "%d doc(s) available. Read one by passing its path:\n\n", len(docs))
	for _, d := range docs {
		fmt.Fprintf(&sb, "%s — %s\n", d.RelPath, d.Title)
	}
	fmt.Fprint(&sb, versionFooter())
	return fantasy.NewTextResponse(sb.String())
}

func usageResponse() fantasy.ToolResponse {
	var sb strings.Builder
	sb.WriteString("Provide one of: query=\"...\" to search, path=\"...\" to read a doc, or list=true to browse.\n")
	sb.WriteString("Use it to answer questions about how Phosphor itself works or is configured.\n\n")
	fmt.Fprint(&sb, versionFooter())
	return fantasy.NewTextResponse(sb.String())
}

func versionFooter() string {
	return fmt.Sprintf("\nDocs are compiled into this binary and match Phosphor build %s.", phosphordocs.Version)
}
