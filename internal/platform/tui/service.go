package tui

import (
	"context"
	"database/sql"
	"log/slog"
	"os"
	"sync"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/hackafterdark/phosphor/internal/ui/common"
	ui "github.com/hackafterdark/phosphor/internal/ui/model"
	"github.com/hackafterdark/phosphor/internal/workspace"
)

// Service wraps the Bubble Tea terminal user interface as a registered platform service.
type Service struct {
	ws           workspace.Workspace
	dbConn       *sql.DB
	sessionID    string
	continueLast bool
	logger       *slog.Logger
	program      *tea.Program
	doneCh       chan struct{}
	stopOnce     sync.Once
}

// NewService creates a new TUI service.
func NewService(ws workspace.Workspace, dbConn *sql.DB, sessionID string, continueLast bool, logger *slog.Logger) *Service {
	return &Service{
		ws:           ws,
		dbConn:       dbConn,
		sessionID:    sessionID,
		continueLast: continueLast,
		logger:       logger,
		doneCh:       make(chan struct{}),
	}
}

// Name returns the service name "tui".
func (s *Service) Name() string {
	return "tui"
}

// Describe returns a description of the TUI service.
func (s *Service) Describe() string {
	return "Bubble Tea terminal interface"
}

// Start runs the Bubble Tea TUI asynchronously.
func (s *Service) Start(ctx context.Context) error {
	com := common.NewCommon(s.ws, s.dbConn)
	model := ui.New(com, s.sessionID, s.continueLast)

	inputFilter := ui.NewFilter()
	var env uv.Environ = os.Environ()
	s.program = tea.NewProgram(
		model,
		tea.WithEnvironment(env),
		tea.WithContext(ctx),
		tea.WithFilter(inputFilter.Filter),
	)

	// Subscribe to workspace events.
	go s.ws.Subscribe(s.program)

	go func() {
		defer close(s.doneCh)
		if _, err := s.program.Run(); err != nil {
			s.logger.Error("TUI run error", "error", err)
		}
	}()

	return nil
}

// Stop gracefully stops the Bubble Tea TUI.
func (s *Service) Stop(ctx context.Context) error {
	s.stopOnce.Do(func() {
		if s.program != nil {
			s.program.Quit()
		}
	})

	select {
	case <-s.doneCh:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Done returns a channel that is closed when the TUI program exits.
func (s *Service) Done() <-chan struct{} {
	return s.doneCh
}
