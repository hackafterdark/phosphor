package httpapi

import (
	"net/http"
	"time"

	"github.com/hackafterdark/phosphor/internal/session"
	"github.com/labstack/echo/v5"
)

type statelessSessionResponse struct {
	ID           string `json:"id"`
	UUID         string `json:"uuid"`
	Title        string `json:"title"`
	Service      string `json:"service"`
	MessageCount int64  `json:"message_count"`
	CreatedAt    int64  `json:"created_at"`
	UpdatedAt    int64  `json:"updated_at"`
}

// listStatelessSessions lists all stateless sessions, optionally filtered by
// service origin.
// GET /v1/stateless-sessions?service=openai-api
func (s *Service) listStatelessSessions(c *echo.Context) error {
	serviceFilter := c.QueryParam("service")

	sessions, err := s.srv.Backend().ListStatelessSessions(c.Request().Context(), s.workspace, serviceFilter)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}

	output := make([]statelessSessionResponse, len(sessions))
	for i, sess := range sessions {
		output[i] = statelessSessionResponse{
			ID:           session.HashID(sess.ID),
			UUID:         sess.ID,
			Title:        sess.Title,
			Service:      sess.Service,
			MessageCount: sess.MessageCount,
			CreatedAt:    sess.CreatedAt,
			UpdatedAt:    sess.UpdatedAt,
		}
	}

	return c.JSON(http.StatusOK, output)
}

type pruneRequest struct {
	Before string `json:"before"`
	DryRun bool   `json:"dry_run"`
}

type pruneResponse struct {
	SessionID      string `json:"session_id"`
	MessagesPruned int    `json:"messages_pruned"`
	DryRun         bool   `json:"dry_run,omitempty"`
}

// pruneStatelessSession prunes messages older than cutoff from a stateless
// session.
// POST /v1/stateless-sessions/:session-id/prune
func (s *Service) pruneStatelessSession(c *echo.Context) error {
	sessionID := c.Param("session-id")

	var req pruneRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}

	cutoff, err := time.Parse(time.RFC3339, req.Before)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid 'before' timestamp (must be RFC3339)")
	}

	ctx := c.Request().Context()

	if req.DryRun {
		count, err := s.srv.Backend().CountPrunableMessages(ctx, s.workspace, sessionID, cutoff)
		if err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
		}

		return c.JSON(http.StatusOK, pruneResponse{
			SessionID:      sessionID,
			MessagesPruned: count,
			DryRun:         true,
		})
	}

	count, err := s.srv.Backend().PruneStatelessSession(ctx, s.workspace, sessionID, cutoff)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}

	return c.JSON(http.StatusOK, pruneResponse{
		SessionID:      sessionID,
		MessagesPruned: count,
	})
}
