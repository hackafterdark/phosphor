package tools

import (
	"context"
	"encoding/json"
	"testing"

	"charm.land/fantasy"
	"github.com/hackafterdark/phosphor/internal/memory"
	agenttools "github.com/hackafterdark/phosphor/pkg/agent/tools"
	"github.com/hackafterdark/phosphor/pkg/config"
	"github.com/stretchr/testify/require"
)

func tbool(v bool) *bool { return &v }

// runWriter executes the tool the way fantasy's executor does: a tool call with a
// JSON input string against a context carrying the session.
func runWriter(t *testing.T, ctx context.Context, tool fantasy.AgentTool, callID string, params Params) fantasy.ToolResponse {
	t.Helper()
	input, err := json.Marshal(params)
	require.NoError(t, err)
	resp, err := tool.Run(ctx, fantasy.ToolCall{ID: callID, Name: tool.Info().Name, Input: string(input)})
	require.NoError(t, err)
	return resp
}

// TestWriterDispatchesOpsAndPrintsTheFootprintBlock covers the handler's dispatch
// surface end to end: an empty op defaults to add, every write rides the gate, an
// unknown op is refused rather than silently dropped, the pin and retire families
// report their effect, a parked write surfaces as a proposal, and every
// successful write ends with the footprint block the agent tunes against.
func TestWriterDispatchesOpsAndPrintsTheFootprintBlock(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, memory.EnsureVault(dir))
	ctx := context.WithValue(context.Background(), agenttools.SessionIDContextKey, "tools-dispatch")

	auto := memory.DefaultPolicy()
	auto.AskMode = memory.AskModeAuto
	writer := NewMemoryTool(nil, nil, dir, true, auto)

	// The empty op defaults to add, and a committed write pays the footprint line.
	thread, summary := "tools-rollout", "The release train cuts on Fridays"
	wantID := memory.NewID(thread, summary)
	added := runWriter(t, ctx, writer, "bare", Params{
		Type: "fact", Thread: thread, Summary: summary,
		Body: "Deploys ride the Friday train.", Asserted: tbool(true),
	})
	require.False(t, added.IsError, added.Content)
	require.Contains(t, added.Content, "Injected window:", "the footprint block reports the standing byte cost")
	require.Contains(t, added.Content, "Corpus:", "the footprint block reports the corpus shape")

	// The pin family is a vouch and the retire family is a tombstone; both act by
	// id and both report what they did to that id.
	pinned := runWriter(t, ctx, writer, "pin", Params{Op: "pin", ID: wantID})
	require.False(t, pinned.IsError, pinned.Content)
	require.Contains(t, pinned.Content, "Pinned")
	unpinned := runWriter(t, ctx, writer, "unpin", Params{Op: "unpin", ID: wantID})
	require.False(t, unpinned.IsError, unpinned.Content)
	require.Contains(t, unpinned.Content, "Unpinned")
	retired := runWriter(t, ctx, writer, "retire", Params{Op: "retire", ID: wantID})
	require.False(t, retired.IsError, retired.Content)
	require.Contains(t, retired.Content, "Retired")

	// An op off the closed menu is refused loudly, never guessed toward.
	unknown := runWriter(t, ctx, writer, "bogus", Params{Op: "frobnicate", ID: wantID})
	require.True(t, unknown.IsError, "an unknown op has to fail loudly: %s", unknown.Content)
	require.Contains(t, unknown.Content, "Unknown memory op")

	// A non-primary context cannot write the shared vault; the refusal answers as
	// an error response so the model reads it as a stopped action.
	sub := NewMemoryTool(nil, nil, dir, false, auto)
	refused := runWriter(t, ctx, sub, "sub-add", Params{
		Type: "fact", Thread: "tools-subagent", Summary: "A subagent objective that must not stick",
		Body: "Subagent scratch work.", Asserted: tbool(true),
	})
	require.True(t, refused.IsError, "the non-primary writer has to refuse: %s", refused.Content)

	// Under the ask dial the same write is parked as a proposal rather than lost.
	ask := memory.DefaultPolicy()
	ask.AskMode = memory.AskModeAsk
	parked := runWriter(t, ctx, NewMemoryTool(nil, nil, dir, true, ask), "ask-add", Params{
		Type: "decision", Thread: "tools-rollout", Summary: "Cutover happens behind the flag",
		Body: "The flag gates the cutover.", Asserted: tbool(true),
	})
	require.False(t, parked.IsError, parked.Content)
	require.Contains(t, parked.Content, "Pending proposal id", "a parked write surfaces its proposal for later decision")
}

// TestEnabledResolvesTheTriStates covers the feature gate every tool surface and
// the system block consult before touching the vault.
func TestEnabledResolvesTheTriStates(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		cfg  *config.Config
		want bool
	}{
		{"nil config is on by default", nil, true},
		{"absent block follows the shipped on", &config.Config{}, true},
		{"explicit false turns it off", &config.Config{Memory: &config.Memory{Enabled: tbool(false)}}, false},
		{"explicit true stays on", &config.Config{Memory: &config.Memory{Enabled: tbool(true)}}, true},
		{"the off provider escape hatches out", &config.Config{Memory: &config.Memory{Provider: "off"}}, false},
		{"the none provider escape hatches out", &config.Config{Memory: &config.Memory{Provider: "none"}}, false},
		{"the escape outranks an explicit true", &config.Config{Memory: &config.Memory{Provider: "off", Enabled: tbool(true)}}, false},
		{"builtin is a real provider not an escape", &config.Config{Memory: &config.Memory{Provider: "builtin"}}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var store *config.ConfigStore
			if c.cfg != nil {
				store = config.NewTestStore(c.cfg)
			}
			require.Equal(t, c.want, Enabled(store))
		})
	}
}

// TestPolicyResolvesTheDialsFromConfig covers the phosphor.json-to-gate mapping:
// unset fields keep the shipped defaults, and every dial the config names has to
// reach the policy that decides the writes.
func TestPolicyResolvesTheDialsFromConfig(t *testing.T) {
	t.Parallel()
	require.Equal(t, memory.DefaultPolicy(), Policy(nil), "an unconfigured build runs the shipped policy")

	cfg := config.NewTestStore(&config.Config{Memory: &config.Memory{
		Ask:                   "auto",
		Adaptive:              tbool(false),
		MinSamples:            9,
		Confirm:               []string{"secrets"},
		Ignore:                []string{"preferences"},
		Untunable:             []string{"policy", "working-state"},
		RateLimitFloorPct:     20,
		AllowNonPrimaryWrites: tbool(true),
	}})
	p := Policy(cfg)
	require.Equal(t, memory.AskModeAuto, p.AskMode, "the one dial most people touch has to reach the gate")
	require.False(t, p.Adaptive)
	require.Equal(t, 9, p.MinSamples)
	require.Equal(t, []string{"secrets"}, p.Confirm)
	require.Equal(t, []string{"preferences"}, p.Ignore)
	require.Equal(t, []string{"policy", "working-state"}, p.Untunable)
	require.InDelta(t, 20, p.RateLimitFloorPct, 0.001)
	require.True(t, p.AllowNonPrimaryWrites, "the only loosening of the write fence is an explicit one")
}

// TestSettingsMapsTheAutoPromoteDials pins the config surface shipped for the
// §13 auto-promote decision: the tri-state rides through as a pointer so nil
// keeps the on-by-default posture, and zero-valued dials mean unset rather than
// off, which is the convention every other numeric memory setting follows.
func TestSettingsMapsTheAutoPromoteDials(t *testing.T) {
	t.Parallel()
	shipped := Settings(nil)
	require.True(t, shipped.AutoPromoteEnabled(), "the automatic path is index arithmetic and ships on")
	require.InDelta(t, 0.75, shipped.AutoPromoteMinTrust, 0.001,
		"the floor has to sit above the unreviewed 0.5 default and at or below the confirmed 0.8")
	require.Equal(t, 50, shipped.AutoPromoteSharePct)

	cfg := config.NewTestStore(&config.Config{Memory: &config.Memory{
		AutoPromote:         tbool(false),
		AutoPromoteMinTrust: 0.9,
		AutoPromoteSharePct: 30,
	}})
	s := Settings(cfg)
	require.NotNil(t, s.AutoPromote)
	require.False(t, s.AutoPromoteEnabled(), "false has to collapse Tier A to pins for hand-tuning")
	require.InDelta(t, 0.9, s.AutoPromoteMinTrust, 0.001)
	require.Equal(t, 30, s.AutoPromoteSharePct)
}
