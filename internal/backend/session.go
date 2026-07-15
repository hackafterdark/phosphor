package backend

import (
	"context"
	"log/slog"
	"time"

	"github.com/hackafterdark/phosphor/internal/proto"
	"github.com/hackafterdark/phosphor/pkg/message"
	"github.com/hackafterdark/phosphor/pkg/session"
)

// CreateSession creates a new session in the given workspace.
func (b *Backend) CreateSession(ctx context.Context, workspaceID, title string) (session.Session, error) {
	ws, err := b.GetWorkspace(workspaceID)
	if err != nil {
		slog.Error("CreateSession: workspace not found", "workspace_id", workspaceID, "error", err)
		return session.Session{}, err
	}
	slog.Info("CreateSession: workspace found", "workspace_id", workspaceID, "client_count", len(ws.clients))

	return ws.Sessions.Create(ctx, title)
}

// GetSession retrieves a session by workspace and session ID.
func (b *Backend) GetSession(ctx context.Context, workspaceID, sessionID string) (session.Session, error) {
	ws, err := b.GetWorkspace(workspaceID)
	if err != nil {
		return session.Session{}, err
	}

	return ws.Sessions.Get(ctx, sessionID)
}

// ListSessions returns all sessions in the given workspace.
func (b *Backend) ListSessions(ctx context.Context, workspaceID string) ([]session.Session, error) {
	ws, err := b.GetWorkspace(workspaceID)
	if err != nil {
		return nil, err
	}

	return ws.Sessions.List(ctx)
}

// ListSessionsFiltered returns all non-stateless sessions in the given
// workspace. Stateless sessions (created by OpenAI-compatible clients
// without a session ID) are hidden from the TUI so they don't clutter
// the session list with "Untitled" entries.
func (b *Backend) ListSessionsFiltered(ctx context.Context, workspaceID string) ([]session.Session, error) {
	all, err := b.ListSessions(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	filtered := make([]session.Session, 0, len(all))
	for _, s := range all {
		if !s.IsStateless && s.Service != "cron" {
			filtered = append(filtered, s)
		}
	}
	return filtered, nil
}

// GetAgentSession returns session metadata with the agent's busy
// status.
func (b *Backend) GetAgentSession(ctx context.Context, workspaceID, sessionID string) (proto.AgentSession, error) {
	ws, err := b.GetWorkspace(workspaceID)
	if err != nil {
		return proto.AgentSession{}, err
	}

	se, err := ws.Sessions.Get(ctx, sessionID)
	if err != nil {
		return proto.AgentSession{}, err
	}

	var isSessionBusy bool
	if ws.AgentCoordinator != nil {
		isSessionBusy = ws.AgentCoordinator.IsSessionBusy(sessionID)
	}

	return proto.AgentSession{
		Session: proto.Session{
			ID:    se.ID,
			Title: se.Title,
		},
		IsBusy: isSessionBusy,
	}, nil
}

// ListSessionMessages returns all messages for a session.
func (b *Backend) ListSessionMessages(ctx context.Context, workspaceID, sessionID string) ([]message.Message, error) {
	ws, err := b.GetWorkspace(workspaceID)
	if err != nil {
		return nil, err
	}

	// Drain debounced updates so HTTP clients (and the TUI on session
	// switch) observe the latest in-memory state rather than racing the
	// debounce timer in message.Service.
	if err := ws.Messages.FlushAll(ctx); err != nil {
		return nil, err
	}
	return ws.Messages.List(ctx, sessionID)
}

// ListSessionHistory returns the history items for a session.
func (b *Backend) ListSessionHistory(ctx context.Context, workspaceID, sessionID string) (any, error) {
	ws, err := b.GetWorkspace(workspaceID)
	if err != nil {
		return nil, err
	}

	return ws.History.ListBySession(ctx, sessionID)
}

// SaveSession updates a session in the given workspace.
func (b *Backend) SaveSession(ctx context.Context, workspaceID string, sess session.Session) (session.Session, error) {
	ws, err := b.GetWorkspace(workspaceID)
	if err != nil {
		return session.Session{}, err
	}

	return ws.Sessions.Save(ctx, sess)
}

// DeleteSession deletes a session from the given workspace.
func (b *Backend) DeleteSession(ctx context.Context, workspaceID, sessionID string) error {
	ws, err := b.GetWorkspace(workspaceID)
	if err != nil {
		return err
	}

	return ws.Sessions.Delete(ctx, sessionID)
}

// RenameSession renames a session in the given workspace.
func (b *Backend) RenameSession(ctx context.Context, workspaceID, sessionID, title string) error {
	ws, err := b.GetWorkspace(workspaceID)
	if err != nil {
		return err
	}

	return ws.Sessions.Rename(ctx, sessionID, title)
}

// UpdateSessionStateless marks a session as stateless (agent skips history
// loading) and records the service origin for audit provenance.
func (b *Backend) UpdateSessionStateless(ctx context.Context, workspaceID, sessionID string, stateless bool, service string) error {
	ws, err := b.GetWorkspace(workspaceID)
	if err != nil {
		return err
	}

	return ws.Sessions.UpdateStateless(ctx, sessionID, stateless, service)
}

// GetDefaultSession returns the first stateless session for the given
// workspace, or ErrSessionNotFound if none exists.
func (b *Backend) GetDefaultSession(ctx context.Context, workspaceID string) (session.Session, error) {
	ws, err := b.GetWorkspace(workspaceID)
	if err != nil {
		return session.Session{}, err
	}

	sessions, err := ws.Sessions.List(ctx)
	if err != nil {
		return session.Session{}, err
	}
	for _, s := range sessions {
		if s.IsStateless {
			return s, nil
		}
	}
	return session.Session{}, ErrSessionNotFound
}

// ListStatelessSessions returns all stateless sessions in the workspace,
// optionally filtered by service origin.
func (b *Backend) ListStatelessSessions(ctx context.Context, workspaceID, serviceFilter string) ([]session.Session, error) {
	ws, err := b.GetWorkspace(workspaceID)
	if err != nil {
		return nil, err
	}

	return ws.Sessions.ListStatelessSessions(ctx, serviceFilter)
}

// CountPrunableMessages returns the count of messages older than cutoff for a
// session in the workspace.
func (b *Backend) CountPrunableMessages(ctx context.Context, workspaceID, sessionID string, cutoff time.Time) (int, error) {
	ws, err := b.GetWorkspace(workspaceID)
	if err != nil {
		return 0, err
	}

	return ws.Sessions.CountPrunableMessages(ctx, sessionID, cutoff)
}

// PruneStatelessSession removes messages older than cutoff from a stateless
// session in the workspace and returns the number of messages deleted.
func (b *Backend) PruneStatelessSession(ctx context.Context, workspaceID, sessionID string, cutoff time.Time) (int, error) {
	ws, err := b.GetWorkspace(workspaceID)
	if err != nil {
		return 0, err
	}

	return ws.Sessions.PruneMessages(ctx, sessionID, cutoff)
}

// ListUserMessages returns user-role messages for a session.
func (b *Backend) ListUserMessages(ctx context.Context, workspaceID, sessionID string) ([]message.Message, error) {
	ws, err := b.GetWorkspace(workspaceID)
	if err != nil {
		return nil, err
	}

	return ws.Messages.ListUserMessages(ctx, sessionID)
}

// ListAllUserMessages returns all user-role messages across sessions.
func (b *Backend) ListAllUserMessages(ctx context.Context, workspaceID string) ([]message.Message, error) {
	ws, err := b.GetWorkspace(workspaceID)
	if err != nil {
		return nil, err
	}

	return ws.Messages.ListAllUserMessages(ctx)
}
