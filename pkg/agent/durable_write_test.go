package agent

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestDurableWriteCtxOutlivesParentCancelButStaysBounded pins the pair of guarantees
// the terminal message writes depend on. A write must outlive the cancellation of the
// run that issued it, otherwise a cancel racing the end of a turn drops the Finish
// part and the UI renders an already answered message as live thinking forever. It
// must also stay bounded, otherwise an abandoned run keeps a handle on the database
// for as long as the session scoped context lives.
func TestDurableWriteCtxOutlivesParentCancelButStaysBounded(t *testing.T) {
	t.Parallel()

	parent, cancel := context.WithCancel(context.Background())
	cancel()

	writeCtx, cancelWrite := durableWriteCtx(parent)
	defer cancelWrite()

	require.NoError(t, writeCtx.Err(), "a terminal write must not inherit the run's cancellation")

	deadline, ok := writeCtx.Deadline()
	require.True(t, ok, "the detached write must carry a deadline rather than run unbounded")
	require.False(t, deadline.After(time.Now().Add(messageWriteTimeout+time.Second)),
		"the detached write must be bounded by messageWriteTimeout")

	cancelWrite()
	require.ErrorIs(t, context.Canceled, writeCtx.Err())
}
