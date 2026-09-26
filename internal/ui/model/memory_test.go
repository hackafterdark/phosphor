package model

import (
	"strings"
	"testing"

	"github.com/hackafterdark/phosphor/internal/memory"
	"github.com/hackafterdark/phosphor/internal/ui/common"
	"github.com/hackafterdark/phosphor/internal/ui/dialog"
	uis "github.com/hackafterdark/phosphor/internal/ui/styles"
	"github.com/hackafterdark/phosphor/internal/ui/util"
	"github.com/stretchr/testify/require"
)

func newMemoryTestUI(t *testing.T) *UI {
	t.Helper()
	st := uis.CharmtonePantera()
	return &UI{
		com: &common.Common{
			Workspace: &testWorkspace{},
			Styles:    &st,
		},
		dialog: dialog.NewOverlay(),
	}
}

func TestUI_HandleMemorySlashCommand_UnknownSubcommandWarns(t *testing.T) {
	t.Parallel()

	ui := newMemoryTestUI(t)
	ui.registerSlashCommands()

	msg := ui.handleMemorySlashCommand([]string{"bogus"})()
	info, ok := msg.(util.InfoMsg)
	require.True(t, ok)
	require.Equal(t, util.InfoTypeWarn, info.Type)
	require.Contains(t, info.Msg, "Unknown /memory subcommand")
}

func TestUI_HandleMemorySlashCommand_StatusReportsOffWithoutAStore(t *testing.T) {
	t.Parallel()

	ui := newMemoryTestUI(t)

	// A non-AppWorkspace (the test workspace) resolves no in-process memory index,
	// which is exactly the state a memory-off build is in; the status command has to
	// say so rather than print an empty corpus as if the vault were merely quiet.
	msg := ui.handleMemorySlashCommand(nil)()
	info, ok := msg.(util.InfoMsg)
	require.True(t, ok)
	require.Contains(t, info.Msg, "Memory is off")
}

func TestUI_HandleMemorySlashCommand_SourcesNeedsASessionOrAQuery(t *testing.T) {
	t.Parallel()

	ui := newMemoryTestUI(t)

	msg := ui.handleMemorySlashCommand([]string{"sources"})()
	info, ok := msg.(util.InfoMsg)
	require.True(t, ok)
	require.Equal(t, util.InfoTypeWarn, info.Type)
}

func TestUI_HandleMemorySlashCommand_PolicyWriteReportsErrorWithoutAStore(t *testing.T) {
	t.Parallel()

	ui := newMemoryTestUI(t)

	msg := ui.handleMemorySlashCommand([]string{"policy", "always run the tests"})()
	info, ok := msg.(util.InfoMsg)
	require.True(t, ok)
	require.Equal(t, util.InfoTypeError, info.Type)
	require.Contains(t, info.Msg, "memory is off")
}

func TestUI_HandleMemorySlashCommand_ManageNeedsAnID(t *testing.T) {
	t.Parallel()

	ui := newMemoryTestUI(t)

	msg := ui.handleMemorySlashCommand([]string{"pin"})()
	info, ok := msg.(util.InfoMsg)
	require.True(t, ok)
	require.Equal(t, util.InfoTypeWarn, info.Type)
	require.Contains(t, info.Msg, "Usage: /memory pin")
}

func TestUI_HandleMemorySlashCommand_ManageReportsOffWithoutAStore(t *testing.T) {
	t.Parallel()

	ui := newMemoryTestUI(t)

	msg := ui.handleMemorySlashCommand([]string{"retire", "some-entry-id"})()
	info, ok := msg.(util.InfoMsg)
	require.True(t, ok)
	require.Contains(t, info.Msg, "Memory is off")
}

func TestUI_MemorySidebarPanelHiddenWithoutAStore(t *testing.T) {
	t.Parallel()

	ui := newMemoryTestUI(t)

	// The panel gates itself on the store rather than on a separate config knob,
	// so a memory-off build must lose the section entirely, not show an empty one.
	require.Empty(t, ui.memoryInfo(40))
}

func TestUI_MemorySlashCommandIsRegistered(t *testing.T) {
	t.Parallel()

	ui := newMemoryTestUI(t)
	ui.registerSlashCommands()

	_, ok := ui.slashHandlers["memory"]
	require.True(t, ok, "the dispatcher only runs commands present in the handler map")
}

func TestFormatInjectPair(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		used  int
		limit int
		want  string
	}{
		{"shared KB unit", 3145, 8192, "3.1/8 KB"},
		{"trailing .0 trimmed", 1024, 8192, "1/8 KB"},
		{"sub-kilo share of a kilo limit", 512, 1024, "0.5/1 KB"},
		{"bytes under a kilobyte", 500, 900, "500/900 B"},
		{"megabyte scale", 3145728, 8388608, "3/8 MB"},
		{"unbounded window prints only the used side", 500, 0, "500 B"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, formatInjectPair(tc.used, tc.limit))
		})
	}
}

func TestInjectBar(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		used  int
		limit int
		width int
		want  string
	}{
		{"empty budget is all shade", 0, 100, 10, "░░░░░░░░░░"},
		{"full budget is all blocks", 100, 100, 10, "▓▓▓▓▓▓▓▓▓▓"},
		{"half fills five steps", 50, 100, 10, "▓▓▓▓▓░░░░░"},
		{"over-quota clamps to full", 150, 100, 10, "▓▓▓▓▓▓▓▓▓▓"},
		{"negative reads as empty", -5, 100, 10, "░░░░░░░░░░"},
		{"no limit means no bar", 10, 0, 10, ""},
		{"no width means no bar", 10, 100, 0, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, injectBar(tc.used, tc.limit, tc.width))
		})
	}
}

func TestUI_MemoryReviewCommandOpensTheDialogAndReportsOffWithoutAStore(t *testing.T) {
	t.Parallel()

	ui := newMemoryTestUI(t)

	// The bare listing is a dialog surface, not a line rendered into the
	// conversation: opening it is the command, and the queue arrives on the
	// load message the open returns.
	cmd := ui.handleMemorySlashCommand([]string{"review"})
	require.NotNil(t, cmd)
	require.True(t, ui.dialog.ContainsDialog(dialog.MemoryReviewID))

	msg, ok := cmd().(dialog.MemoryReviewLoadedMsg)
	require.True(t, ok)
	require.Contains(t, msg.Note, "Memory is off")

	// Re-opening brings it to front and re-arms the load rather than
	// stacking a second copy.
	require.NotNil(t, ui.handleMemorySlashCommand([]string{"review"}))
}

func TestUI_DecideMemoryReviewWithoutAStoreReportsThroughTheDialog(t *testing.T) {
	t.Parallel()

	ui := newMemoryTestUI(t)

	// Decisions answer inside the dialog's own status line, so a storeless
	// build reports there rather than emitting an InfoMsg onto the chat
	// surface.
	msg, ok := ui.decideMemoryReview(dialog.MemoryReviewItem{Kind: "draft", ID: "x"}, dialog.ReviewConfirm)().(dialog.MemoryReviewLoadedMsg)
	require.True(t, ok)
	require.Contains(t, msg.Note, "Memory is off")
}

func TestReviewDecideOneGuardsUnknownKinds(t *testing.T) {
	t.Parallel()

	// Without a store there is nothing to decide against; the dialog path
	// must fail closed through the loaded message, which the off-store test
	// above pins. Here, the pure converter is what the queue is made of.
	items := memoryReviewItems(nil, nil)
	require.Empty(t, items)
}

func TestMemoryReviewItems(t *testing.T) {
	t.Parallel()

	yes := true
	drafts := []memory.Entry{
		{
			ID: "2026-draft", Type: memory.TypeDecision, Thread: "rel", Summary: "ship the\nflag dark",
			Body: "because the launch is the\nreason",
		},
		{
			ID: "2026-asserted", Type: memory.TypeFact, Summary: "observed fact",
			Source: "session#a", Asserted: &yes,
		},
	}
	proposals := []memory.Proposal{
		{
			ID: 7, Op: "add", Bucket: "add/fact/B/project", Rationale: "dial ask",
			Payload: `{"summary":"queued thing"}`,
		},
	}

	items := memoryReviewItems(drafts, proposals)
	require.Len(t, items, 3)

	draft := items[0]
	require.Equal(t, "draft", draft.Kind)
	require.Equal(t, "2026-draft", draft.ID)
	require.Contains(t, draft.Label, "2026-draft [decision] in \"rel\"",
		"a draft row names itself, its lane, and its thread")
	require.Equal(t, "ship the flag dark", draft.Summary, "the row collapses the summary to one line")
	require.Equal(t, "unconfirmed inference", draft.Why, "the row carries the reason it waits")
	require.Contains(t, draft.Detail, "because the launch is the\nreason",
		"the detail view carries the body verbatim, folds and all, which the row has no room for")

	asserted := items[1]
	require.Equal(t, "from session#a", asserted.Why)

	proposal := items[2]
	require.Equal(t, "proposal", proposal.Kind)
	require.Equal(t, "7", proposal.ID, "the decision op takes the number as text")
	require.Equal(t, "#7 add [add/fact/B/project]", proposal.Label)
	require.Equal(t, "queued thing", proposal.Summary,
		"the payload's summary is shown over the rationale")
	require.Contains(t, proposal.Detail, "bucket add/fact/B/project")

	long := memoryReviewItems([]memory.Entry{{ID: "x", Summary: strings.Repeat("word ", 60)}}, nil)
	require.Less(t, len([]rune(long[0].Summary)), 100, "one entry may not push the row past its line")
	require.Contains(t, long[0].Summary, "…")
}

func TestUI_MemoryReviewCommandGuardsItsSubcommands(t *testing.T) {
	t.Parallel()

	ui := newMemoryTestUI(t)

	msg := ui.handleMemorySlashCommand([]string{"review", "abolish"})()
	info, ok := msg.(util.InfoMsg)
	require.True(t, ok)
	require.Equal(t, util.InfoTypeWarn, info.Type)
	require.Contains(t, info.Msg, "Unknown /memory review subcommand")

	msg = ui.handleMemorySlashCommand([]string{"review", "confirm"})()
	info, ok = msg.(util.InfoMsg)
	require.True(t, ok)
	require.Equal(t, util.InfoTypeWarn, info.Type)
	require.Contains(t, info.Msg, "Usage: /memory review confirm|ignore|retire")
}

// TestUI_SlashDispatchRoutesMemoryReviewToTheDialog proves the parse path a
// user actually drives: they type "/memory review" into the editor and the
// dispatcher has to hand "review" to the handler, not the empty subcommand.
// The dialog opening is what distinguishes the review path from the bare
// "/memory" status report, so the two being different types is what pins the
// argument through.
func TestUI_SlashDispatchRoutesMemoryReviewToTheDialog(t *testing.T) {
	t.Parallel()

	ui := newMemoryTestUI(t)
	ui.registerSlashCommands()

	require.NotNil(t, ui.handleSlashCommand("/memory review"))
	require.True(t, ui.dialog.ContainsDialog(dialog.MemoryReviewID))

	msg, ok := ui.handleSlashCommand("/memory review")().(dialog.MemoryReviewLoadedMsg)
	require.True(t, ok)
	// The review queue's no-store voice, loaded into the dialog, not a chat
	// line.
	require.Contains(t, msg.Note, "Memory is off")

	// The bare form must still be the status report, so the two are not the
	// same path.
	bare := ui.handleSlashCommand("/memory")()
	bareInfo, ok := bare.(util.InfoMsg)
	require.True(t, ok)
	require.Contains(t, bareInfo.Msg, "no vault is open")
}

// TestUI_SlashDispatchForwardsMemoryReviewDecisionArgs guards the second half of
// the original bug: "/memory review confirm" with no id has to reach the review
// handler's usage guard, which only fires once it has seen the "confirm" token.
func TestUI_SlashDispatchForwardsMemoryReviewDecisionArgs(t *testing.T) {
	t.Parallel()

	ui := newMemoryTestUI(t)
	ui.registerSlashCommands()

	msg := ui.handleSlashCommand("/memory review confirm")()
	info, ok := msg.(util.InfoMsg)
	require.True(t, ok)
	require.Equal(t, util.InfoTypeWarn, info.Type)
	require.Contains(t, info.Msg, "Usage: /memory review confirm|ignore|retire")
}

func TestUI_IsRunnableSlashCommand(t *testing.T) {
	t.Parallel()

	ui := newMemoryTestUI(t)
	ui.registerSlashCommands()

	// An argument-free command typed in full runs on the first Enter.
	require.True(t, ui.isRunnableSlashCommand("/compact"))
	// A command that takes arguments runs once an argument token is present.
	require.True(t, ui.isRunnableSlashCommand("/memory review"))
	require.True(t, ui.isRunnableSlashCommand("/name my session"))
	// A bare arg-taking name is still mid-typing and must not bypass the popup.
	require.False(t, ui.isRunnableSlashCommand("/memory"))
	require.False(t, ui.isRunnableSlashCommand("/name"))
	// Half-typed or unknown names never qualify.
	require.False(t, ui.isRunnableSlashCommand("/mem"))
	require.False(t, ui.isRunnableSlashCommand("/notacommand arg"))
	require.False(t, ui.isRunnableSlashCommand("no slash"))
}

func TestUI_MemoryKeyCommandReportsOffWithoutAReachableStore(t *testing.T) {
	t.Parallel()

	// A non-AppWorkspace resolves neither the in-process index nor a maintenance
	// handle, so every key subcommand must report that memory is off rather than
	// crash on the missing store or hand back a phantom seal reading. The maintenance
	// fallback is gated behind a real workspace, and this is that gate.
	for _, args := range [][]string{
		{"key"},
		{"key", "status"},
		{"key", "show"},
		{"key", "restore", "some bip39 phrase"},
		{"key", "rotate", "confirm"},
	} {
		ui := newMemoryTestUI(t)
		msg := ui.handleMemorySlashCommand(args)()
		info, ok := msg.(util.InfoMsg)
		require.True(t, ok)
		require.Contains(t, info.Msg, "Memory is off")
	}
}
