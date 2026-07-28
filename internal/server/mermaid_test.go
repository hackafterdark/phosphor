package server_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hackafterdark/phosphor/internal/server"
	"github.com/hackafterdark/phosphor/pkg/db"
	"github.com/labstack/echo/v5"
	"github.com/stretchr/testify/require"
)

// mockQuerier is a minimal implementation of db.Querier for tests.
type mockQuerier struct {
	diagrams map[int64]db.Diagram
	nextID   int64
}

func (m *mockQuerier) CreateDiagram(ctx context.Context, arg db.CreateDiagramParams) (db.Diagram, error) {
	m.nextID++
	d := db.Diagram{
		ID:        m.nextID,
		SessionID: arg.SessionID,
		Syntax:    arg.Syntax,
		CreatedAt: time.Now().UnixMilli(),
	}
	if m.diagrams == nil {
		m.diagrams = make(map[int64]db.Diagram)
	}
	m.diagrams[d.ID] = d
	return d, nil
}

func (m *mockQuerier) GetDiagram(ctx context.Context, id int64) (db.Diagram, error) {
	d, ok := m.diagrams[id]
	if !ok {
		return db.Diagram{}, nil
	}
	return d, nil
}

// Stub methods (no-op for unused querier methods)
func (m *mockQuerier) AccumulateActiveTime(ctx context.Context, sessionID string) error { return nil }
func (m *mockQuerier) CreateFile(ctx context.Context, arg db.CreateFileParams) (db.File, error) {
	return db.File{}, nil
}
func (m *mockQuerier) CreateGoal(ctx context.Context, arg db.CreateGoalParams) (db.Goal, error) {
	return db.Goal{}, nil
}
func (m *mockQuerier) CreateMessage(ctx context.Context, arg db.CreateMessageParams) (db.Message, error) {
	return db.Message{}, nil
}
func (m *mockQuerier) CreateSession(ctx context.Context, arg db.CreateSessionParams) (db.Session, error) {
	return db.Session{}, nil
}
func (m *mockQuerier) DeleteFile(ctx context.Context, id string) error                   { return nil }
func (m *mockQuerier) DeleteGoal(ctx context.Context, sessionID string) error            { return nil }
func (m *mockQuerier) DeleteMessage(ctx context.Context, id string) error                { return nil }
func (m *mockQuerier) DeleteSession(ctx context.Context, id string) error                { return nil }
func (m *mockQuerier) DeleteSessionFiles(ctx context.Context, sessionID string) error    { return nil }
func (m *mockQuerier) DeleteSessionMessages(ctx context.Context, sessionID string) error { return nil }
func (m *mockQuerier) GetAverageResponseTime(ctx context.Context) (int64, error)         { return 0, nil }
func (m *mockQuerier) GetFile(ctx context.Context, id string) (db.File, error)           { return db.File{}, nil }
func (m *mockQuerier) GetFileByPathAndSession(ctx context.Context, arg db.GetFileByPathAndSessionParams) (db.File, error) {
	return db.File{}, nil
}
func (m *mockQuerier) GetFileRead(ctx context.Context, arg db.GetFileReadParams) (db.ReadFile, error) {
	return db.ReadFile{}, nil
}
func (m *mockQuerier) GetGoalBySessionID(ctx context.Context, sessionID string) (db.Goal, error) {
	return db.Goal{}, nil
}
func (m *mockQuerier) GetHourDayHeatmap(ctx context.Context) ([]db.GetHourDayHeatmapRow, error) {
	return nil, nil
}
func (m *mockQuerier) GetLastSession(ctx context.Context) (db.Session, error) {
	return db.Session{}, nil
}
func (m *mockQuerier) GetMessage(ctx context.Context, id string) (db.Message, error) {
	return db.Message{}, nil
}
func (m *mockQuerier) GetRecentActivity(ctx context.Context) ([]db.GetRecentActivityRow, error) {
	return nil, nil
}
func (m *mockQuerier) GetSessionByID(ctx context.Context, id string) (db.Session, error) {
	return db.Session{}, nil
}
func (m *mockQuerier) GetToolUsage(ctx context.Context) ([]db.GetToolUsageRow, error) {
	return nil, nil
}
func (m *mockQuerier) GetTotalStats(ctx context.Context) (db.GetTotalStatsRow, error) {
	return db.GetTotalStatsRow{}, nil
}
func (m *mockQuerier) GetUsageByDay(ctx context.Context) ([]db.GetUsageByDayRow, error) {
	return nil, nil
}
func (m *mockQuerier) GetUsageByDayOfWeek(ctx context.Context) ([]db.GetUsageByDayOfWeekRow, error) {
	return nil, nil
}
func (m *mockQuerier) GetUsageByDayRange(ctx context.Context, strftime interface{}) ([]db.GetUsageByDayRangeRow, error) {
	return nil, nil
}
func (m *mockQuerier) GetUsageByHour(ctx context.Context) ([]db.GetUsageByHourRow, error) {
	return nil, nil
}
func (m *mockQuerier) GetUsageByModel(ctx context.Context) ([]db.GetUsageByModelRow, error) {
	return nil, nil
}
func (m *mockQuerier) ListAllUserMessages(ctx context.Context) ([]db.Message, error) { return nil, nil }
func (m *mockQuerier) ListFilesByPath(ctx context.Context, path string) ([]db.File, error) {
	return nil, nil
}
func (m *mockQuerier) ListFilesBySession(ctx context.Context, sessionID string) ([]db.File, error) {
	return nil, nil
}
func (m *mockQuerier) ListLatestSessionFiles(ctx context.Context, sessionID string) ([]db.File, error) {
	return nil, nil
}
func (m *mockQuerier) ListMessagesBySession(ctx context.Context, sessionID string) ([]db.Message, error) {
	return nil, nil
}
func (m *mockQuerier) ListNewFiles(ctx context.Context) ([]db.File, error) { return nil, nil }
func (m *mockQuerier) ListSessionReadFiles(ctx context.Context, sessionID string) ([]db.ReadFile, error) {
	return nil, nil
}
func (m *mockQuerier) ListSessions(ctx context.Context) ([]db.Session, error) { return nil, nil }
func (m *mockQuerier) ListUserMessagesBySession(ctx context.Context, sessionID string) ([]db.Message, error) {
	return nil, nil
}
func (m *mockQuerier) RecordFileRead(ctx context.Context, arg db.RecordFileReadParams) error {
	return nil
}
func (m *mockQuerier) RecordTokenUsage(ctx context.Context, arg db.RecordTokenUsageParams) error {
	return nil
}
func (m *mockQuerier) RenameSession(ctx context.Context, arg db.RenameSessionParams) error {
	return nil
}
func (m *mockQuerier) UpdateGoalStatus(ctx context.Context, arg db.UpdateGoalStatusParams) (db.Goal, error) {
	return db.Goal{}, nil
}
func (m *mockQuerier) UpdateMessage(ctx context.Context, arg db.UpdateMessageParams) error {
	return nil
}
func (m *mockQuerier) UpdateSession(ctx context.Context, arg db.UpdateSessionParams) (db.Session, error) {
	return db.Session{}, nil
}
func (m *mockQuerier) UpdateSessionTitleAndUsage(ctx context.Context, arg db.UpdateSessionTitleAndUsageParams) error {
	return nil
}
func (m *mockQuerier) UpdateStatelessSession(ctx context.Context, arg db.UpdateStatelessSessionParams) error {
	return nil
}
func (m *mockQuerier) UpdatePinned(ctx context.Context, arg db.UpdatePinnedParams) error { return nil }
func (m *mockQuerier) ListPrunableSessions(ctx context.Context, updatedAt int64) ([]db.Session, error) {
	return nil, nil
}
func (m *mockQuerier) BulkDeleteSessions(ctx context.Context, updatedAt int64) error { return nil }

func setupTest(e *echo.Echo) *mockQuerier {
	mq := &mockQuerier{}
	server.SetDiagramsQuerier(mq)
	server.SetMermaidServiceEnabled(true, 8643)
	e.GET("/service/mermaid/render", server.HandleMermaidRender)
	return mq
}

func TestHandleMermaidRender_Basic(t *testing.T) {
	t.Parallel()

	e := echo.New()
	mq := setupTest(e)

	// Create a diagram first
	syntax := "graph TD;A[Client]-->B[Server]"
	diagram, err := mq.CreateDiagram(t.Context(), db.CreateDiagramParams{
		SessionID: "test-session",
		Syntax:    syntax,
		CreatedAt: time.Now().UnixMilli(),
	})
	require.NoError(t, err)

	// Test with valid ID
	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/service/mermaid/render?id=%d", diagram.ID), nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	require.Contains(t, body, "<pre class=\"mermaid\">graph TD;A[Client]--&gt;B[Server]</pre>")
	require.Contains(t, body, "globalThis[\"mermaid\"] =")
	require.NotContains(t, body, "var mermaid = (")

	// Test missing ID
	req = httptest.NewRequest(http.MethodGet, "/service/mermaid/render", nil)
	rec = httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)

	// Test invalid ID
	req = httptest.NewRequest(http.MethodGet, "/service/mermaid/render?id=abc", nil)
	rec = httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)

	// Test non-existent ID
	req = httptest.NewRequest(http.MethodGet, "/service/mermaid/render?id=999", nil)
	rec = httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestMermaidURL_GeneratesShortURL(t *testing.T) {
	t.Parallel()

	mq := &mockQuerier{}
	server.SetDiagramsQuerier(mq)
	server.SetMermaidServiceEnabled(true, 8643)

	syntax := "sequenceDiagram;A->B: Hello;B->A: Hi!"
	url := server.MermaidURL(syntax)

	require.NotEmpty(t, url)
	require.Contains(t, url, "/service/mermaid/render?id=")
	require.NotContains(t, url, syntax)
}
