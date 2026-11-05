package agent

import (
	"context"
	"log/slog"

	"github.com/hackafterdark/phosphor/pkg/session"
)

// reconcileStaleTodos demotes any todo left in_progress on the session to
// pending. It runs on every terminal path of a turn — normal completion,
// error, and cancel — because the model can end its run without updating
// the todo it was working on (the classic "last bookkeeping step skipped"
// pattern), and the UI renders the persisted state verbatim: a stale
// in_progress looks like live work forever and leaks into future prompts
// as the active todo.
//
// Demote rather than auto-complete: marking an item completed would assert
// the work succeeded, which cannot be known when the run ended in error or
// cancellation. Pending is honest — the item is open and nothing is
// running it — and the next turn's prompt shows the model the pending
// item so it can re-start it or mark it completed on the evidence.
//
// Errors are logged, not returned: reconciliation is state hygiene on a
// terminal path and must never alter the run's outcome.
func (a *sessionAgent) reconcileStaleTodos(ctx context.Context, sessionID string) {
	// The terminal defer runs after workspace shutdown cancels the run
	// context, so detach the cancellation (bounded by a timeout) or the
	// database writes below would be rejected exactly when reconciliation
	// matters most.
	ctx, cancel := durableWriteCtx(ctx)
	defer cancel()
	currentSession, err := a.sessions.Get(ctx, sessionID)
	if err != nil {
		slog.Warn("Failed to load session for todo reconciliation", "error", err, "session_id", sessionID)
		return
	}

	var stale bool
	for i := range currentSession.Todos {
		if currentSession.Todos[i].Status == session.TodoStatusInProgress {
			currentSession.Todos[i].Status = session.TodoStatusPending
			stale = true
		}
	}
	if !stale {
		return
	}

	// Targeted todos-only write: it preserves concurrent title, usage,
	// and summary updates (GenerateTitle runs in a goroutine that can
	// land between this read and write) and publishes the session
	// UpdatedEvent itself, so the UI refreshes without an extra publish.
	if err := a.sessions.UpdateTodos(ctx, sessionID, currentSession.Todos); err != nil {
		slog.Warn("Failed to reconcile stale todos", "error", err, "session_id", sessionID)
	}
}
