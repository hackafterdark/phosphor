package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"charm.land/fantasy"
	"github.com/hackafterdark/phosphor/internal/config"
	"github.com/hackafterdark/phosphor/internal/permission"
	"github.com/hackafterdark/phosphor/internal/pubsub"
	"github.com/stretchr/testify/require"
)

// securityViolationMsg is the standard error message returned when a tool
// detects an out-of-workspace path. All workspace hardening tests assert on
// this substring to catch regression in the exact wording.
const securityViolationMsg = "Security violation: path"

func TestWorkspaceHardening_WriteToolBlocksOutsidePaths(t *testing.T) {
	t.Parallel()

	workingDir := t.TempDir()
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")

	tool := NewWriteTool(nil, &mockPermissionService{}, &mockHistoryService{}, mockFileTrackerService{}, workingDir)

	tests := []struct {
		name string
		path string
	}{
		{"parent directory escape", "../outside.txt"},
		{"absolute path outside workspace", filepath.Join(os.TempDir(), "evil.txt")},
		{"deep parent escape", "../../etc/passwd"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			input, err := json.Marshal(WriteParams{FilePath: tt.path, Content: "pwned"})
			require.NoError(t, err)

			resp, err := tool.Run(ctx, fantasy.ToolCall{
				ID:    "test-call",
				Name:  WriteToolName,
				Input: string(input),
			})
			require.NoError(t, err)
			require.True(t, resp.IsError, "write should block %s", tt.name)
			require.Contains(t, resp.Content, securityViolationMsg,
				"write should return security violation for %s, got: %s", tt.name, resp.Content)
		})
	}
}

func TestWorkspaceHardening_EditToolBlocksOutsidePaths(t *testing.T) {
	t.Parallel()

	workingDir := t.TempDir()
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")

	tool := NewEditTool(nil, &mockPermissionService{}, &mockHistoryService{}, mockFileTrackerService{}, workingDir)

	tests := []struct {
		name string
		path string
	}{
		{"parent directory escape", "../outside.txt"},
		{"absolute path outside workspace", filepath.Join(os.TempDir(), "evil.txt")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			input, err := json.Marshal(EditParams{
				FilePath:  tt.path,
				OldString: "",
				NewString: "pwned",
			})
			require.NoError(t, err)

			resp, err := tool.Run(ctx, fantasy.ToolCall{
				ID:    "test-call",
				Name:  EditToolName,
				Input: string(input),
			})
			require.NoError(t, err)
			require.True(t, resp.IsError, "edit should block %s", tt.name)
			require.Contains(t, resp.Content, securityViolationMsg,
				"edit should return security violation for %s, got: %s", tt.name, resp.Content)
		})
	}
}

func TestWorkspaceHardening_MultiEditToolBlocksOutsidePaths(t *testing.T) {
	t.Parallel()

	workingDir := t.TempDir()
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")

	tool := NewMultiEditTool(nil, &mockPermissionService{}, &mockHistoryService{}, mockFileTrackerService{}, workingDir)

	tests := []struct {
		name string
		path string
	}{
		{"parent directory escape", "../outside.txt"},
		{"absolute path outside workspace", filepath.Join(os.TempDir(), "evil.txt")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			input, err := json.Marshal(MultiEditParams{
				FilePath: tt.path,
				Edits: []MultiEditOperation{{OldString: "", NewString: "pwned"}},
			})
			require.NoError(t, err)

			resp, err := tool.Run(ctx, fantasy.ToolCall{
				ID:    "test-call",
				Name:  MultiEditToolName,
				Input: string(input),
			})
			require.NoError(t, err)
			require.True(t, resp.IsError, "multiedit should block %s", tt.name)
			require.Contains(t, resp.Content, securityViolationMsg,
				"multiedit should return security violation for %s, got: %s", tt.name, resp.Content)
		})
	}
}

func TestWorkspaceHardening_AppendToolBlocksOutsidePaths(t *testing.T) {
	t.Parallel()

	workingDir := t.TempDir()
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")

	tool := NewAppendTool(nil, &mockPermissionService{}, &mockHistoryService{}, mockFileTrackerService{}, workingDir)

	tests := []struct {
		name string
		path string
	}{
		{"parent directory escape", "../outside.txt"},
		{"absolute path outside workspace", filepath.Join(os.TempDir(), "evil.txt")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			input, err := json.Marshal(AppendParams{FilePath: tt.path, Content: "pwned"})
			require.NoError(t, err)

			resp, err := tool.Run(ctx, fantasy.ToolCall{
				ID:    "test-call",
				Name:  AppendToolName,
				Input: string(input),
			})
			require.NoError(t, err)
			require.True(t, resp.IsError, "append should block %s", tt.name)
			require.Contains(t, resp.Content, securityViolationMsg,
				"append should return security violation for %s, got: %s", tt.name, resp.Content)
		})
	}
}

func TestWorkspaceHardening_ViewToolBlocksOutsidePaths(t *testing.T) {
	t.Parallel()

	workingDir := t.TempDir()
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")

	tool := newViewToolForTest(workingDir)

	tests := []struct {
		name string
		path string
	}{
		{"parent directory escape", "../etc/passwd"},
		{"absolute path outside workspace", filepath.Join(os.TempDir(), "sensitive.txt")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			resp := runViewTool(t, tool, ctx, ViewParams{FilePath: tt.path})
			require.True(t, resp.IsError, "view should block %s", tt.name)
			require.Contains(t, resp.Content, securityViolationMsg,
				"view should return security violation for %s, got: %s", tt.name, resp.Content)
		})
	}
}

func TestWorkspaceHardening_DownloadToolBlocksOutsidePaths(t *testing.T) {
	t.Parallel()

	workingDir := t.TempDir()
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")

	tool := NewDownloadTool(nil, workingDir, nil)

	tests := []struct {
		name string
		path string
	}{
		{"parent directory escape", "../outside.txt"},
		{"absolute path outside workspace", filepath.Join(os.TempDir(), "evil.txt")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			input, err := json.Marshal(DownloadParams{
				URL:      "https://example.com/malware",
				FilePath: tt.path,
			})
			require.NoError(t, err)

			resp, err := tool.Run(ctx, fantasy.ToolCall{
				ID:    "test-call",
				Name:  DownloadToolName,
				Input: string(input),
			})
			require.NoError(t, err)
			require.True(t, resp.IsError, "download should block %s", tt.name)
			require.Contains(t, resp.Content, securityViolationMsg,
				"download should return security violation for %s, got: %s", tt.name, resp.Content)
		})
	}
}

func TestWorkspaceHardening_LsToolBlocksOutsidePaths(t *testing.T) {
	t.Parallel()

	workingDir := t.TempDir()
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")

	perms := &mockPermissionService{Broker: pubsub.NewBroker[permission.PermissionRequest]()}
	tool := NewLsTool(perms, workingDir, config.ToolLs{})

	tests := []struct {
		name string
		path string
	}{
		{"parent directory escape", "../etc"},
		{"absolute path outside workspace", os.TempDir()},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			input, err := json.Marshal(LSParams{Path: tt.path})
			require.NoError(t, err)

			resp, err := tool.Run(ctx, fantasy.ToolCall{
				ID:    "test-call",
				Name:  LSToolName,
				Input: string(input),
			})
			require.NoError(t, err)
			require.True(t, resp.IsError, "ls should block %s", tt.name)
			require.Contains(t, resp.Content, securityViolationMsg,
				"ls should return security violation for %s, got: %s", tt.name, resp.Content)
		})
	}
}

func TestWorkspaceHardening_GlobToolBlocksOutsidePaths(t *testing.T) {
	t.Parallel()

	workingDir := t.TempDir()
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")

	tool := NewGlobTool(workingDir)

	tests := []struct {
		name string
		path    string
		pattern string
	}{
		{"parent directory escape", "../etc", "*.conf"},
		{"absolute path outside workspace", os.TempDir(), "*"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			input, err := json.Marshal(GlobParams{Path: tt.path, Pattern: tt.pattern})
			require.NoError(t, err)

			resp, err := tool.Run(ctx, fantasy.ToolCall{
				ID:    "test-call",
				Name:  GlobToolName,
				Input: string(input),
			})
			require.NoError(t, err)
			require.True(t, resp.IsError, "glob should block %s", tt.name)
			require.Contains(t, resp.Content, securityViolationMsg,
				"glob should return security violation for %s, got: %s", tt.name, resp.Content)
		})
	}
}

func TestWorkspaceHardening_GrepToolBlocksOutsidePaths(t *testing.T) {
	t.Parallel()

	workingDir := t.TempDir()
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")

	tool := NewGrepTool(workingDir, config.ToolGrep{})

	tests := []struct {
		name string
		path  string
	}{
		{"parent directory escape", "../etc"},
		{"absolute path outside workspace", os.TempDir()},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			input, err := json.Marshal(GrepParams{Path: tt.path, Pattern: "secret"})
			require.NoError(t, err)

			resp, err := tool.Run(ctx, fantasy.ToolCall{
				ID:    "test-call",
				Name:  GrepToolName,
				Input: string(input),
			})
			require.NoError(t, err)
			require.True(t, resp.IsError, "grep should block %s", tt.name)
			require.Contains(t, resp.Content, securityViolationMsg,
				"grep should return security violation for %s, got: %s", tt.name, resp.Content)
		})
	}
}

func TestWorkspaceHardening_StructuralSearchToolBlocksOutsidePaths(t *testing.T) {
	t.Parallel()

	workingDir := t.TempDir()
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")

	tool := NewStructuralSearchTool(workingDir)

	tests := []struct {
		name string
		path  string
	}{
		{"parent directory escape", "../etc"},
		{"absolute path outside workspace", os.TempDir()},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			input, err := json.Marshal(StructuralSearchParams{Path: tt.path, TemplateName: "find_functions"})
			require.NoError(t, err)

			resp, err := tool.Run(ctx, fantasy.ToolCall{
				ID:    "test-call",
				Name:  StructuralSearchToolName,
				Input: string(input),
			})
			require.NoError(t, err)
			require.True(t, resp.IsError, "structural_search should block %s", tt.name)
			require.Contains(t, resp.Content, securityViolationMsg,
				"structural_search should return security violation for %s, got: %s", tt.name, resp.Content)
		})
	}
}

func TestWorkspaceHardening_BashWorkingDirBlocksOutsidePaths(t *testing.T) {
	t.Parallel()

	workingDir := t.TempDir()
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")

	tool := newBashToolForTest(workingDir)

	// Attempt to run a command with working_dir pointing outside the workspace.
	resp := runBashTool(t, tool, ctx, BashParams{
		Command:     "echo hello",
		WorkingDir:  os.TempDir(),
		Description: "escape via working_dir",
	})

	require.True(t, resp.IsError, "bash with out-of-workspace working_dir should be blocked")
	require.Contains(t, resp.Content, "Security violation",
		"bash should return security violation for out-of-workspace working_dir, got: %s", resp.Content)
}

func TestWorkspaceHardening_AllToolsAllowInWorkspace(t *testing.T) {
	t.Parallel()

	workingDir := t.TempDir()
	// Create a file inside the workspace that all tools can reference.
	testFile := filepath.Join(workingDir, "test.txt")
	require.NoError(t, os.WriteFile(testFile, []byte("hello"), 0o644))

	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")

	t.Run("write", func(t *testing.T) {
		t.Parallel()
		tool := NewWriteTool(nil, &mockPermissionService{}, &mockHistoryService{}, mockFileTrackerService{}, workingDir)
		input, err := json.Marshal(WriteParams{FilePath: "safe.txt", Content: "safe"})
		require.NoError(t, err)
		resp, err := tool.Run(ctx, fantasy.ToolCall{ID: "t", Name: WriteToolName, Input: string(input)})
		require.NoError(t, err)
		require.False(t, resp.IsError, "write should allow in-workspace path")
	})

	t.Run("edit", func(t *testing.T) {
		t.Parallel()
		tool := NewEditTool(nil, &mockPermissionService{}, &mockHistoryService{}, mockFileTrackerService{}, workingDir)
		input, err := json.Marshal(EditParams{FilePath: "test.txt", OldString: "hello", NewString: "world"})
		require.NoError(t, err)
		resp, err := tool.Run(ctx, fantasy.ToolCall{ID: "t", Name: EditToolName, Input: string(input)})
		require.NoError(t, err)
		require.False(t, resp.IsError, "edit should allow in-workspace path")
	})

	t.Run("view", func(t *testing.T) {
		t.Parallel()
		tool := newViewToolForTest(workingDir)
		resp := runViewTool(t, tool, ctx, ViewParams{FilePath: "test.txt"})
		require.False(t, resp.IsError, "view should allow in-workspace path")
	})

	t.Run("ls", func(t *testing.T) {
		t.Parallel()
		perms := &mockPermissionService{Broker: pubsub.NewBroker[permission.PermissionRequest]()}
		tool := NewLsTool(perms, workingDir, config.ToolLs{})
		input, err := json.Marshal(LSParams{Path: "."})
		require.NoError(t, err)
		resp, err := tool.Run(ctx, fantasy.ToolCall{ID: "t", Name: LSToolName, Input: string(input)})
		require.NoError(t, err)
		require.False(t, resp.IsError, "ls should allow in-workspace path")
	})

	t.Run("glob", func(t *testing.T) {
		t.Parallel()
		tool := NewGlobTool(workingDir)
		input, err := json.Marshal(GlobParams{Pattern: "*.txt"})
		require.NoError(t, err)
		resp, err := tool.Run(ctx, fantasy.ToolCall{ID: "t", Name: GlobToolName, Input: string(input)})
		require.NoError(t, err)
		require.False(t, resp.IsError, "glob should allow in-workspace path")
	})

	t.Run("grep", func(t *testing.T) {
		t.Parallel()
		tool := NewGrepTool(workingDir, config.ToolGrep{})
		input, err := json.Marshal(GrepParams{Pattern: "hello"})
		require.NoError(t, err)
		resp, err := tool.Run(ctx, fantasy.ToolCall{ID: "t", Name: GrepToolName, Input: string(input)})
		require.NoError(t, err)
		require.False(t, resp.IsError, "grep should allow in-workspace path")
	})

	t.Run("bash", func(t *testing.T) {
		t.Parallel()
		tool := newBashToolForTest(workingDir)
		resp := runBashTool(t, tool, ctx, BashParams{Command: "echo hello"})
		require.False(t, resp.IsError, "bash should allow in-workspace path")
	})
}
