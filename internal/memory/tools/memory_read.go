package tools

import (
	"context"
	_ "embed"
	"fmt"
	"strings"
	"text/template"

	"charm.land/fantasy"
	"github.com/hackafterdark/phosphor/internal/memory"
	"github.com/hackafterdark/phosphor/pkg/config"
	"github.com/hackafterdark/phosphor/pkg/otel"
	"go.opentelemetry.io/otel/attribute"
)

//go:embed memory_read.md
var memoryReadDescriptionTmpl []byte

// ReadToolName is the exported name of the progressive-disclosure reader.
const ReadToolName = memory.ReadToolName

// ReadParams is the argument set of the reader.
type ReadParams struct {
	IDs []string `json:"ids" jsonschema:"description=Entry ids to fetch the full body for (surfaced by memory_search); max ~20"`
}

// NewMemoryReadTool builds the progressive-disclosure reader: a search returns
// summaries plus snippets, and this returns the full, sanitized body on demand so
// the index→summary→body ladder never over-pays the context window.
func NewMemoryReadTool(cfg *config.ConfigStore, workingDir string) fantasy.AgentTool {
	return fantasy.NewAgentTool(
		ReadToolName,
		renderMemoryReadDescription(),
		func(ctx context.Context, params ReadParams, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
			ctx, span := otel.StartSpan(ctx, "execute_tool "+ReadToolName)
			defer span.End()
			span.SetAttributes(
				attribute.String("gen_ai.tool.name", ReadToolName),
				attribute.String("gen_ai.tool.call.id", call.ID),
				attribute.String("gen_ai.tool.call.arguments", call.Input),
			)

			ids := make([]string, 0, len(params.IDs))
			for _, id := range params.IDs {
				if id = strings.TrimSpace(id); id != "" {
					ids = append(ids, id)
				}
			}
			if len(ids) == 0 {
				return fantasy.NewTextErrorResponse("Provide at least one entry id to read."), nil
			}
			const maxIDs = 20
			if len(ids) > maxIDs {
				ids = ids[:maxIDs]
			}

			store, err := openStore(cfg, workingDir)
			if err != nil {
				return fantasy.NewTextErrorResponse(fmt.Sprintf("Failed to open the memory vault: %v", err)), nil
			}
			defer store.Close()

			if _, err := store.SyncIfStale(ctx); err != nil {
				return fantasy.NewTextErrorResponse(fmt.Sprintf("Failed to refresh the memory index: %v", err)), nil
			}

			entries, err := store.Get(ctx, ids...)
			if err != nil {
				return fantasy.NewTextErrorResponse(fmt.Sprintf("Memory read failed: %v", err)), nil
			}
			if len(entries) == 0 {
				return fantasy.NewTextResponse("No memories with those ids. They may have been retired or never recorded."), nil
			}

			var sb strings.Builder
			for _, e := range entries {
				badge := fmt.Sprintf("[%s · %s", e.Type, e.Status)
				if e.Thread != "" {
					badge += " · " + e.Thread
				}
				badge += fmt.Sprintf(" · trust %.2f]", e.Trust)
				fmt.Fprintf(&sb, "id=%s %s\n", e.ID, badge)
				if e.Summary != "" {
					fmt.Fprintf(&sb, "%s\n", memory.Sanitize(e.Summary))
				}
				// The body is the whole point of a read; it is sanitized on the way out
				// exactly as it was on the way in, since humans and sync write these files.
				if body := strings.TrimSpace(memory.Sanitize(e.Body)); body != "" {
					fmt.Fprintf(&sb, "%s\n", body)
				}
				if notes := strings.TrimSpace(memory.Sanitize(e.Notes)); notes != "" {
					fmt.Fprintf(&sb, "\nNotes:\n%s\n", notes)
				}
				if e.Supersedes != "" {
					fmt.Fprintf(&sb, "\nsupersedes: %s\n", e.Supersedes)
				}
				if e.FromDecision != "" {
					fmt.Fprintf(&sb, "from_decision: %s\n", e.FromDecision)
				}
				if e.Source != "" {
					fmt.Fprintf(&sb, "source: %s\n", e.Source)
				}
				sb.WriteString("\n")
			}
			return fantasy.NewTextResponse(sb.String()), nil
		},
	)
}

func renderMemoryReadDescription() string {
	tmpl, err := template.New("memory_read").Parse(string(memoryReadDescriptionTmpl))
	if err != nil {
		return "Fetch the full body of memories by id, after memory_search has surfaced their summaries."
	}
	var sb strings.Builder
	if err := tmpl.Execute(&sb, nil); err != nil {
		return string(memoryReadDescriptionTmpl)
	}
	return sb.String()
}
