package agent

import (
	"fmt"
	"strings"
	"testing"

	"github.com/hackafterdark/phosphor/pkg/message"
	"github.com/stretchr/testify/require"
)

func userMsg(text string) message.Message {
	return message.Message{
		Role:  message.User,
		Parts: []message.ContentPart{message.TextContent{Text: text}},
	}
}

func toolCallMsg(id, name, input string) message.Message {
	return message.Message{
		Role: message.Assistant,
		Parts: []message.ContentPart{
			message.ToolCall{ID: id, Name: name, Input: input, Finished: true},
		},
	}
}

func toolResultMsg(id, name, content string) message.Message {
	return message.Message{
		Role: message.Tool,
		Parts: []message.ContentPart{
			message.ToolResult{ToolCallID: id, Name: name, Content: content},
		},
	}
}

func toolErrMsg(id, name, content string) message.Message {
	return message.Message{
		Role: message.Tool,
		Parts: []message.ContentPart{
			message.ToolResult{ToolCallID: id, Name: name, Content: content, IsError: true},
		},
	}
}

func firstResultContent(t *testing.T, m message.Message) string {
	t.Helper()
	for _, p := range m.Parts {
		if tr, ok := p.(message.ToolResult); ok {
			return tr.Content
		}
	}
	t.Fatalf("message has no tool result part")
	return ""
}

// bigView returns a view result well past any collapse threshold.
func bigView(seed string) string {
	return strings.Repeat(seed+" line of file content\n", 40)
}

// bigSearch returns a grep-style result past the collapse threshold.
func bigSearch() string {
	var b strings.Builder
	for i := range 60 {
		b.WriteString(fmt.Sprintf("internal/foo/bar%d.go:%d: matched line %d\n", i, i, i))
	}
	return b.String()
}

func TestMicrocompact_Disabled(t *testing.T) {
	msgs := []message.Message{
		userMsg("t1"),
		toolCallMsg("c1", "view", `{"file_path":"a.go"}`),
		toolResultMsg("c1", "view", bigView("old")),
		userMsg("t2"),
		toolCallMsg("c2", "view", `{"file_path":"a.go"}`),
		toolResultMsg("c2", "view", bigView("new")),
	}

	out, freed := microcompactToolOutputs(msgs, false, 0.5, 1, 5000, 10000)
	require.Equal(t, msgs[2].Parts, out[2].Parts)
	require.Zero(t, freed)
}

func TestMicrocompact_BelowThreshold(t *testing.T) {
	msgs := []message.Message{
		userMsg("t1"),
		toolCallMsg("c1", "view", `{"file_path":"a.go"}`),
		toolResultMsg("c1", "view", bigView("old")),
		userMsg("t2"),
		toolCallMsg("c2", "view", `{"file_path":"a.go"}`),
		toolResultMsg("c2", "view", bigView("new")),
	}

	// usage 1000 < 0.5 * 10000 → no elision even with duplicates.
	out, freed := microcompactToolOutputs(msgs, true, 0.5, 1, 1000, 10000)
	require.Equal(t, msgs[2].Parts, out[2].Parts)
	require.Zero(t, freed)
}

func TestMicrocompact_ViewDedupLatestWins(t *testing.T) {
	msgs := []message.Message{
		userMsg("t1"),
		toolCallMsg("c1", "view", `{"file_path":"a.go"}`),
		toolResultMsg("c1", "view", bigView("first")),
		userMsg("t2"),
		toolCallMsg("c2", "view", `{"file_path":"a.go"}`),
		toolResultMsg("c2", "view", bigView("second")),
		userMsg("t3"),
		toolCallMsg("c3", "view", `{"file_path":"a.go"}`),
		toolResultMsg("c3", "view", bigView("third")),
	}

	// Hot zone = last turn (index 6); cold zone = 0..5. The newest view
	// of a.go in the whole transcript is c3; c1's result is elided.
	// c2's assistant call (index 4) is in the cold zone but its result is
	// superseded too — latest-wins looks across the whole transcript.
	out, freed := microcompactToolOutputs(msgs, true, 0.5, 1, 5000, 10000)
	require.NotZero(t, freed)
	require.Equal(t, elidedViewMarker, firstResultContent(t, out[2]))
	require.Equal(t, elidedViewMarker, firstResultContent(t, out[5]))

	// Hot zone untouched.
	require.Equal(t, bigView("third"), firstResultContent(t, out[8]))

	// User prompts and assistant tool calls are never mutated.
	require.Equal(t, "t1", msgs[0].Content().Text)
	require.Equal(t, out[0], msgs[0])
	require.Equal(t, out[1], msgs[1])
	require.Equal(t, out[3], msgs[3])
	require.Equal(t, out[4], msgs[4])

	// Stored input slice is untouched (non-destructive).
	require.Equal(t, bigView("first"), firstResultContent(t, msgs[2]))
}

func TestMicrocompact_LatestViewInHotZoneStillElidesCold(t *testing.T) {
	msgs := []message.Message{
		userMsg("t1"),
		toolCallMsg("c1", "view", `{"file_path":"a.go"}`),
		toolResultMsg("c1", "view", bigView("old")),
		userMsg("t2"),
		toolCallMsg("c2", "view", `{"file_path":"a.go"}`),
		toolResultMsg("c2", "view", bigView("fresh")),
	}

	// Hot zone = last 2 turns → whole transcript is hot, nothing elided.
	out, freed := microcompactToolOutputs(msgs, true, 0.5, 2, 5000, 10000)
	require.Zero(t, freed)
	require.Equal(t, bigView("old"), firstResultContent(t, out[2]))

	// Hot zone = last turn → c1 elided, c2 (hot) untouched.
	out, freed = microcompactToolOutputs(msgs, true, 0.5, 1, 5000, 10000)
	require.NotZero(t, freed)
	require.Equal(t, elidedViewMarker, firstResultContent(t, out[2]))
	require.Equal(t, bigView("fresh"), firstResultContent(t, out[5]))
}

func TestMicrocompact_SearchCollapse(t *testing.T) {
	msgs := []message.Message{
		userMsg("t1"),
		toolCallMsg("c1", "grep", `{"pattern":"pkg/sandbox"}`),
		toolResultMsg("c1", "grep", bigSearch()),
		userMsg("t2"),
		toolCallMsg("c2", "grep", `{"pattern":"pkg/sandbox"}`),
		toolResultMsg("c2", "grep", bigSearch()),
	}

	out, freed := microcompactToolOutputs(msgs, true, 0.5, 1, 5000, 10000)
	require.NotZero(t, freed)

	marker := firstResultContent(t, out[2])
	require.True(t, strings.HasPrefix(marker, elidedSearchPrefix), marker)
	require.Contains(t, marker, `"pkg/sandbox"`)
	require.Contains(t, marker, "60 matches found")

	// Hot zone untouched.
	require.Equal(t, bigSearch(), firstResultContent(t, out[5]))
}

func TestMicrocompact_ShortSearchKept(t *testing.T) {
	msgs := []message.Message{
		userMsg("t1"),
		toolCallMsg("c1", "grep", `{"pattern":"tiny"}`),
		toolResultMsg("c1", "grep", "src/tiny.go:1: tiny\n"),
		userMsg("t2"),
		toolCallMsg("c2", "bash", `{"command":"true"}`),
		toolResultMsg("c2", "bash", "done"),
	}

	out, freed := microcompactToolOutputs(msgs, true, 0.5, 1, 5000, 10000)
	require.Zero(t, freed)
	require.Equal(t, "src/tiny.go:1: tiny\n", firstResultContent(t, out[2]))
	// Non-view, non-search tools are never touched.
	require.Equal(t, "done", firstResultContent(t, out[5]))
}

func TestMicrocompact_ErrorsAndDiagnosticsPreserved(t *testing.T) {
	bigErr := bigSearch()
	msgs := []message.Message{
		userMsg("t1"),
		toolCallMsg("c1", "bash", `{"command":"go test ./..."}`),
		toolErrMsg("c1", "bash", bigErr),
		userMsg("t2"),
		toolCallMsg("c2", "lsp_diagnostics", `{"file_path":"a.go"}`),
		toolResultMsg("c2", "lsp_diagnostics", bigErr),
		userMsg("t3"),
		toolCallMsg("c3", "edit", `{"file_path":"a.go"}`),
		toolResultMsg("c3", "edit", bigErr),
	}

	out, freed := microcompactToolOutputs(msgs, true, 0.5, 1, 5000, 10000)
	require.Zero(t, freed)
	// Error results are evidence — preserved even when huge.
	require.Equal(t, bigErr, firstResultContent(t, out[2]))
	// Diagnostics and edit results are outside both elision whitelists.
	require.Equal(t, bigErr, firstResultContent(t, out[5]))
	require.Equal(t, bigErr, firstResultContent(t, out[8]))
}

func TestMicrocompact_NoColdZone(t *testing.T) {
	msgs := []message.Message{
		userMsg("t1"),
		toolCallMsg("c1", "view", `{"file_path":"a.go"}`),
		toolResultMsg("c1", "view", bigView("x")),
		userMsg("t2"),
		toolCallMsg("c2", "view", `{"file_path":"a.go"}`),
		toolResultMsg("c2", "view", bigView("y")),
	}

	// Retention window covers the whole transcript.
	out, freed := microcompactToolOutputs(msgs, true, 0.5, 8, 5000, 10000)
	require.Zero(t, freed)
	require.Equal(t, msgs, out)
}

func TestMicrocompact_IdempotentAndMonotonic(t *testing.T) {
	msgs := []message.Message{
		userMsg("t1"),
		toolCallMsg("c1", "view", `{"file_path":"a.go"}`),
		toolResultMsg("c1", "view", bigView("old")),
		userMsg("t2"),
		toolCallMsg("c2", "grep", `{"pattern":"x"}`),
		toolResultMsg("c2", "grep", bigSearch()),
		userMsg("t3"),
		toolCallMsg("c3", "bash", `{"command":"true"}`),
		toolResultMsg("c3", "bash", "ok"),
	}

	out1, freed1 := microcompactToolOutputs(msgs, true, 0.5, 1, 5000, 10000)
	require.NotZero(t, freed1)

	// Re-running on the elided transcript frees nothing further and the
	// markers stay byte-stable, so prompt prefixes remain cacheable.
	out2, freed2 := microcompactToolOutputs(out1, true, 0.5, 1, 5000, 10000)
	require.Zero(t, freed2)
	require.Equal(t, out1[2].Parts, out2[2].Parts)
	require.Equal(t, out1[5].Parts, out2[5].Parts)
}

func TestMicrocompact_HotZoneStart(t *testing.T) {
	msgs := []message.Message{
		userMsg("t1"),
		userMsg("t2"),
		userMsg("t3"),
	}
	require.Equal(t, 2, hotZoneStart(msgs, 1))
	require.Equal(t, 1, hotZoneStart(msgs, 2))
	require.Equal(t, 0, hotZoneStart(msgs, 3))
	// Retention beyond the transcript → no cold zone.
	require.Equal(t, len(msgs), hotZoneStart(msgs, 4))
}

func TestMicrocompact_BashSearchDedup(t *testing.T) {
	big := bigSearch()
	msgs := []message.Message{
		userMsg("t1"),
		toolCallMsg("c1", "bash", `{"command":"rg pattern1"}`),
		toolResultMsg("c1", "bash", big),
		userMsg("t2"),
		toolCallMsg("c2", "bash", `{"command":"rg pattern2"}`),
		toolResultMsg("c2", "bash", big),
		userMsg("t3"),
		toolCallMsg("c3", "bash", `{"command":"rg pattern1"}`),
		toolResultMsg("c3", "bash", big),
	}

	// Hot zone = last turn. c1 duplicates c3 (same canonical command) and
	// is elided; c2 has no later duplicate and stays verbatim; c3 is hot.
	out, freed := microcompactToolOutputs(msgs, true, 0.5, 1, 5000, 10000)
	require.NotZero(t, freed)
	require.Equal(t, elidedSearchCmdMarker, firstResultContent(t, out[2]))
	require.Equal(t, big, firstResultContent(t, out[5]))
	require.Equal(t, big, firstResultContent(t, out[8]))
}

func TestMicrocompact_BashSearchDedupWhitespaceOnly(t *testing.T) {
	big := bigSearch()
	msgs := []message.Message{
		userMsg("t1"),
		toolCallMsg("c1", "bash", `{"command":"rg  -l  foo   src/"}`),
		toolResultMsg("c1", "bash", big),
		userMsg("t2"),
		toolCallMsg("c2", "bash", `{"command":"rg -l foo src/"}`),
		toolResultMsg("c2", "bash", big),
		userMsg("t3"),
		toolCallMsg("c3", "bash", `{"command":"true"}`),
		toolResultMsg("c3", "bash", "ok"),
	}

	// Whitespace variants canonicalize to the same command; c1 is elided.
	out, freed := microcompactToolOutputs(msgs, true, 0.5, 1, 5000, 10000)
	require.NotZero(t, freed)
	require.Equal(t, elidedSearchCmdMarker, firstResultContent(t, out[2]))
	require.Equal(t, big, firstResultContent(t, out[5]))
}

func TestMicrocompact_BashCompoundAndDynamicNeverElided(t *testing.T) {
	big := bigSearch()
	msgs := []message.Message{
		userMsg("t1"),
		toolCallMsg("c1", "bash", `{"command":"rg foo | head -5"}`),
		toolResultMsg("c1", "bash", big),
		userMsg("t2"),
		toolCallMsg("c2", "bash", `{"command":"rg foo | head -5"}`),
		toolResultMsg("c2", "bash", big),
		userMsg("t3"),
		toolCallMsg("c3", "bash", `{"command":"rg foo | head -5"}`),
		toolResultMsg("c3", "bash", big),
	}

	// Piped commands are compound: their output may carry evidence and is
	// never canonicalized, so identical runs all stay verbatim.
	out, freed := microcompactToolOutputs(msgs, true, 0.5, 1, 5000, 10000)
	require.Zero(t, freed)
	require.Equal(t, big, firstResultContent(t, out[2]))
	require.Equal(t, big, firstResultContent(t, out[5]))
}

func TestMicrocompact_BashErrorAndNonSearchPreserved(t *testing.T) {
	big := bigSearch()
	msgs := []message.Message{
		userMsg("t1"),
		toolCallMsg("c1", "bash", `{"command":"rg foo"}`),
		toolErrMsg("c1", "bash", big),
		userMsg("t2"),
		toolCallMsg("c2", "bash", `{"command":"go test ./..."}`),
		toolResultMsg("c2", "bash", big),
		userMsg("t3"),
		toolCallMsg("c3", "bash", `{"command":"rg foo"}`),
		toolResultMsg("c3", "bash", big),
	}

	// The earlier duplicate is an error result: evidence, never elided.
	// The go test output is not a search binary: verbatim.
	out, freed := microcompactToolOutputs(msgs, true, 0.5, 1, 5000, 10000)
	require.Zero(t, freed)
	require.Equal(t, big, firstResultContent(t, out[2]))
	require.Equal(t, big, firstResultContent(t, out[5]))
}

func TestCanonicalSearchCmd(t *testing.T) {
	cases := []struct {
		cmd  string
		want string
	}{
		{"rg foo src/", "rg foo src/"},
		{"rg  -l  foo   src/", "rg -l foo src/"},
		{"rg foo > /dev/null 2>&1", "rg foo"},
		{"go test ./...", "go test ./..."},
		{"cat server.log", "cat server.log"},
		{"rg foo | head -5", ""},
		{"rg foo && echo done", ""},
		{"rg $(cat pat)", ""},
		{"rm -rf src/", "rm -rf src/"},
		{"rg", ""},
		{"", ""},
	}
	for _, c := range cases {
		require.Equal(t, c.want, canonicalBashCmd(c.cmd), c.cmd)
	}
}

func TestMicrocompact_BashDynamicOutputNeverElided(t *testing.T) {
	first := "line1\nline2\n"
	second := "line1\nline2\npanic: runtime error\n"
	msgsWith := func(firstOut, secondOut string) []message.Message {
		return []message.Message{
			userMsg("t1"),
			toolCallMsg("c1", "bash", `{"command":"cat server.log"}`),
			toolResultMsg("c1", "bash", firstOut),
			userMsg("t2"),
			toolCallMsg("c2", "bash", `{"command":"cat server.log"}`),
			toolResultMsg("c2", "bash", secondOut),
			userMsg("t3"),
			toolCallMsg("c3", "bash", `{"command":"true"}`),
			toolResultMsg("c3", "bash", "ok"),
		}
	}

	// Dynamic log: the second cat adds new error lines, so both copies
	// carry distinct information and neither is elided.
	out, freed := microcompactToolOutputs(msgsWith(first, second), true, 0.5, 1, 5000, 10000)
	require.Zero(t, freed)
	require.Equal(t, first, firstResultContent(t, out[2]))
	require.Equal(t, second, firstResultContent(t, out[5]))

	// Byte-identical output: the repetition adds nothing, so the earlier
	// copy is elided and the newest stays verbatim. (freed may be ≤0 for
	// tiny outputs whose marker is longer than the content — the marker
	// is the behavioral assertion.)
	out, _ = microcompactToolOutputs(msgsWith(first, first), true, 0.5, 1, 5000, 10000)
	require.Equal(t, elidedSearchCmdMarker, firstResultContent(t, out[2]))
	require.Equal(t, first, firstResultContent(t, out[5]))
}

func TestMicrocompact_BashIdenticalTestOutputElided(t *testing.T) {
	testOut := "ok  \tgithub.com/hackafterdark/phosphor/pkg/agent\t0.158s\n"
	msgs := []message.Message{
		userMsg("t1"),
		toolCallMsg("c1", "bash", `{"command":"go test ./pkg/agent/..."}`),
		toolResultMsg("c1", "bash", testOut),
		userMsg("t2"),
		toolCallMsg("c2", "bash", `{"command":"go test ./pkg/agent/..."}`),
		toolResultMsg("c2", "bash", testOut),
		userMsg("t3"),
		toolCallMsg("c3", "bash", `{"command":"true"}`),
		toolResultMsg("c3", "bash", "ok"),
	}

	// A re-run with byte-identical output (e.g. cached test results) is
	// pure duplication even for a non-search binary.
	out, _ := microcompactToolOutputs(msgs, true, 0.5, 1, 5000, 10000)
	require.Equal(t, elidedSearchCmdMarker, firstResultContent(t, out[2]))
	require.Equal(t, testOut, firstResultContent(t, out[5]))
}
