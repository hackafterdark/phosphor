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

// Audit finding #9: write and append skipped the write-path secret gate that
// edit and multiedit enforce, so a full-file credential write bypassed the
// accidental-commit mitigation. These tests pin that both tools now block
// detected secrets and still accept clean content.

func runWriteTool(t *testing.T, workingDir, path, content string) (fantasy.ToolResponse, error) {
	t.Helper()
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")
	tool := NewWriteTool(nil, &mockPermissionService{}, &mockHistoryService{}, mockFileTrackerService{}, workingDir)
	input, err := json.Marshal(WriteParams{FilePath: path, Content: content})
	require.NoError(t, err)
	return tool.Run(ctx, fantasy.ToolCall{ID: "test-call", Name: WriteToolName, Input: string(input)})
}

func runAppendTool(t *testing.T, workingDir, path, content string) (fantasy.ToolResponse, error) {
	t.Helper()
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")
	tool := NewAppendTool(nil, &mockPermissionService{}, &mockHistoryService{}, mockFileTrackerService{}, workingDir)
	input, err := json.Marshal(AppendParams{FilePath: path, Content: content})
	require.NoError(t, err)
	return tool.Run(ctx, fantasy.ToolCall{ID: "test-call", Name: AppendToolName, Input: string(input)})
}

func TestWriteTool_SecretGateBlocksDetectedCredential(t *testing.T) {
	t.Parallel()

	workingDir := t.TempDir()
	resp, err := runWriteTool(t, workingDir, "deploy/app.env",
		"AWS_ACCESS_KEY_ID=\""+egressDetectedKey+"\"\n")
	require.NoError(t, err)
	require.True(t, resp.IsError, "write with a detected credential must be blocked, got: %s", resp.Content)

	_, statErr := os.Stat(filepath.Join(workingDir, "deploy", "app.env"))
	require.True(t, os.IsNotExist(statErr), "the blocked write must not touch the filesystem")
}

func TestWriteTool_SecretGateAllowsCleanContent(t *testing.T) {
	t.Parallel()

	workingDir := t.TempDir()
	resp, err := runWriteTool(t, workingDir, "deploy/app.txt", "NOT_A_SECRET=plain\n")
	require.NoError(t, err)
	require.False(t, resp.IsError, "clean content must pass the gate, got: %s", resp.Content)
}

func TestAppendTool_SecretGateBlocksDetectedCredential(t *testing.T) {
	t.Parallel()

	workingDir := t.TempDir()
	resp, err := runAppendTool(t, workingDir, "deploy/app.env",
		"AWS_ACCESS_KEY_ID=\""+egressDetectedKey+"\"\n")
	require.NoError(t, err)
	require.True(t, resp.IsError, "append with a detected credential must be blocked, got: %s", resp.Content)

	_, statErr := os.Stat(filepath.Join(workingDir, "deploy", "app.env"))
	require.True(t, os.IsNotExist(statErr), "the blocked append must not touch the filesystem")
}

func TestAppendTool_SecretGateAllowsCleanContent(t *testing.T) {
	t.Parallel()

	workingDir := t.TempDir()
	resp, err := runAppendTool(t, workingDir, "deploy/app.txt", "NOT_A_SECRET=plain\n")
	require.NoError(t, err)
	require.False(t, resp.IsError, "clean content must pass the gate, got: %s", resp.Content)
}