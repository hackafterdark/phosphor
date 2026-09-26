package eval

import (
	"context"
	"encoding/json"
	"regexp"
	"strconv"
	"testing"

	"charm.land/fantasy"
	"github.com/hackafterdark/phosphor/internal/memory"
	memtools "github.com/hackafterdark/phosphor/internal/memory/tools"
	agenttools "github.com/hackafterdark/phosphor/pkg/agent/tools"
	"github.com/stretchr/testify/require"
)

// proposalIDLine parses the proposal id out of the writer's own reply, so the
// replay accepts a parked write through the number the agent is actually shown
// rather than through a private channel the production path never sees.
var proposalIDLine = regexp.MustCompile(`Pending proposal id:\s*(\d+)`)

// TestEvalNudgeEligibility is the truth table for the post-turn reminder. Each
// manifest case drives the exact call postturn.go makes, so what is measured here
// is the shipped decision seam and not a paraphrase of it: the reminder fires on
// every case curated as decision-shaped and stays silent on every case curated as
// not. Silence on the negative rows matters as much as firing on the positive ones,
// because a reminder that nags a fully cautious agent is the failure mode the
// design calls out as worse than the one it fixes.
func TestEvalNudgeEligibility(t *testing.T) {
	m := loadManifest(t)
	require.NotEmpty(t, m.Nudge, "the manifest declares no nudge cases")

	var fired, total int
	for _, c := range m.Nudge {
		note, ok := memory.ShouldNudge(memory.TurnInfo{
			SessionID:   "eval-eligibility",
			Text:        c.TurnText,
			Primary:     c.IsPrimary(),
			WroteMemory: c.WroteMemory,
			Suppressed:  memory.Suppressed(c.TurnText),
		})
		total++
		if ok {
			fired++
		}
		require.Equal(t, c.ExpectNudge, ok,
			"case %s: reminder %s but the manifest says otherwise (%s)",
			c.ID, map[bool]string{true: "fired", false: "stayed silent"}[ok], c.Note)
		if ok {
			// The note is the fixed line, not a variant: the agent-facing contract is
			// that the reminder names the tool and offers the one-clause out.
			require.Equal(t, memory.NudgeText, note, "case %s fired a different note than the shipped one", c.ID)
			require.Contains(t, note, memtools.ToolName, "the reminder has to name the tool it is asking for")
		}
	}
	t.Logf("nudge eligibility: fired %d of %d curated cases, expected %d",
		fired, total, countExpected(m.Nudge))

	// A non-primary turn must be silent even when everything else is eligible, and
	// the shape test has to prove it is the primary flag doing it, not an accident
	// of the text.
	primary := false
	silent, ok := memory.ShouldNudge(memory.TurnInfo{
		SessionID: "eval-eligibility",
		Text:      "We decided to keep the corpus deterministic and to rule out embeddings.",
		Primary:   primary,
	})
	require.False(t, ok, "a subagent turn may not be reminded about a write it cannot perform")
	require.Empty(t, silent)
}

// TestEvalNudgeToWriteFunnel is section 11's primary health metric run as the
// scripted replay the spec names: a decision-shaped turn ends, the reminder
// fires, and the agent reaches for the writer through the real tool, the real
// gate, and the real store.
//
// What this genuinely measures is the plumbing end to end: the nudge counts
// itself, every write that reaches the vault passes the gate, a write the gate
// parks is accept-able by number and lands, a turn that answers "transient"
// leaves nothing behind, and the published funnel counters agree with the writes
// that actually committed. The compliance of a live model is not knowable
// deterministically and is not faked here; the replay is scripted to comply, and
// the bar it has to clear is the shipped 0.7, so a regression anywhere along the
// chain drops the measured conversion under it.
func TestEvalNudgeToWriteFunnel(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, memory.EnsureVault(dir))

	ctx := context.WithValue(context.Background(), agenttools.SessionIDContextKey, "eval-funnel")
	writer := memtools.NewMemoryTool(nil, nil, dir, true, memory.DefaultPolicy())
	reader, err := memory.Open(memory.OpenOptions{WorkspaceDir: dir, Settings: memory.DefaultSettings(), Shared: true})
	require.NoError(t, err)
	t.Cleanup(func() { _ = reader.Close() })

	memory.ResetMetrics()

	// The suppression counter is probed first, before the funnel turns, so its
	// count is attributable to the one probe and nothing else.
	_, ok := memory.ShouldNudge(memory.TurnInfo{
		SessionID:  "eval-funnel",
		Text:       "We decided the scratch pad name is transient. //ignore-memory-this-turn",
		Primary:    true,
		Suppressed: true,
	})
	require.False(t, ok, "the opt-out token has to silence the reminder")
	require.Equal(t, int64(1), memory.Metrics().Suppressed, "a suppressed turn is counted as suppressed")

	var reached, committedDirect, writesLanded, declined int
	for _, s := range funnelTurns() {
		note, fired := memory.ShouldNudge(memory.TurnInfo{
			SessionID: "eval-funnel",
			Text:      s.turn,
			Primary:   true,
		})
		require.True(t, fired, "funnel turn %s was curated decision-shaped but the gate did not find it so", s.id)
		require.Equal(t, memory.NudgeText, note, "funnel turn %s", s.id)

		before := report(t, reader, ctx)

		if s.declines {
			// The reminder offers a one-clause out instead of forcing a write; this is
			// the turn that takes it, and it must leave the vault untouched.
			declined++
			after := report(t, reader, ctx)
			require.Equal(t, before.Active, after.Active, "declined turn %s still changed the vault", s.id)
			continue
		}
		reached++

		resp := callWriter(t, writer, ctx, "eval-"+s.id, memtools.Params{
			Op:       "add",
			Type:     s.kind,
			Summary:  s.summary,
			Body:     s.body,
			Thread:   s.thread,
			Tags:     []string{s.thread},
			Asserted: ptr(true),
			Scope:    "project",
		})
		require.False(t, resp.IsError, "funnel turn %s: the writer refused: %s", s.id, resp.Content)

		if groups := proposalIDLine.FindStringSubmatch(resp.Content); len(groups) > 1 {
			// The gate parked it; the replay supplies the human decision by accepting
			// the proposal the writer named, which is the only route a pending write has
			// to the vault.
			gate := memory.NewGate(reader, nil, memory.DefaultPolicy())
			out, err := gate.Resolve(ctx, parseInt(t, groups[1]), true)
			require.NoError(t, err, "funnel turn %s", s.id)
			require.Equal(t, memory.StatusCommitted, out.Status,
				"funnel turn %s: accepting proposal %s did not commit it", s.id, groups[1])
		} else {
			committedDirect++
		}

		got, err := reader.Get(ctx, s.wantID)
		require.NoError(t, err, "funnel turn %s", s.id)
		require.Len(t, got, 1, "funnel turn %s never reached the vault", s.id)
		require.Equal(t, memory.StatusActive, got[0].Status,
			"funnel turn %s landed but not as active content: %q", s.id, got[0].Status)
		require.Equal(t, s.thread, got[0].Thread, "funnel turn %s landed in the wrong thread", s.id)
		writesLanded++

		after := report(t, reader, ctx)
		require.Equal(t, before.Active+1, after.Active, "funnel turn %s wrote more or less than one row", s.id)
	}

	// The end-to-end conversion: of the eligible nudged turns, the fraction that
	// ended with the decision actually in the vault. This is the section 11 number.
	conversion := float64(writesLanded) / float64(reached+declined)
	t.Logf("funnel: %d eligible turns, %d reached for the writer, %d committed directly, %d parked and accepted, %d declined",
		reached+declined, reached, committedDirect, reached-committedDirect, declined)
	t.Logf("nudge to write conversion %.3f (bar %.2f)", conversion, BarNudgeConversion)
	require.GreaterOrEqual(t, conversion, BarNudgeConversion, "the feature's primary health metric")

	// And the shipped counters, checked for arithmetic rather than for size: they
	// are what the live product will publish, so they must agree exactly with the
	// writes this replay observed, not merely point the same direction.
	m := memory.Metrics()
	require.Equal(t, int64(reached+declined), m.EligibleTurns, "every eligible turn must be counted once")
	require.Equal(t, int64(reached+declined), m.NudgedTurns, "every eligible turn was nudged")
	require.Equal(t, int64(committedDirect), m.WritesAfter,
		"the write counter tracks committed writes through the tool and nothing else")
	require.InDelta(t, m.Conversion, float64(committedDirect)/float64(reached+declined), 1e-9,
		"the published conversion is the counter ratio")

	// A write the gate refused must not be counted as a write. The non-primary tool
	// is the refusal on the table; the counters must not move.
	sub := memtools.NewMemoryTool(nil, nil, dir, false, memory.DefaultPolicy())
	resp := callWriter(t, sub, ctx, "eval-refused", memtools.Params{
		Op: "add", Type: "fact", Summary: "A borrowed context must not reach the vault",
		Body: "Proves a refused write is refused all the way down to the funnel counters.", Thread: "eval-funnel-refused",
		Asserted: ptr(true), Scope: "project",
	})
	require.True(t, resp.IsError, "the non-primary writer must answer as an error")
	require.Equal(t, int64(committedDirect), memory.Metrics().WritesAfter,
		"a refused write inflated the funnel counter")
}

// funnelTurn is one scripted agent turn: the text that ends it, the write the
// agent reaches for when it complies, and the entry id that write has to land
// under. Declined turns take the reminder's one-clause out instead.
type funnelTurn struct {
	id       string
	turn     string
	declines bool
	kind     string
	summary  string
	body     string
	thread   string
	wantID   string
}

// funnelTurns is the scripted replay. Every turn text matches the shipped
// decision-pattern trigger by construction, the bodies share little vocabulary so
// the classifier reads them as distinct subjects rather than as refinements of
// each other, and each entry lives in its own thread for the same reason.
func funnelTurns() []funnelTurn {
	// The compliance rows are every field a complying turn needs; the one declined
	// turn is appended below, since it names no write at all.
	rows := []struct {
		id, thread, kind, turn, summary, body string
	}{
		{"funnel-runner-dedup", "eval-funnel-runner", "decision",
			"We decided to keep the runner deduplicating identical commands before it fans out.",
			"Deduplicate identical hook commands before fanout",
			"The runner collapses repeated commands per event so one script cannot fire twice for the same invocation."},
		{"funnel-no-embeddings", "eval-funnel-retrieval", "decision",
			"We are going with BM25 over the FTS index and we ruled out embeddings entirely.",
			"Retrieval stays lexical with no embedding model",
			"Ranking is BM25 with tag and trust boosts, so the feature carries no model weight and no vector store."},
		{"funnel-dial-default", "eval-funnel-dial", "decision",
			"The plan is to ship the balanced dial as the default ask posture.",
			"Balanced is the shipped ask posture",
			"Only consequential writes interrupt; reversible archive adds commit without asking."},
		{"funnel-inferred-floor", "eval-funnel-lifecycle", "constraint",
			"The hard rule is that an inferred statement may never enter the hot window unconfirmed.",
			"Inferred content is capped out of the injected window",
			"An unconfirmed inference lands as a pending draft and has to be confirmed before promotion."},
		{"funnel-token-ceiling", "eval-funnel-budget", "constraint",
			"Remember that the always-injected block carries a standing token ceiling near four hundred tokens.",
			"The injected window has a token ceiling",
			"The window is priced against the provider prefix cache, so its size is a per-session tax when it drifts."},
		{"funnel-docs-artifact", "eval-funnel-docs", "fact",
			"Note that the embedded help corpus is a generated build artifact copied from the docs tree.",
			"The embedded corpus is generated, never hand-edited",
			"The generator removes and recopies from the canonical docs directory, and a drift guard fails the suite on any mismatch."},
		{"funnel-path-fold", "eval-funnel-paths", "fact",
			"We settled on comparing folded forward-slash forms so a backslash cannot dodge the write fence.",
			"Path comparisons fold separators before the fence check",
			"Both sides of every containment test are normalised to the same slash form first."},
		{"funnel-skill-load", "eval-funnel-skills", "reference",
			"Keep in mind that the skill manifest is read before the agent acts on a matching task.",
			"Skill instructions load before task work",
			"The description only triggers the read; the procedure itself lives in the manifest beside the skill."},
		{"funnel-vault-guard", "eval-funnel-vault", "preference",
			"The rule for the editor tools is that they consult the vault guard before any write lands.",
			"Editor writes pass the vault guard first",
			"The shared vault is writable by exactly one path, so the guard checks every edit, write and append against it."},
	}

	out := make([]funnelTurn, 0, len(rows)+1)
	for _, r := range rows {
		out = append(out, funnelTurn{
			id:      r.id,
			turn:    r.turn,
			thread:  r.thread,
			kind:    r.kind,
			summary: r.summary,
			body:    r.body,
			wantID:  memory.NewID(r.thread, r.summary),
		})
	}
	// The declined turn rides last, which is where an agent that answers "it is
	// transient" sits in a replay: it is the answer to the reminder, not a write.
	return append(out, funnelTurn{
		id:       "funnel-transient",
		thread:   "eval-funnel-transient",
		turn:     "We decided the scratch pad name is transient and not worth persisting.",
		declines: true,
	})
}

// callWriter executes the real writer tool the way fantasy"s executor does: a tool
// call with a JSON input string, run against a context carrying the session.
func callWriter(t *testing.T, tool fantasy.AgentTool, ctx context.Context, callID string, params memtools.Params) fantasy.ToolResponse {
	t.Helper()
	input, err := json.Marshal(params)
	require.NoError(t, err)
	resp, err := tool.Run(ctx, fantasy.ToolCall{
		ID:    callID,
		Name:  tool.Info().Name,
		Input: string(input),
	})
	require.NoError(t, err)
	return resp
}

func report(t *testing.T, s *memory.Store, ctx context.Context) memory.Stats {
	t.Helper()
	_, err := s.SyncIfStale(ctx)
	require.NoError(t, err)
	stats, err := s.Report(ctx)
	require.NoError(t, err)
	return stats
}

func countExpected(cases []NudgeCase) int {
	var n int
	for _, c := range cases {
		if c.ExpectNudge {
			n++
		}
	}
	return n
}

func parseInt(t *testing.T, s string) int64 {
	t.Helper()
	v, err := strconv.ParseInt(s, 10, 64)
	require.NoError(t, err, "proposal id %q is not a number", s)
	return v
}
