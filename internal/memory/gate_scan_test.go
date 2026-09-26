package memory

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

// These paths read whole rows back out of the index. They are the ones a
// single-destination database/sql.Rows.Scan silently breaks, so each gets a
// direct round trip rather than riding along behind the search tests.

func TestReportCountsByType(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openTestStore(t, DefaultSettings())
	yes := true

	for i, typ := range []Type{TypeDecision, TypeDecision, TypeConstraint} {
		name := fmt.Sprintf("%s-%d", typ, i)
		e := Entry{
			Type: typ, Thread: "report", ID: NewID("report", name),
			Summary: "report " + name, Body: "body",
			Status: StatusActive, Asserted: &yes,
		}
		require.NoError(t, s.Put(ctx, e))
	}

	st, err := s.Report(ctx)
	require.NoError(t, err)
	require.Equal(t, 3, st.Active)
	require.Equal(t, 2, st.ByType[string(TypeDecision)])
	require.Equal(t, 1, st.ByType[string(TypeConstraint)])
}

func TestNeighborsFollowsEdges(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openTestStore(t, DefaultSettings())
	yes := true

	src := Entry{
		Type: TypeDecision, Thread: "graph", ID: NewID("graph", "source"),
		Summary: "the linking decision", Body: "body", Status: StatusActive,
		Asserted: &yes, Links: []string{"[[target-id]]"},
	}
	dst := Entry{
		Type: TypeFact, Thread: "graph", ID: "target-id",
		Summary: "the linked fact", Body: "body", Status: StatusActive, Asserted: &yes,
	}
	require.NoError(t, s.Put(ctx, dst))
	require.NoError(t, s.Put(ctx, src))

	hits, err := s.Neighbors(ctx, src.ID)
	require.NoError(t, err)
	require.Len(t, hits, 1)
	require.Equal(t, "target-id", hits[0].ID)
	require.Equal(t, "linked:wikilink", hits[0].Why)
}

func TestProposalQueueAndAuditTrailRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openTestStore(t, DefaultSettings())
	yes := true

	gate := NewGate(s, nil, Policy{
		AskMode: AskModeAsk, Adaptive: true, MinSamples: 1,
		Untunable: []string{}, MaxAsksPerTurn: 0,
	})

	e := Entry{
		Type: TypeDecision, Thread: "asked", ID: NewID("asked", "needs approval"),
		Summary: "the entry that must be approved", Body: "an approval needle for recall",
		Status: StatusActive, Asserted: &yes,
	}
	out, err := gate.Add(ctx, WriteRequest{SessionID: "s1", Primary: true, Entry: e})
	require.NoError(t, err)
	require.Equal(t, StatusPendingWrite, out.Status)
	require.NotNil(t, out.Proposal)

	pending, err := gate.Pending(ctx)
	require.NoError(t, err)
	require.Len(t, pending, 1)
	require.Equal(t, string(OpAdd), pending[0].Op)
	require.NotEmpty(t, pending[0].Rationale)

	done, err := gate.Resolve(ctx, pending[0].ID, true)
	require.NoError(t, err)
	require.Equal(t, StatusCommitted, done.Status)

	hits, err := s.Search(ctx, SearchQuery{Query: "approval needle", Limit: 5})
	require.NoError(t, err)
	require.Len(t, hits, 1)

	asks, err := gate.RecentAsks(ctx, 10)
	require.NoError(t, err)
	require.Greater(t, len(asks), 0)
	require.Equal(t, "s1", asks[0].SessionID)
	require.Equal(t, "ask", asks[0].Decision)

	bucket, ok, err := gate.Bucket(ctx, pending[0].Bucket)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, pending[0].Bucket, bucket.Name)
}
