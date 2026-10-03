package tools

import (
	"context"
	_ "embed"
	"fmt"
	"html/template"
	"strings"

	"charm.land/fantasy"
	"github.com/hackafterdark/phosphor/pkg/otel"
	"go.opentelemetry.io/otel/attribute"
)

const PhosphorTraceToolName = "phosphor_trace"

//go:embed phosphor_trace.md.tpl
var phosphorTraceDescriptionTmpl []byte

var phosphorTraceDescriptionTpl = template.Must(
	template.New("phosphorTraceDescription").
		Parse(string(phosphorTraceDescriptionTmpl)),
)

type phosphorTraceDescriptionData struct {
	DefaultLines int
	MaxLines     int
}

func phosphorTraceDescription() string {
	out := renderTemplate(phosphorTraceDescriptionTpl, phosphorTraceDescriptionData{
		DefaultLines: defaultTraceLines,
		MaxLines:     maxTraceLines,
	})
	// go:embed keeps the template's on-disk line endings, which vary with the
	// platform checkout; pin them to LF so the value is byte-stable.
	return strings.ReplaceAll(out, "\r\n", "\n")
}

// Default and max span limits for the trace tool output.
const (
	defaultTraceLines = 40
	maxTraceLines     = 100
)

// Preferred attribute keys, in render order, for the compact key=value
// suffix on each span line. Only keys actually present are shown.
var traceAttrOrder = []string{
	"gen_ai.operation.name",
	"gen_ai.provider.name",
	"gen_ai.request.model",
	"gen_ai.step.index",
	"gen_ai.tool.name",
	"gen_ai.response.finish_reason",
	"gen_ai.usage.input_tokens",
	"gen_ai.usage.output_tokens",
	"gen_ai.error.message",
}

type PhosphorTraceParams struct {
	SessionID string `json:"session_id,omitempty" description:"Filter to spans of one session (gen_ai.conversation.id); empty returns the most recent spans of any trace"`
	Lines     int    `json:"lines,omitempty" description:"Maximum number of span lines to return (default 40, max 100)"`
}

func NewPhosphorTraceTool() fantasy.AgentTool {
	return fantasy.NewAgentTool(
		PhosphorTraceToolName,
		phosphorTraceDescription(),
		func(ctx context.Context, params PhosphorTraceParams, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
			ctx, span := otel.StartSpan(ctx, "execute_tool phosphor_trace")
			defer span.End()
			span.SetAttributes(
				attribute.String("gen_ai.tool.name", PhosphorTraceToolName),
				attribute.String("gen_ai.tool.call.id", call.ID),
				attribute.String("gen_ai.tool.call.arguments", call.Input),
			)
			result := runPhosphorTrace(params)
			return fantasy.NewTextResponse(result), nil
		},
	)
}

// runPhosphorTrace reads the in-process span ring and renders it.
func runPhosphorTrace(params PhosphorTraceParams) string {
	summaries := otel.GetSpanSummaries(params.SessionID, params.Lines)
	if len(summaries) == 0 {
		return "No spans buffered. The in-memory span ring is off unless observability.memory_buffer is set to a positive size in phosphor.json."
	}
	return formatTrace(summaries)
}

// formatTrace renders span summaries as an indented tree, one line per span:
// name (duration) [status] [key attrs]. Completion order is children-first,
// so the lines are rebuilt as a DFS from the roots (spans whose parent is not
// in the set) to get a stable parent-before-child ordering.
func formatTrace(summaries []otel.SpanSummary) string {
	byID := make(map[string]int, len(summaries))
	for i, s := range summaries {
		if s.SpanID != "" {
			byID[s.SpanID] = i
		}
	}
	children := make(map[int][]int, len(summaries))
	visited := make([]bool, len(summaries))
	var roots []int
	for i, s := range summaries {
		if p, ok := byID[s.ParentID]; s.ParentID != "" && ok {
			children[p] = append(children[p], i)
		} else {
			roots = append(roots, i)
		}
	}

	var b strings.Builder
	var walk func(i, depth int)
	walk = func(i, depth int) {
		if visited[i] {
			return
		}
		visited[i] = true
		s := summaries[i]
		writeSpanLine(&b, s, depth)
		for _, c := range children[i] {
			walk(c, depth+1)
		}
	}
	for _, r := range roots {
		walk(r, 0)
	}
	// Spans in a cycle or otherwise unreachable from the roots: emit flat.
	for i := range summaries {
		if !visited[i] {
			writeSpanLine(&b, summaries[i], 0)
		}
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// writeSpanLine emits a single "name (duration) [status] [key attrs]" line at
// the given nesting depth.
func writeSpanLine(b *strings.Builder, s otel.SpanSummary, depth int) {
	fmt.Fprintf(b, "%s%s (%.1fms)", strings.Repeat("  ", depth), s.Name, s.DurationMs)
	if s.Status != "" {
		fmt.Fprintf(b, " status=%s", s.Status)
	}
	if attrs := formatTraceAttrs(s.Attributes); attrs != "" {
		fmt.Fprintf(b, " %s", attrs)
	}
	b.WriteString("\n")
}

// formatTraceAttrs renders the preferred attribute keys as k=v pairs.
func formatTraceAttrs(attrs map[string]string) string {
	if len(attrs) == 0 {
		return ""
	}
	parts := make([]string, 0, len(traceAttrOrder))
	for _, k := range traceAttrOrder {
		if v, ok := attrs[k]; ok && v != "" {
			parts = append(parts, k+"="+v)
		}
	}
	return strings.Join(parts, " ")
}
