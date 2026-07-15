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

func TestAppendToolAppendsToExistingFile(t *testing.T) {
	t.Parallel()

	workingDir := t.TempDir()
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")

	// Create a file with initial content
	initialContent := "line 1\nline 2\n"
	err := os.WriteFile(filepath.Join(workingDir, "test.txt"), []byte(initialContent), 0o644)
	require.NoError(t, err)

	tool := NewAppendTool(nil, &mockPermissionService{}, &mockHistoryService{}, mockFileTrackerService{}, workingDir)

	input, err := json.Marshal(AppendParams{FilePath: "test.txt", Content: "line 3\n"})
	require.NoError(t, err)

	resp, err := tool.Run(ctx, fantasy.ToolCall{
		ID:    "test-call",
		Name:  AppendToolName,
		Input: string(input),
	})
	require.NoError(t, err)
	require.False(t, resp.IsError)

	b, err := os.ReadFile(filepath.Join(workingDir, "test.txt"))
	require.NoError(t, err)
	require.Equal(t, "line 1\nline 2\nline 3\n", string(b))
}

func TestAppendToolCreatesNewFile(t *testing.T) {
	t.Parallel()

	workingDir := t.TempDir()
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")

	tool := NewAppendTool(nil, &mockPermissionService{}, &mockHistoryService{}, mockFileTrackerService{}, workingDir)

	input, err := json.Marshal(AppendParams{FilePath: "new.txt", Content: "hello world\n"})
	require.NoError(t, err)

	resp, err := tool.Run(ctx, fantasy.ToolCall{
		ID:    "test-call",
		Name:  AppendToolName,
		Input: string(input),
	})
	require.NoError(t, err)
	require.False(t, resp.IsError)

	b, err := os.ReadFile(filepath.Join(workingDir, "new.txt"))
	require.NoError(t, err)
	require.Equal(t, "hello world\n", string(b))
}

func TestAppendToolRequiresFilePath(t *testing.T) {
	t.Parallel()

	workingDir := t.TempDir()
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")

	tool := NewAppendTool(nil, &mockPermissionService{}, &mockHistoryService{}, mockFileTrackerService{}, workingDir)

	input, err := json.Marshal(AppendParams{FilePath: "", Content: "content"})
	require.NoError(t, err)

	resp, err := tool.Run(ctx, fantasy.ToolCall{
		ID:    "test-call",
		Name:  AppendToolName,
		Input: string(input),
	})
	require.NoError(t, err)
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "file_path is required")
}

func TestAppendToolRequiresContent(t *testing.T) {
	t.Parallel()

	workingDir := t.TempDir()
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")

	tool := NewAppendTool(nil, &mockPermissionService{}, &mockHistoryService{}, mockFileTrackerService{}, workingDir)

	input, err := json.Marshal(AppendParams{FilePath: "test.txt", Content: ""})
	require.NoError(t, err)

	resp, err := tool.Run(ctx, fantasy.ToolCall{
		ID:    "test-call",
		Name:  AppendToolName,
		Input: string(input),
	})
	require.NoError(t, err)
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "content is required")
}

func TestAppendToolRefusesDirectory(t *testing.T) {
	t.Parallel()

	workingDir := t.TempDir()
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")

	// Create a directory
	err := os.MkdirAll(filepath.Join(workingDir, "testdir"), 0o755)
	require.NoError(t, err)

	tool := NewAppendTool(nil, &mockPermissionService{}, &mockHistoryService{}, mockFileTrackerService{}, workingDir)

	input, err := json.Marshal(AppendParams{FilePath: "testdir", Content: "content"})
	require.NoError(t, err)

	resp, err := tool.Run(ctx, fantasy.ToolCall{
		ID:    "test-call",
		Name:  AppendToolName,
		Input: string(input),
	})
	require.NoError(t, err)
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "directory, not a file")
}

func TestAppendToolMultipleAppends(t *testing.T) {
	t.Parallel()

	workingDir := t.TempDir()
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")

	tool := NewAppendTool(nil, &mockPermissionService{}, &mockHistoryService{}, mockFileTrackerService{}, workingDir)

	// First append
	input1, err := json.Marshal(AppendParams{FilePath: "multi.txt", Content: "first\n"})
	require.NoError(t, err)
	resp1, err := tool.Run(ctx, fantasy.ToolCall{
		ID:    "test-call-1",
		Name:  AppendToolName,
		Input: string(input1),
	})
	require.NoError(t, err)
	require.False(t, resp1.IsError)

	// Second append
	input2, err := json.Marshal(AppendParams{FilePath: "multi.txt", Content: "second\n"})
	require.NoError(t, err)
	resp2, err := tool.Run(ctx, fantasy.ToolCall{
		ID:    "test-call-2",
		Name:  AppendToolName,
		Input: string(input2),
	})
	require.NoError(t, err)
	require.False(t, resp2.IsError)

	// Third append
	input3, err := json.Marshal(AppendParams{FilePath: "multi.txt", Content: "third"})
	require.NoError(t, err)
	resp3, err := tool.Run(ctx, fantasy.ToolCall{
		ID:    "test-call-3",
		Name:  AppendToolName,
		Input: string(input3),
	})
	require.NoError(t, err)
	require.False(t, resp3.IsError)

	b, err := os.ReadFile(filepath.Join(workingDir, "multi.txt"))
	require.NoError(t, err)
	require.Equal(t, "first\nsecond\nthird", string(b))
}

func TestAppendToolCreatesParentDirs(t *testing.T) {
	t.Parallel()

	workingDir := t.TempDir()
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")

	tool := NewAppendTool(nil, &mockPermissionService{}, &mockHistoryService{}, mockFileTrackerService{}, workingDir)

	input, err := json.Marshal(AppendParams{FilePath: "nested/deep/file.txt", Content: "content"})
	require.NoError(t, err)

	resp, err := tool.Run(ctx, fantasy.ToolCall{
		ID:    "test-call",
		Name:  AppendToolName,
		Input: string(input),
	})
	require.NoError(t, err)
	require.False(t, resp.IsError)

	b, err := os.ReadFile(filepath.Join(workingDir, "nested/deep/file.txt"))
	require.NoError(t, err)
	require.Equal(t, "content", string(b))
}
