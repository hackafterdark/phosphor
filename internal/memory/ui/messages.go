package ui

import (
	"github.com/hackafterdark/phosphor/internal/memory"
	"github.com/hackafterdark/phosphor/pkg/message"
)

// The three agent tools whose stored results carry provenance. Read results are
// included because a read discloses exactly which entry the reader leaned on, which
// is the same fact the pill counts.
var toolNames = map[string]bool{
	memory.ToolName:       true,
	memory.SearchToolName: true,
	memory.ReadToolName:   true,
}

// IsMemoryTool reports whether a stored tool result came from the memory family.
func IsMemoryTool(name string) bool { return toolNames[name] }

// SourcesFromResults collects the memories a single turn touched by re-reading the
// cards out of that turn"s stored tool results. The results are the record, not a
// live list kept somewhere else: what the pill shows is the bytes the model was shown,
// so the two cannot drift, and a session re-opened after a restart still reports what
// actually happened in it.
func SourcesFromResults(results []message.ToolResult) []Source {
	var sources []Source
	seen := map[string]bool{}
	for _, res := range results {
		if res.IsError || !IsMemoryTool(res.Name) {
			continue
		}
		parsed, _, ok := Parse(res.Content)
		if !ok {
			continue
		}
		for _, s := range parsed {
			if s.ID == "" || seen[s.ID] {
				continue
			}
			seen[s.ID] = true
			sources = append(sources, s)
		}
	}
	return sources
}

// SourcesFromMessages is SourcesFromResults across a whole session, first-sighting
// order preserved so the list reads as the session"s timeline.
func SourcesFromMessages(msgs []message.Message) []Source {
	var all []message.ToolResult
	for _, msg := range msgs {
		all = append(all, msg.ToolResults()...)
	}
	return SourcesFromResults(all)
}

// TurnFootprint is the injected-window cost a turn's writes reported, taken from the
// last memory write in the message list because that is the budget the next write in
// the same turn would have to fit inside of.
func TurnFootprint(msgs []message.Message) (Footprint, bool) {
	var (
		best Footprint
		got  bool
	)
	for _, msg := range msgs {
		for _, res := range msg.ToolResults() {
			if res.IsError || res.Name != memory.ToolName {
				continue
			}
			if f, ok := ParseFootprint(res.Content); ok {
				best, got = f, true
			}
		}
	}
	return best, got
}
