package session

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/hackafterdark/phosphor/pkg/db"
	"github.com/hackafterdark/phosphor/pkg/pubsub"
	"github.com/zeebo/xxh3"
)

type TodoStatus string

const (
	TodoStatusPending    TodoStatus = "pending"
	TodoStatusInProgress TodoStatus = "in_progress"
	TodoStatusCompleted  TodoStatus = "completed"
)

// HashID returns the XXH3 hash of a session ID (UUID) as a hex string.
func HashID(id string) string {
	h := xxh3.New()
	h.WriteString(id)
	return fmt.Sprintf("%x", h.Sum(nil))
}

type Todo struct {
	Content    string     `json:"content"`
	Status     TodoStatus `json:"status"`
	ActiveForm string     `json:"active_form"`
}

// HasIncompleteTodos returns true if there are any non-completed todos.
func HasIncompleteTodos(todos []Todo) bool {
	for _, todo := range todos {
		if todo.Status != TodoStatusCompleted {
			return true
		}
	}
	return false
}

type Session struct {
	ID               string
	ParentSessionID  string
	Title            string
	MessageCount     int64
	PromptTokens     int64
	CompletionTokens int64
	EstimatedUsage   bool
	SummaryMessageID string
	Cost             float64
	Todos            []Todo
	CreatedAt        int64
	UpdatedAt        int64
	CurrentTokens    int64
	IsStateless      bool
	Service          string
	IsPinned         bool
}

type Service interface {
	pubsub.Subscriber[Session]
	Create(ctx context.Context, title string) (Session, error)
	CreateTitleSession(ctx context.Context, parentSessionID string) (Session, error)
	CreateTaskSession(ctx context.Context, toolCallID, parentSessionID, title string) (Session, error)
	Get(ctx context.Context, id string) (Session, error)
	GetLast(ctx context.Context) (Session, error)
	List(ctx context.Context) ([]Session, error)
	Save(ctx context.Context, session Session) (Session, error)
	UpdateTitleAndUsage(ctx context.Context, sessionID, title string, promptTokens, completionTokens int64, cost float64) error
	RecordTokenUsage(ctx context.Context, sessionID, model, provider string, promptTokens, completionTokens int64, cost float64) error
	Rename(ctx context.Context, id string, title string) error
	UpdateStateless(ctx context.Context, sessionID string, stateless bool, service string) error
	Delete(ctx context.Context, id string) error

	// ListStatelessSessions returns all stateless sessions, optionally
	// filtered by service origin.
	ListStatelessSessions(ctx context.Context, serviceFilter string) ([]Session, error)

	// CountPrunableMessages returns the count of messages older than cutoff
	// for a session.
	CountPrunableMessages(ctx context.Context, sessionID string, cutoff time.Time) (int, error)

	// PruneMessages removes messages older than cutoff from a session and
	// returns the number of messages deleted.
	PruneMessages(ctx context.Context, sessionID string, cutoff time.Time) (int, error)

	// Pin marks a session as pinned to protect it from bulk deletion.
	Pin(ctx context.Context, sessionID string) error

	// Unpin removes the pinned status from a session.
	Unpin(ctx context.Context, sessionID string) error

	// ListPrunableSessions returns all non-pinned sessions older than the cutoff.
	ListPrunableSessions(ctx context.Context, before time.Time) ([]Session, error)

	// BulkDeleteSessions deletes all non-pinned sessions older than the cutoff.
	BulkDeleteSessions(ctx context.Context, before time.Time) (int, error)

	// Agent tool session management
	CreateAgentToolSessionID(messageID, toolCallID string) string
	ParseAgentToolSessionID(sessionID string) (messageID string, toolCallID string, ok bool)
	IsAgentToolSession(sessionID string) bool
}

type service struct {
	*pubsub.Broker[Session]
	db *sql.DB
	q  *db.Queries

	// Estimated usage stays in memory so fetch-modify-save paths (e.g.,
	// updating todos or parent-session cost) do not rebuild a session from
	// SQLite and incorrectly clear the UI "~" marker.
	estimatedUsageMu sync.RWMutex
	estimatedUsage   map[string]bool
}

func (s *service) Create(ctx context.Context, title string) (Session, error) {
	dbSession, err := s.q.CreateSession(ctx, db.CreateSessionParams{
		ID:    uuid.New().String(),
		Title: title,
	})
	if err != nil {
		return Session{}, err
	}
	session := s.fromDBItem(dbSession)
	s.Publish(pubsub.CreatedEvent, session)
	return session, nil
}

func (s *service) CreateTaskSession(ctx context.Context, toolCallID, parentSessionID, title string) (Session, error) {
	dbSession, err := s.q.CreateSession(ctx, db.CreateSessionParams{
		ID:              toolCallID,
		ParentSessionID: sql.NullString{String: parentSessionID, Valid: true},
		Title:           title,
	})
	if err != nil {
		return Session{}, err
	}
	session := s.fromDBItem(dbSession)
	s.Publish(pubsub.CreatedEvent, session)
	return session, nil
}

func (s *service) CreateTitleSession(ctx context.Context, parentSessionID string) (Session, error) {
	dbSession, err := s.q.CreateSession(ctx, db.CreateSessionParams{
		ID:              "title-" + parentSessionID,
		ParentSessionID: sql.NullString{String: parentSessionID, Valid: true},
		Title:           "Generate a title",
	})
	if err != nil {
		return Session{}, err
	}
	session := s.fromDBItem(dbSession)
	s.Publish(pubsub.CreatedEvent, session)
	return session, nil
}

func (s *service) Delete(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	qtx := s.q.WithTx(tx)

	dbSession, err := qtx.GetSessionByID(ctx, id)
	if err != nil {
		return err
	}
	if err = qtx.DeleteSessionMessages(ctx, dbSession.ID); err != nil {
		return fmt.Errorf("deleting session messages: %w", err)
	}
	if err = qtx.DeleteSessionFiles(ctx, dbSession.ID); err != nil {
		return fmt.Errorf("deleting session files: %w", err)
	}
	if err = qtx.DeleteSession(ctx, dbSession.ID); err != nil {
		return fmt.Errorf("deleting session: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("committing transaction: %w", err)
	}

	session := s.fromDBItem(dbSession)
	s.clearEstimatedUsageState(dbSession.ID)
	s.Publish(pubsub.DeletedEvent, session)
	return nil
}

func (s *service) Get(ctx context.Context, id string) (Session, error) {
	dbSession, err := s.q.GetSessionByID(ctx, id)
	if err != nil {
		return Session{}, err
	}
	session := s.fromDBItem(dbSession)
	s.applyEstimatedUsageState(&session)
	return session, nil
}

func (s *service) GetLast(ctx context.Context) (Session, error) {
	dbSession, err := s.q.GetLastSession(ctx)
	if err != nil {
		return Session{}, err
	}
	session := s.fromDBItem(dbSession)
	s.applyEstimatedUsageState(&session)
	return session, nil
}

func (s *service) Save(ctx context.Context, session Session) (Session, error) {
	todosJSON, err := marshalTodos(session.Todos)
	if err != nil {
		return Session{}, err
	}

	dbSession, err := s.q.UpdateSession(ctx, db.UpdateSessionParams{
		ID:               session.ID,
		Title:            session.Title,
		PromptTokens:     session.PromptTokens,
		CompletionTokens: session.CompletionTokens,
		SummaryMessageID: sql.NullString{
			String: session.SummaryMessageID,
			Valid:  session.SummaryMessageID != "",
		},
		Cost: session.Cost,
		Todos: sql.NullString{
			String: todosJSON,
			Valid:  todosJSON != "",
		},
		CurrentTokens: session.CurrentTokens,
	})
	if err != nil {
		return Session{}, err
	}
	estimatedUsage := session.EstimatedUsage
	s.setEstimatedUsageState(session.ID, estimatedUsage)
	session = s.fromDBItem(dbSession)
	session.EstimatedUsage = estimatedUsage
	s.Publish(pubsub.UpdatedEvent, session)
	return session, nil
}

// UpdateTitleAndUsage updates only the title and usage fields atomically.
// This is safer than fetching, modifying, and saving the entire session.
func (s *service) UpdateTitleAndUsage(ctx context.Context, sessionID, title string, promptTokens, completionTokens int64, cost float64) error {
	if err := s.q.UpdateSessionTitleAndUsage(ctx, db.UpdateSessionTitleAndUsageParams{
		ID:               sessionID,
		Title:            title,
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		Cost:             cost,
	}); err != nil {
		return err
	}
	s.publishSessionUpdate(ctx, sessionID)
	return nil
}

// RecordTokenUsage inserts a new token usage record.
func (s *service) RecordTokenUsage(ctx context.Context, sessionID, model, provider string, promptTokens, completionTokens int64, cost float64) error {
	id := uuid.New().String()
	return s.q.RecordTokenUsage(ctx, db.RecordTokenUsageParams{
		ID:               id,
		SessionID:        sessionID,
		Model:            model,
		Provider:         provider,
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		Cost:             cost,
	})
}

// Rename updates only the title of a session without touching updated_at or
// usage fields.
func (s *service) Rename(ctx context.Context, id string, title string) error {
	if err := s.q.RenameSession(ctx, db.RenameSessionParams{
		ID:    id,
		Title: title,
	}); err != nil {
		return err
	}
	s.publishSessionUpdate(ctx, id)
	return nil
}

// UpdateStateless marks a session as stateless (agent skips history loading)
// and records the service origin for audit provenance.
func (s *service) UpdateStateless(ctx context.Context, sessionID string, stateless bool, service string) error {
	if err := s.q.UpdateStatelessSession(ctx, db.UpdateStatelessSessionParams{
		ID:          sessionID,
		IsStateless: boolToInt64(stateless),
		Service:     service,
	}); err != nil {
		return err
	}
	s.publishSessionUpdate(ctx, sessionID)
	return nil
}

// ListStatelessSessions returns all stateless sessions, optionally filtered
// by service origin.
func (s *service) ListStatelessSessions(ctx context.Context, serviceFilter string) ([]Session, error) {
	dbSessions, err := s.q.ListStatelessSessions(ctx, db.ListStatelessSessionsParams{
		ServiceFilter: serviceFilter,
	})
	if err != nil {
		return nil, err
	}
	sessions := make([]Session, len(dbSessions))
	for i, dbSession := range dbSessions {
		sessions[i] = s.fromDBItem(dbSession)
	}
	return sessions, nil
}

// CountPrunableMessages returns the count of messages older than cutoff for a
// session.
func (s *service) CountPrunableMessages(ctx context.Context, sessionID string, cutoff time.Time) (int, error) {
	return s.q.CountPrunableMessages(ctx, db.CountPrunableMessagesParams{
		SessionID: sessionID,
		CutoffAt:  cutoff.Unix(),
	})
}

// PruneMessages removes messages older than cutoff from a session and returns
// the number of messages deleted.
func (s *service) PruneMessages(ctx context.Context, sessionID string, cutoff time.Time) (int, error) {
	count, err := s.CountPrunableMessages(ctx, sessionID, cutoff)
	if err != nil {
		return 0, err
	}

	if count == 0 {
		return 0, nil
	}

	if err := s.q.PruneSessionMessages(ctx, db.PruneSessionMessagesParams{
		SessionID: sessionID,
		CutoffAt:  cutoff.Unix(),
	}); err != nil {
		return 0, fmt.Errorf("pruning messages: %w", err)
	}

	s.publishSessionUpdate(ctx, sessionID)
	return count, nil
}

func (s *service) List(ctx context.Context) ([]Session, error) {
	dbSessions, err := s.q.ListSessions(ctx)
	if err != nil {
		return nil, err
	}
	sessions := make([]Session, len(dbSessions))
	for i, dbSession := range dbSessions {
		sessions[i] = s.fromDBItem(dbSession)
		s.applyEstimatedUsageState(&sessions[i])
	}
	return sessions, nil
}

// publishSessionUpdate re-fetches a session and publishes an UpdatedEvent so
// that UI subscribers reflect title or usage changes.
func (s *service) publishSessionUpdate(ctx context.Context, sessionID string) {
	session, err := s.Get(ctx, sessionID)
	if err != nil {
		slog.Error("Failed to re-fetch session for event publish", "error", err, "sessionID", sessionID)
		return
	}
	s.Publish(pubsub.UpdatedEvent, session)
}

func (s *service) applyEstimatedUsageState(session *Session) {
	s.estimatedUsageMu.RLock()
	session.EstimatedUsage = s.estimatedUsage[session.ID]
	s.estimatedUsageMu.RUnlock()
}

func (s *service) setEstimatedUsageState(sessionID string, estimatedUsage bool) {
	s.estimatedUsageMu.Lock()
	defer s.estimatedUsageMu.Unlock()
	if estimatedUsage {
		s.estimatedUsage[sessionID] = true
		return
	}
	delete(s.estimatedUsage, sessionID)
}

func (s *service) clearEstimatedUsageState(sessionID string) {
	s.estimatedUsageMu.Lock()
	delete(s.estimatedUsage, sessionID)
	s.estimatedUsageMu.Unlock()
}

func (s *service) fromDBItem(item db.Session) Session {
	todos, err := unmarshalTodos(item.Todos.String)
	if err != nil {
		slog.Error("Failed to unmarshal todos", "session_id", item.ID, "error", err)
	}
	return Session{
		ID:               item.ID,
		ParentSessionID:  item.ParentSessionID.String,
		Title:            item.Title,
		MessageCount:     item.MessageCount,
		PromptTokens:     item.PromptTokens,
		CompletionTokens: item.CompletionTokens,
		SummaryMessageID: item.SummaryMessageID.String,
		Cost:             item.Cost,
		Todos:            todos,
		CreatedAt:        item.CreatedAt,
		UpdatedAt:        item.UpdatedAt,
		CurrentTokens:    item.CurrentTokens,
		IsStateless:      item.IsStateless != 0,
		Service:          item.Service,
		IsPinned:         item.IsPinned != 0,
	}
}

func marshalTodos(todos []Todo) (string, error) {
	if len(todos) == 0 {
		return "", nil
	}
	data, err := json.Marshal(todos)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func boolToInt64(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

func unmarshalTodos(data string) ([]Todo, error) {
	if data == "" {
		return []Todo{}, nil
	}
	var todos []Todo
	if err := json.Unmarshal([]byte(data), &todos); err != nil {
		return []Todo{}, err
	}
	return todos, nil
}

// Pin marks a session as pinned to protect it from bulk deletion.
func (s *service) Pin(ctx context.Context, sessionID string) error {
	return s.q.UpdatePinned(ctx, db.UpdatePinnedParams{
		ID:       sessionID,
		IsPinned: 1,
	})
}

// Unpin removes the pinned status from a session.
func (s *service) Unpin(ctx context.Context, sessionID string) error {
	return s.q.UpdatePinned(ctx, db.UpdatePinnedParams{
		ID:       sessionID,
		IsPinned: 0,
	})
}

// ListPrunableSessions returns all non-pinned sessions older than the cutoff.
func (s *service) ListPrunableSessions(ctx context.Context, before time.Time) ([]Session, error) {
	dbSessions, err := s.q.ListPrunableSessions(ctx, before.Unix())
	if err != nil {
		return nil, err
	}
	sessions := make([]Session, len(dbSessions))
	for i, dbSession := range dbSessions {
		sessions[i] = s.fromDBItem(dbSession)
	}
	return sessions, nil
}

// BulkDeleteSessions deletes all non-pinned sessions older than the cutoff.
func (s *service) BulkDeleteSessions(ctx context.Context, before time.Time) (int, error) {
	// First count how many sessions would be deleted.
	sessions, err := s.ListPrunableSessions(ctx, before)
	if err != nil {
		return 0, err
	}

	if err := s.q.BulkDeleteSessions(ctx, before.Unix()); err != nil {
		return 0, fmt.Errorf("bulk deleting sessions: %w", err)
	}

	for _, session := range sessions {
		s.Publish(pubsub.DeletedEvent, session)
	}
	return len(sessions), nil
}

func NewService(q *db.Queries, conn *sql.DB) Service {
	broker := pubsub.NewBroker[Session]()
	return &service{
		Broker:         broker,
		db:             conn,
		q:              q,
		estimatedUsage: make(map[string]bool),
	}
}

// CreateAgentToolSessionID creates a session ID for agent tool sessions using the format "messageID$$toolCallID"
func (s *service) CreateAgentToolSessionID(messageID, toolCallID string) string {
	return fmt.Sprintf("%s$$%s", messageID, toolCallID)
}

// ParseAgentToolSessionID parses an agent tool session ID into its components
func (s *service) ParseAgentToolSessionID(sessionID string) (messageID string, toolCallID string, ok bool) {
	parts := strings.Split(sessionID, "$$")
	if len(parts) != 2 {
		return "", "", false
	}
	return parts[0], parts[1], true
}

// IsAgentToolSession checks if a session ID follows the agent tool session format
func (s *service) IsAgentToolSession(sessionID string) bool {
	_, _, ok := s.ParseAgentToolSessionID(sessionID)
	return ok
}
