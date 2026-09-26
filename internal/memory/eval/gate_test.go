package eval

import (
	"context"
	"testing"

	"github.com/hackafterdark/phosphor/internal/memory"
	"github.com/stretchr/testify/require"
)

// TestEvalGatePrecision is the anti-nurture gate: of the asks the write path surfaces,
// the bar is how many a reader would call warranted. An ask that is not warranted is
// the interrupt the design promises not to generate, and it is the reason the dial
// exists at all.
//
// The gate is driven as it runs in production, through the real classify and decide
// path over the real store, with no permission service so an ask falls to the
// proposal queue rather than to a prompt. That keeps the measurement deterministic and
// keeps it honest about the thing it is actually asserting: the decision, not the UI.
func TestEvalGatePrecision(t *testing.T) {
	m := loadManifest(t)
	require.NotEmpty(t, m.Gate, "the manifest declares no gate cases")

	var asks, warranted, missed int
	for _, c := range m.Gate {
		s, _ := newCorpusStore(t, corpusSettings())
		g := memory.NewGate(s, nil, memory.DefaultPolicy())

		out, err := g.Add(context.Background(), memory.WriteRequest{
			SessionID: "eval-gate",
			Primary:   true,
			Entry: memory.Entry{
				Type:     memory.NormalType(c.Type),
				Thread:   "eval-gate",
				Summary:  "Gate case " + c.ID,
				Body:     "A curated write used only to measure what the gate decides about it.",
				Pinned:   c.Pinned,
				Asserted: ptr(c.Asserted),
				Scope:    memory.ScopeGlobal,
			},
		})
		require.NoError(t, err, "case %s", c.ID)

		asked := out.Status == memory.StatusPendingWrite
		switch {
		case asked && c.ExpectAsk:
			asks++
			warranted++
		case asked && !c.ExpectAsk:
			asks++
			t.Errorf("case %s: the gate asked about a write that does not warrant an ask (%s)", c.ID, out.Rationale)
		case !asked && c.ExpectAsk:
			missed++
			t.Errorf("case %s: the gate auto-committed a write that should have been proposed (%s)", c.ID, out.Rationale)
		default:
			require.Equal(t, memory.StatusCommitted, out.Status, "case %s", c.ID)
		}
		t.Logf("%-20s type=%-10s asserted=%-5v pinned=%-5v -> status=%-8s expected_ask=%v",
			c.ID, c.Type, c.Asserted, c.Pinned, out.Status, c.ExpectAsk)
	}

	if asks == 0 {
		t.Fatalf("the gate surfaced no asks at all over the curated cases, which is not a pass, it is a blind spot")
	}
	precision := float64(warranted) / float64(asks)
	t.Logf("ask precision %.3f over %d asks, %d warranted, %d missed (bar %.2f)",
		precision, asks, warranted, missed, BarGatePrecision)
	require.GreaterOrEqual(t, precision, BarGatePrecision, "of the asks surfaced, the warranted fraction")
}

// TestEvalGateExplainsEveryAsk asserts the explainability the design makes mandatory.
// A prompt that cannot say why it interrupted is not a gate, and a silent auto-commit
// that cannot say why it did not ask is not auditable.
func TestEvalGateExplainsEveryAsk(t *testing.T) {
	m := loadManifest(t)
	ctx := context.Background()

	for _, c := range m.Gate {
		s, _ := newCorpusStore(t, corpusSettings())
		g := memory.NewGate(s, nil, memory.DefaultPolicy())

		out, err := g.Add(ctx, memory.WriteRequest{
			SessionID: "eval-explain",
			Primary:   true,
			Entry: memory.Entry{
				Type:     memory.NormalType(c.Type),
				Thread:   "eval-explain",
				Summary:  "Explainability case " + c.ID,
				Body:     "A curated write used to check that the gate states its reason.",
				Pinned:   c.Pinned,
				Asserted: ptr(c.Asserted),
				Scope:    memory.ScopeGlobal,
			},
		})
		require.NoError(t, err)
		require.NotEmpty(t, out.Rationale, "case %s decided %q without saying why", c.ID, out.Status)
	}

	// The trail has to be queryable afterwards, not merely present on the response,
	// because "why did it ask" is a question asked after the fact.
	s, _ := newCorpusStore(t, corpusSettings())
	g := memory.NewGate(s, nil, memory.DefaultPolicy())
	_, err := g.Add(ctx, memory.WriteRequest{
		SessionID: "eval-trail",
		Primary:   true,
		Entry: memory.Entry{
			Type:     memory.TypeDecision,
			Thread:   "eval-trail",
			Summary:  "A decision the gate has to ask about",
			Body:     "Decision-shaped content forces the untunable floor to answer.",
			Asserted: ptr(true),
			Scope:    memory.ScopeGlobal,
		},
	})
	require.NoError(t, err)

	log, err := g.RecentAsks(ctx, 10)
	require.NoError(t, err)
	require.NotEmpty(t, log, "every gate decision belongs in the asklog")

	pending, err := g.Pending(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, pending, "an unanswered ask has to be parked, not dropped")
}

// TestEvalGatePolicyFloor is section 5's level one: policy memories sit on the hard
// override, so they may not be committed without asking at any dial position. This is
// asserted as its own case rather than folded into the precision bar, because an
// auto-committed policy is not a precision imperfection, it is a standing instruction
// the agent wrote for itself.
func TestEvalGatePolicyFloor(t *testing.T) {
	for _, mode := range []memory.AskMode{memory.AskModeAsk, memory.AskModeBalanced, memory.AskModeAuto} {
		s, _ := newCorpusStore(t, corpusSettings())
		policy := memory.DefaultPolicy()
		policy.AskMode = mode
		g := memory.NewGate(s, nil, policy)

		out, err := g.Add(context.Background(), memory.WriteRequest{
			SessionID: "eval-policy",
			Primary:   true,
			Entry: memory.Entry{
				Type:     memory.TypePolicy,
				Thread:   "eval-policy",
				Summary:  "Always ask before touching the deployment scripts",
				Body:     "A standing instruction the agent is adopting for future sessions.",
				Asserted: ptr(true),
				Scope:    memory.ScopeGlobal,
			},
		})
		require.NoError(t, err)
		require.Equal(t, memory.StatusPendingWrite, out.Status,
			"a policy memory may not auto-commit at dial position %q, it has to be confirmed", mode)
	}
}
