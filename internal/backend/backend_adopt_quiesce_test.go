package backend

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestWorkspaceQuiesceCancelsAndDrains pins the adopted-workspace
// teardown contract: quiesce marks the workspace closing, cancels the
// workspace context, and waits for dispatched runs to return, without
// calling the embedded app cleanup.
func TestWorkspaceQuiesceCancelsAndDrains(t *testing.T) {
	wsCtx, wsCancel := context.WithCancel(context.Background())
	ws := &Workspace{ctx: wsCtx, cancel: wsCancel}

	done := make(chan struct{})
	ws.runWG.Add(1)
	go func() {
		defer ws.runWG.Done()
		defer close(done)
		<-ws.ctx.Done()
	}()

	ws.quiesce()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("dispatched run was not drained by quiesce")
	}
	require.True(t, ws.closing)
	require.ErrorIs(t, wsCtx.Err(), context.Canceled)
}
