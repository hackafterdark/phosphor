package agent

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	memorytools "github.com/hackafterdark/phosphor/internal/memory/tools"
	agenttools "github.com/hackafterdark/phosphor/pkg/agent/tools"
	"github.com/hackafterdark/phosphor/pkg/message"
)

// G5: deterministic microcompaction of stale tool outputs.
//
// The pass splits the transcript into a hot zone (the last
// microCompactRetentionTurns user turns, kept 100% untouched) and a cold
// zone (everything older). Cold-zone tool results are elided — never
// truncated, never reordered — according to fixed rules:
//
//   - File view deduplication (latest-wins): when the same file path was
//     viewed several times, only the most recent view survives intact;
//     earlier ones become "[Elided: superseded by later file view]".
//   - Stale search collapse: verbose navigation output (grep, glob, ls,
//     workspace/structural search, ...) is replaced by a one-line summary
//     naming the query and, where countable, the match count.
//   - Duplicate search-command deduplication (latest-wins): identical
//     pure search commands run through bash (rg, grep, find, ls, ...) are
//     elided down to their most recent occurrence.
//
// Evidence-bearing results — errors, diagnostics, edit/write results — are
// never elided: they are the verification chain the model reasons over.
//
// The pass operates on an outbound copy of the transcript only. Stored
// messages are never modified, and elisions are monotonic (a result once
// elided stays elided), so the prompt prefix only ever shrinks and provider
// prefix caches keep hitting.

const (
	// elidedViewMarker replaces a cold-zone file view that a later view of
	// the same path supersedes.
	elidedViewMarker = "[Elided: superseded by later file view]"
	// elidedSearchPrefix opens the one-line summary for a collapsed search
	// result. It doubles as the monotonicity marker: content starting with
	// it is already elided and is skipped on later passes.
	elidedSearchPrefix = "[Elided search output:"
	// elidedSearchCmdMarker replaces a cold-zone search command (run via
	// bash) whose output is superseded by an identical later call.
	elidedSearchCmdMarker = "[Elided: superseded by identical later search command]"
	// defaultSearchCollapseChars is the length above which a cold-zone
	// search result is collapsed into its summary line. Results at or
	// below this size stay verbatim — there is nothing to win.
	defaultSearchCollapseChars = 200
)

// viewTools are the tools whose output is the current content of a file
// path; a later view of the same path supersedes earlier ones.
var viewTools = map[string]bool{
	agenttools.ViewToolName:         true,
	agenttools.ViewNodeToolName:     true,
	agenttools.PhosphorDocsToolName: true,
}

// searchTools are navigation/discovery tools whose output is a list of
// findings that later turns supersede.
var searchTools = map[string]bool{
	agenttools.GrepToolName:             true,
	agenttools.GlobToolName:             true,
	agenttools.LSToolName:               true,
	agenttools.WorkspaceSearchToolName:  true,
	agenttools.StructuralSearchToolName: true,
	agenttools.SemanticSearchToolName:   true,
	agenttools.SourcegraphToolName:      true,
	agenttools.WebSearchToolName:        true,
	agenttools.ReferencesToolName:       true,
	agenttools.PhosphorDocsToolName:     true,
	memorytools.SearchToolName:          true,
}

// searchCmdBinaries are shell binaries whose canonicalization is accepted
// for duplicate elision on command text alone, because their output is
// navigation output re-derivable from the workspace at any time. The list
// is deliberately restricted to search/navigation binaries: file-reading
// binaries (cat, tail, head) are excluded because they imply full streaming
// of a possibly dynamic file — a second run can legitimately differ — and
// are only elided when the outputs are byte-identical (see
// canonicalBashCmd). Any other bash command keeps its output verbatim
// unless its repetition produced identical output, because its result may
// carry evidence (test output, build failures, side effects).
var searchCmdBinaries = map[string]bool{
	"rg":    true,
	"grep":  true,
	"egrep": true,
	"fgrep": true,
	"ag":    true,
	"ack":   true,
	"find":  true,
	"ls":    true,
	"tree":  true,
	"wc":    true,
	"sort":  true,
	"uniq":  true,
}

// microcompactToolOutputs elides stale cold-zone tool outputs from msgs.
// threshold is the fraction of contextWindow at which elision activates;
// usage is the estimated current context usage. retentionTurns is the hot
// zone size. It returns the (possibly rewritten) transcript and the number
// of estimated tokens freed. When the pass is disabled or the usage is
// below threshold, msgs is returned unchanged.
func microcompactToolOutputs(
	msgs []message.Message,
	enabled bool,
	threshold float64,
	retentionTurns int,
	usage int64,
	contextWindow int64,
) ([]message.Message, int64) {
	if !enabled || threshold <= 0 || contextWindow <= 0 || usage < int64(threshold*float64(contextWindow)) {
		return msgs, 0
	}
	if retentionTurns <= 0 {
		retentionTurns = 8
	}

	hotStart := hotZoneStart(msgs, retentionTurns)
	if hotStart >= len(msgs) {
		// The hot zone covers the whole transcript; nothing to elide.
		return msgs, 0
	}

	// Latest-wins map for file views: path -> index of the newest call.
	latestViewByPath := map[string]int{}
	// Latest-wins map for duplicate pure search commands run via bash:
	// canonical command -> index of the newest call.
	latestSearchCmd := map[string]int{}
	// Byte-identical duplicate map for any canonicalizable bash command:
	// canonical command -> newest identical result content.
	latestBashOutput := map[string]string{}
	for i, m := range msgs {
		if m.Role != message.Assistant {
			continue
		}
		for _, tc := range m.ToolCalls() {
			switch {
			case viewTools[tc.Name]:
				if path := toolInputPath(tc.Input); path != "" {
					latestViewByPath[path] = i
				}
			case tc.Name == agenttools.BashToolName:
				if cmd := canonicalBashCmd(toolInputCommand(tc.Input)); cmd != "" {
					latestSearchCmd[cmd] = i
					// Only overwrite when this call actually produced a
					// result, so the map ends up holding the newest
					// identical-call output without clobbering it with "".
					if trs := bashResultFor(msgs, i, tc.ID); trs != "" {
						latestBashOutput[cmd] = trs
					}
				}
			}
		}
	}

	out := make([]message.Message, len(msgs))
	copy(out, msgs)

	var elided, freed int64
	for i := range out {
		if i >= hotStart {
			break
		}
		m := out[i]
		if m.Role != message.Tool || len(m.Parts) == 0 {
			continue
		}
		changed := false
		parts := make([]message.ContentPart, len(m.Parts))
		copy(parts, m.Parts)
		for j, part := range parts {
			tr, ok := part.(message.ToolResult)
			if !ok || tr.IsError || tr.Content == "" {
				// Error results are evidence; never elide them.
				continue
			}
			if isElided(tr.Content) {
				// Monotonicity: already elided, keep the marker stable.
				continue
			}
			content := tr.Content
			switch {
			case viewTools[tr.Name]:
				path := toolInputPath(toolCallInputFor(msgs, i, tr.ToolCallID))
				if path == "" {
					continue
				}
				if latest, ok := latestViewByPath[path]; ok && latest > i {
					tr.Content = elidedViewMarker
					parts[j] = tr
					changed = true
					elided++
					freed += approxTokenCount(content) - approxTokenCount(elidedViewMarker)
				}
			case searchTools[tr.Name]:
				if len(content) <= defaultSearchCollapseChars {
					continue
				}
				tr.Content = elidedSearchSummary(tr, toolCallInputFor(msgs, i, tr.ToolCallID))
				parts[j] = tr
				changed = true
				elided++
				freed += approxTokenCount(content) - approxTokenCount(tr.Content)
			case tr.Name == agenttools.BashToolName:
				// Duplicate commands run through bash. Search binaries
				// (rg/grep/find/...) elide on identical canonical command
				// alone; every other canonicalizable command additionally
				// requires byte-identical output, so dynamic or evidence-
				// bearing results (cat of a growing log, go test) stay
				// verbatim unless the repetition truly adds nothing.
				cmd := canonicalBashCmd(toolInputCommand(toolCallInputFor(msgs, i, tr.ToolCallID)))
				if cmd == "" {
					continue
				}
				if latest, ok := latestSearchCmd[cmd]; ok && latest > i {
					if searchCmdBinaries[strings.Fields(cmd)[0]] || latestBashOutput[cmd] == content {
						tr.Content = elidedSearchCmdMarker
						parts[j] = tr
						changed = true
						elided++
						freed += approxTokenCount(content) - approxTokenCount(elidedSearchCmdMarker)
					}
				}
			}
		}
		if changed {
			m.Parts = parts
			out[i] = m
		}
	}

	if elided > 0 {
		slog.Info("Microcompaction elided stale tool outputs",
			"elided_results", elided,
			"freed_tokens", freed,
			"hot_zone_start", hotStart,
		)
	}
	return out, freed
}

// hotZoneStart returns the index of the first message in the hot zone: the
// user message that opened the retentionTurns-th most recent turn. Messages
// before it form the cold zone.
func hotZoneStart(msgs []message.Message, retentionTurns int) int {
	seen := 0
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role != message.User {
			continue
		}
		seen++
		if seen == retentionTurns {
			return i
		}
	}
	// Fewer turns than the retention window: no cold zone exists.
	return len(msgs)
}

// toolInputPath extracts the file path argument from a view tool call's
// JSON input.
func toolInputPath(input string) string {
	var args struct {
		FilePath string `json:"file_path"`
	}
	if err := json.Unmarshal([]byte(input), &args); err != nil {
		return ""
	}
	return args.FilePath
}

// toolInputQuery extracts the query argument from a search tool call's JSON
// input — "pattern" for grep/glob/structural search, "query" for the
// workspace/web search agenttools.
func toolInputQuery(input string) string {
	var args struct {
		Pattern string `json:"pattern"`
		Query   string `json:"query"`
	}
	if err := json.Unmarshal([]byte(input), &args); err != nil {
		return ""
	}
	if args.Pattern != "" {
		return args.Pattern
	}
	return args.Query
}

// toolCallInputFor finds the input of the tool call with the given ID among
// the assistant messages preceding (or directly above) tool-result message
// index i.
func toolCallInputFor(msgs []message.Message, i int, toolCallID string) string {
	for k := min(i, len(msgs)-1); k >= 0; k-- {
		if msgs[k].Role != message.Assistant {
			continue
		}
		for _, tc := range msgs[k].ToolCalls() {
			if tc.ID == toolCallID {
				return tc.Input
			}
		}
	}
	return ""
}

// isElided reports whether a tool result was already replaced by an elision
// marker, so repeated passes leave it untouched and prompt prefixes stay
// stable.
func isElided(content string) bool {
	return content == elidedViewMarker ||
		content == elidedSearchCmdMarker ||
		strings.HasPrefix(content, elidedSearchPrefix)
}

// toolInputCommand extracts the command argument from a bash tool call's
// JSON input.
func toolInputCommand(input string) string {
	var args struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal([]byte(input), &args); err != nil {
		return ""
	}
	return args.Command
}

// canonicalBashCmd returns the canonical form of a pure, side-effect-free
// command — a single read-only binary with no chaining, redirection, or
// expansion — or "" when the command is anything else. Canonicalizing
// whitespace makes variants that differ only in spacing or trailing
// redirects to /dev/null compare equal, so exact-duplicate commands can be
// deduplicated latest-wins. Compound commands are never canonicalized:
// their output may carry evidence (a chained build or test result) that a
// navigation summary cannot represent.
//
// Metacharacters are detected per shell word, not per substring: arguments
// like go's "./pkg/agent/..." contain characters that look like shell
// operators (`;` in `...`) but are harmless as arguments. Only operators
// standing alone (or attached to the reserved /dev/null redirection) are
// treated as compound/dynamic.
func canonicalBashCmd(cmd string) string {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return ""
	}
	cmd = strings.TrimSuffix(cmd, " > /dev/null 2>&1")
	cmd = strings.TrimSuffix(cmd, ">/dev/null 2>&1")
	for _, tok := range strings.Fields(cmd) {
		switch tok {
		case "|", "||", "&&", ";", ">", ">>", "<", "$(", "`":
			return ""
		}
		// A word that contains shell metacharacters (unquoted substitution,
		// redirection, or pipeline attached to an argument) is dynamic or
		// compound — reject unless it is the reserved /dev/null sink.
		if strings.ContainsAny(tok, "|&;<>$`") && tok != "/dev/null" {
			// A single quoted argument may legitimately contain these
			// characters, but detecting quoting reliably is out of scope;
			// err safe and keep the output verbatim.
			return ""
		}
	}
	fields := strings.Fields(cmd)
	if len(fields) < 2 {
		return ""
	}
	return strings.Join(fields, " ")
}

// bashResultFor returns the tool result content for the bash call with the
// given ID appearing at or before message index i, or "" when there is no
// successful result yet.
func bashResultFor(msgs []message.Message, i int, toolCallID string) string {
	for k := i + 1; k < len(msgs) && k <= i+2; k++ {
		m := msgs[k]
		if m.Role != message.Tool {
			continue
		}
		for _, p := range m.Parts {
			if tr, ok := p.(message.ToolResult); ok && tr.ToolCallID == toolCallID && !tr.IsError {
				return tr.Content
			}
		}
	}
	return ""
}

// elidedSearchSummary builds the one-line replacement for a collapsed
// search result, e.g.:
//
//	[Elided search output: "pkg/sandbox" — 12 matches found]
func elidedSearchSummary(tr message.ToolResult, callInput string) string {
	pattern := toolInputQuery(callInput)
	prefix := elidedSearchPrefix
	if pattern != "" {
		prefix += fmt.Sprintf(" %q", pattern)
	}
	matches := countSearchMatches(tr.Content)
	if matches >= 0 {
		return fmt.Sprintf("%s — %d matches found]", prefix, matches)
	}
	return prefix + " output elided]"
}

// countSearchMatches returns the number of result lines in common search
// tool outputs, or -1 when the format is not countable.
func countSearchMatches(content string) int {
	lines := strings.Split(content, "\n")
	count := 0
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		// Grep-style output carries "path:line:" prefixes; glob/ls-style
		// output carries one path per line. Neither heuristic is exact, but
		// the marker only needs to convey rough volume to the model.
		if strings.Contains(line, ":") || strings.HasPrefix(trimmed, "./") || filepathLike(trimmed) {
			count++
		}
	}
	if count == 0 {
		return -1
	}
	return count
}

// filepathLike reports whether s looks like a workspace-relative path.
func filepathLike(s string) bool {
	if s == "" || strings.ContainsAny(s, " \t") {
		return false
	}
	return strings.Contains(s, "/") || strings.Contains(s, "\\") || strings.HasSuffix(s, ":")
}
