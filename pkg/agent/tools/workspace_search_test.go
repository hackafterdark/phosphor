package tools

import (
	"context"
	"testing"

	"charm.land/fantasy"
)

func TestWorkspaceSearchTool(t *testing.T) {
	t.Parallel()
	tool := NewWorkspaceSearchTool(t.TempDir())
	info := tool.Info()
	if info.Name != WorkspaceSearchToolName {
		t.Errorf("expected name %s, got %s", WorkspaceSearchToolName, info.Name)
	}
}

func TestWorkspaceSearchParams(t *testing.T) {
	t.Parallel()
	params := WorkspaceSearchParams{
		Query: "test",
		Table: "all",
		Limit: 10,
	}
	if params.Query != "test" {
		t.Errorf("expected query 'test', got %s", params.Query)
	}
	if params.Limit != 10 {
		t.Errorf("expected limit 10, got %d", params.Limit)
	}
}

func TestWorkspaceSearchDescription(t *testing.T) {
	t.Parallel()
	desc := workspaceSearchDescription()
	if desc == "" {
		t.Error("expected non-empty description")
	}
}

func TestWorkspaceSearchNoQuery(t *testing.T) {
	t.Parallel()
	tool := NewWorkspaceSearchTool(t.TempDir())
	resp, err := tool.Run(context.Background(), fantasy.ToolCall{ID: "test"})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if !resp.IsError {
		t.Error("expected error response when query is empty")
	}
}