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

// TestWorkspaceQuiesceBoundsTheDrain pins the bounded-drain policy:
// a run that ignores cancellation must not park shutdown. quiesce
// returns on the drain deadline and abandons the straggler.
func TestWorkspaceQuiesceBoundsTheDrain(t *testing.T) {
	old := runDrainTimeout
	runDrainTimeout = 50 * time.Millisecond
	t.Cleanup(func() { runDrainTimeout = old })

	wsCtx, wsCancel := context.WithCancel(context.Background())
	ws := &Workspace{ctx: wsCtx, cancel: wsCancel}

	// A run that never returns, even after cancellation.
	hang := make(chan struct{})
	t.Cleanup(func() { close(hang) })
	ws.runWG.Add(1)
	go func() {
		defer ws.runWG.Done()
		<-hang
	}()

	start := time.Now()
	ws.quiesce()

	require.Less(t, time.Since(start), time.Second,
		"quiesce must return on the drain deadline, not wait for the hang")
	require.True(t, ws.closing)
	require.ErrorIs(t, wsCtx.Err(), context.Canceled)
}
