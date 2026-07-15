package tools

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"charm.land/fantasy"
	"github.com/hackafterdark/phosphor/pkg/config"
	"github.com/hackafterdark/phosphor/pkg/permission"
	"github.com/hackafterdark/phosphor/pkg/pubsub"
	"github.com/stretchr/testify/require"
)

// TestCompositionalChains_PermissionRequiredForChains verifies that chained
// commands (non-safe-read-only) require user permission, preventing the
// agent from executing arbitrary write/modification chains without approval.
func TestCompositionalChains_PermissionRequiredForChains(t *testing.T) {
	t.Parallel()

	workingDir := t.TempDir()
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")

	perms := &recordingPermissionService{
		Broker: pubsub.NewBroker[permission.PermissionRequest](),
		allow:  false,
	}
	attribution := &config.Attribution{TrailerStyle: config.TrailerStyleNone}
	tool := NewBashTool(perms, workingDir, config.ToolBash{}, attribution, "test-model")

	// Chained command should trigger permission request.
	resp := runBashToolWithPerms(t, tool, ctx, BashParams{
		Command:     "ls && echo done",
		Description: "chain requires permission",
	})

	require.True(t, resp.IsError, "chained command should require permission")
	require.Contains(t, resp.Content, "User denied permission",
		"denied chain should return permission error, got: %s", resp.Content)
}

// TestCompositionalChains_PathTraversalInChain verifies that path traversal
// attacks cannot be hidden inside chained commands. The validateCommandPaths
// function checks all I/O commands in the chain for out-of-workspace paths.
func TestCompositionalChains_PathTraversalInChain(t *testing.T) {
	t.Parallel()

	workingDir := t.TempDir()
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")

	tool := newBashToolForTest(workingDir)

	// Commands with path traversal in chain arguments should be blocked.
	chains := []struct {
		name    string
		command string
	}{
		{"cat outside && echo", "cat ../etc/passwd && echo done"},
		{"grep outside && cat", "grep secret ../etc/shadow && cat file.txt"},
	}

	for _, tt := range chains {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			resp := runBashTool(t, tool, ctx, BashParams{
				Command:     tt.command,
				Description: tt.name,
			})
			require.True(t, resp.IsError, "chain with path traversal %q should be blocked", tt.name)
			require.Contains(t, resp.Content, "Security violation",
				"chain with path traversal %q should return security error, got: %s", tt.name, resp.Content)
		})
	}
}

// TestCompositionalChains_WorkingDirEscapeInChain verifies that working_dir
// escapes are blocked even when chained with benign commands.
func TestCompositionalChains_WorkingDirEscapeInChain(t *testing.T) {
	t.Parallel()

	workingDir := t.TempDir()
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")

	tool := newBashToolForTest(workingDir)

	resp := runBashTool(t, tool, ctx, BashParams{
		Command:     "echo hello && ls",
		WorkingDir:  os.TempDir(),
		Description: "chain with escape",
	})

	require.True(t, resp.IsError, "chained command with out-of-workspace working_dir should be blocked")
	require.Contains(t, resp.Content, "Security violation",
		"chain with escape should return security error, got: %s", resp.Content)
}

// TestCompositionalChains_ChainTriggersPermission verifies that chains
// always trigger permission requests, even if the first command is safe.
func TestCompositionalChains_ChainTriggersPermission(t *testing.T) {
	t.Parallel()

	workingDir := t.TempDir()
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")

	perms := &recordingPermissionService{
		Broker: pubsub.NewBroker[permission.PermissionRequest](),
		allow:  true,
	}
	attribution := &config.Attribution{TrailerStyle: config.TrailerStyleNone}
	tool := NewBashTool(perms, workingDir, config.ToolBash{}, attribution, "test-model")

	// Plain ls should NOT trigger permission check.
	resp := runBashToolWithPerms(t, tool, ctx, BashParams{
		Command:     "ls",
		Description: "plain ls",
	})
	require.False(t, resp.IsError, "plain ls should not require permission")
	require.Equal(t, 0, perms.requestCount, "plain ls should not trigger permission request")

	// Chained ls && echo should trigger permission check.
	perms.requestCount = 0
	resp = runBashToolWithPerms(t, tool, ctx, BashParams{
		Command:     "ls && echo done",
		Description: "chained ls",
	})
	require.False(t, resp.IsError, "chained ls should execute with permission")
	require.Equal(t, 1, perms.requestCount, "chained command should trigger permission request")
}

// --- Helper functions ---

func runBashToolWithPerms(t *testing.T, tool fantasy.AgentTool, ctx context.Context, params BashParams) fantasy.ToolResponse {
	t.Helper()

	input, err := json.Marshal(params)
	require.NoError(t, err)

	call := fantasy.ToolCall{
		ID:    "test-call",
		Name:  BashToolName,
		Input: string(input),
	}

	resp, err := tool.Run(ctx, call)
	require.NoError(t, err)
	return resp
}
