package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"charm.land/fantasy"
	"github.com/stretchr/testify/require"
)

// configGuardViolationMsg is the substring the config-write guard returns.
// Tests pin it so the wording contract with the agent stays explicit.
const configGuardViolationMsg = "configuration file the agent may not modify"

func configGuardCtx() context.Context {
	return context.WithValue(context.Background(), SessionIDContextKey, "test-session")
}

func configGuardWorkspace(t *testing.T) string {
	t.Helper()
	ws := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(ws, ".phosphor"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(ws, "phosphor.json"), []byte("{}\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(ws, ".phosphor", "phosphor.json"), []byte("{}\n"), 0o644))
	return ws
}

func runTool(t *testing.T, tool fantasy.AgentTool, name string, params any) fantasy.ToolResponse {
	t.Helper()
	input, err := json.Marshal(params)
	require.NoError(t, err)
	resp, err := tool.Run(configGuardCtx(), fantasy.ToolCall{
		ID:    "test-call",
		Name:  name,
		Input: string(input),
	})
	require.NoError(t, err)
	return resp
}

func TestConfigGuard_WriteToolRefusesConfigFiles(t *testing.T) {
	t.Parallel()
	ws := configGuardWorkspace(t)
	tool := NewWriteTool(nil, &mockPermissionService{}, &mockHistoryService{}, mockFileTrackerService{}, ws, nil)

	for _, path := range []string{"phosphor.json", ".phosphor/phosphor.json"} {
		resp := runTool(t, tool, WriteToolName, WriteParams{FilePath: path, Content: "pwned"})
		require.True(t, resp.IsError, path)
		require.Contains(t, resp.Content, configGuardViolationMsg, path)
	}

	// Unrelated in-workspace writes keep working.
	resp := runTool(t, tool, WriteToolName, WriteParams{FilePath: "notes.txt", Content: "ok"})
	require.False(t, resp.IsError, resp.Content)
}

func TestConfigGuard_EditToolRefusesConfigFiles(t *testing.T) {
	t.Parallel()
	ws := configGuardWorkspace(t)
	tool := NewEditTool(nil, &mockPermissionService{}, &mockHistoryService{}, mockFileTrackerService{}, ws, nil)

	for _, path := range []string{"phosphor.json", ".phosphor/phosphor.json"} {
		resp := runTool(t, tool, EditToolName, EditParams{FilePath: path, OldString: "{}", NewString: "pwned"})
		require.True(t, resp.IsError, path)
		require.Contains(t, resp.Content, configGuardViolationMsg, path)
	}
}

func TestConfigGuard_MultiEditToolRefusesConfigFiles(t *testing.T) {
	t.Parallel()
	ws := configGuardWorkspace(t)
	tool := NewMultiEditTool(nil, &mockPermissionService{}, &mockHistoryService{}, mockFileTrackerService{}, ws, nil)

	for _, path := range []string{"phosphor.json", ".phosphor/phosphor.json"} {
		resp := runTool(t, tool, MultiEditToolName, MultiEditParams{
			FilePath: path,
			Edits:    []MultiEditOperation{{OldString: "{}", NewString: "pwned"}},
		})
		require.True(t, resp.IsError, path)
		require.Contains(t, resp.Content, configGuardViolationMsg, path)
	}
}

func TestConfigGuard_AppendToolRefusesConfigFiles(t *testing.T) {
	t.Parallel()
	ws := configGuardWorkspace(t)
	tool := NewAppendTool(nil, &mockPermissionService{}, &mockHistoryService{}, mockFileTrackerService{}, ws, nil)

	for _, path := range []string{"phosphor.json", ".phosphor/phosphor.json"} {
		resp := runTool(t, tool, AppendToolName, AppendParams{FilePath: path, Content: "pwned"})
		require.True(t, resp.IsError, path)
		require.Contains(t, resp.Content, configGuardViolationMsg, path)
	}
}

func TestConfigGuard_ViewToolStillReadsConfig(t *testing.T) {
	t.Parallel()
	ws := configGuardWorkspace(t)
	tool := NewViewTool(nil, &mockPermissionService{}, mockFileTrackerService{}, nil, ws, nil)

	for _, path := range []string{"phosphor.json", ".phosphor/phosphor.json"} {
		resp := runTool(t, tool, ViewToolName, ViewParams{FilePath: path})
		require.False(t, resp.IsError, path)
		require.NotContains(t, resp.Content, configGuardViolationMsg, path)
	}
}
