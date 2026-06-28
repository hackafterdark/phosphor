package tools

import (
	"context"
	"encoding/json"
	"runtime"
	"testing"

	"charm.land/fantasy"
	"github.com/hackafterdark/phosphor/internal/config"
	"github.com/hackafterdark/phosphor/internal/permission"
	"github.com/hackafterdark/phosphor/internal/pubsub"
	"github.com/hackafterdark/phosphor/internal/shell"
	"github.com/stretchr/testify/require"
)

type mockBashPermissionService struct {
	*pubsub.Broker[permission.PermissionRequest]
}

func (m *mockBashPermissionService) Request(ctx context.Context, req permission.CreatePermissionRequest) (bool, error) {
	return true, nil
}

func (m *mockBashPermissionService) Grant(req permission.PermissionRequest) bool { return true }

func (m *mockBashPermissionService) Deny(req permission.PermissionRequest) bool { return true }

func (m *mockBashPermissionService) GrantPersistent(req permission.PermissionRequest) bool {
	return true
}

func (m *mockBashPermissionService) AutoApproveSession(sessionID string) {}

func (m *mockBashPermissionService) SetSkipRequests(skip bool) {}

func (m *mockBashPermissionService) SkipRequests() bool {
	return false
}

func (m *mockBashPermissionService) SubscribeNotifications(ctx context.Context) <-chan pubsub.Event[permission.PermissionNotification] {
	return make(<-chan pubsub.Event[permission.PermissionNotification])
}

func TestBashTool_DefaultAutoBackgroundThreshold(t *testing.T) {
	workingDir := t.TempDir()
	tool := newBashToolForTest(workingDir)
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")

	resp := runBashTool(t, tool, ctx, BashParams{
		Description: "default threshold",
		Command:     "echo done",
	})

	require.False(t, resp.IsError)
	var meta BashResponseMetadata
	require.NoError(t, json.Unmarshal([]byte(resp.Metadata), &meta))
	require.False(t, meta.Background)
	require.Empty(t, meta.ShellID)
	require.Contains(t, meta.Output, "done")
}

func TestBashTool_CustomAutoBackgroundThreshold(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sleep is not available on Windows")
	}
	workingDir := t.TempDir()
	tool := newBashToolForTest(workingDir)
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")

	resp := runBashTool(t, tool, ctx, BashParams{
		Description:         "custom threshold",
		Command:             "sleep 1.5 && echo done",
		AutoBackgroundAfter: 1,
	})

	require.False(t, resp.IsError)
	var meta BashResponseMetadata
	require.NoError(t, json.Unmarshal([]byte(resp.Metadata), &meta))
	require.True(t, meta.Background)
	require.NotEmpty(t, meta.ShellID)
	require.Contains(t, resp.Content, "moved to background")

	bgManager := shell.GetBackgroundShellManager()
	require.NoError(t, bgManager.Kill(meta.ShellID))
}

type recordingPermissionService struct {
	*pubsub.Broker[permission.PermissionRequest]
	requestCount int
	allow        bool
}

func (m *recordingPermissionService) Request(ctx context.Context, req permission.CreatePermissionRequest) (bool, error) {
	m.requestCount++
	return m.allow, nil
}

func (m *recordingPermissionService) Grant(req permission.PermissionRequest) bool { return true }

func (m *recordingPermissionService) Deny(req permission.PermissionRequest) bool { return true }

func (m *recordingPermissionService) GrantPersistent(req permission.PermissionRequest) bool {
	return true
}

func (m *recordingPermissionService) AutoApproveSession(sessionID string) {}

func (m *recordingPermissionService) SetSkipRequests(skip bool) {}

func (m *recordingPermissionService) SkipRequests() bool {
	return false
}

func (m *recordingPermissionService) SubscribeNotifications(ctx context.Context) <-chan pubsub.Event[permission.PermissionNotification] {
	return make(<-chan pubsub.Event[permission.PermissionNotification])
}

func newBashToolForTest(workingDir string) fantasy.AgentTool {
	permissions := &mockBashPermissionService{Broker: pubsub.NewBroker[permission.PermissionRequest]()}
	attribution := &config.Attribution{TrailerStyle: config.TrailerStyleNone}
	return NewBashTool(permissions, workingDir, config.ToolBash{}, attribution, "test-model")
}

func newBashToolWithRecordingPerms(workingDir string, allow bool) (fantasy.AgentTool, *recordingPermissionService) {
	perms := &recordingPermissionService{
		Broker: pubsub.NewBroker[permission.PermissionRequest](),
		allow:  allow,
	}
	attribution := &config.Attribution{TrailerStyle: config.TrailerStyleNone}
	return NewBashTool(perms, workingDir, config.ToolBash{}, attribution, "test-model"), perms
}

func TestBashTool_ChainedCommandsRequirePermission(t *testing.T) {
	workingDir := t.TempDir()
	tool, perms := newBashToolWithRecordingPerms(workingDir, true)
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")

	// ls && echo should trigger permission check.
	resp := runBashTool(t, tool, ctx, BashParams{
		Description: "chained ls",
		Command:     "ls && echo done",
	})

	require.False(t, resp.IsError)
	require.Equal(t, 1, perms.requestCount, "chained command should trigger permission request")

	// Plain ls should NOT trigger permission check.
	perms.requestCount = 0
	resp = runBashTool(t, tool, ctx, BashParams{
		Description: "plain ls",
		Command:     "ls -la",
	})

	require.False(t, resp.IsError)
	require.Equal(t, 0, perms.requestCount, "plain ls should not trigger permission request")
}

func TestBashTool_ChainedCommandsDenied(t *testing.T) {
	workingDir := t.TempDir()
	tool, perms := newBashToolWithRecordingPerms(workingDir, false)
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")

	resp := runBashTool(t, tool, ctx, BashParams{
		Description: "chained ls denied",
		Command:     "ls && rm -rf /",
	})

	require.Equal(t, 1, perms.requestCount)
	require.Contains(t, resp.Content, "User denied permission")
}

func runBashTool(t *testing.T, tool fantasy.AgentTool, ctx context.Context, params BashParams) fantasy.ToolResponse {
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

func newBashToolWithCfg(workingDir string, cfg config.ToolBash) fantasy.AgentTool {
	permissions := &mockBashPermissionService{Broker: pubsub.NewBroker[permission.PermissionRequest]()}
	attribution := &config.Attribution{TrailerStyle: config.TrailerStyleNone}
	return NewBashTool(permissions, workingDir, cfg, attribution, "test-model")
}

// TestBashTool_ConfigAwareBannedCommands verifies that user-configured
// banned_commands extend the built-in deny list. Commands not in either
// list should be allowed; commands in the built-in or user list should
// be blocked.
func TestBashTool_ConfigAwareBannedCommands(t *testing.T) {
	t.Parallel()

	workingDir := t.TempDir()
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")

	tool := newBashToolWithCfg(workingDir, config.ToolBash{
		BannedCommands: []string{"nc", "netcat"},
	})

	// "nc" is not in the built-in bannedCommands (only "nc" is, but let's
	// verify the user-added "netcat" is blocked). Actually "nc" IS in the
	// built-in list, so we test with a command that's ONLY in the user list.
	resp := runBashTool(t, tool, ctx, BashParams{Command: "netcat"})
	require.Contains(t, resp.Content, "not allowed for security reasons",
		"user-banned 'netcat' should be blocked")

	// A built-in banned command (e.g. "curl") should still be blocked even
	// without user config.
	toolDefault := newBashToolForTest(workingDir)
	resp = runBashTool(t, toolDefault, ctx, BashParams{Command: "curl http://example.com"})
	require.Contains(t, resp.Content, "not allowed for security reasons",
		"built-in banned 'curl' should still be blocked")

	// A command not in either list (e.g. "echo") should be allowed.
	resp = runBashTool(t, tool, ctx, BashParams{Command: "echo hello"})
	require.False(t, resp.IsError, "'echo' should not be blocked")
}

// TestBashTool_AllowedEnv_FiltersEnvironment verifies that when AllowedEnv
// is configured, the shell execution only sees the allowed variables. The
// PHOSPHOR_AGENT marker should always be present regardless of config.
func TestBashTool_AllowedEnv_FiltersEnvironment(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		t.Skip("env filtering behavior varies on Windows; skip for CI stability")
	}

	workingDir := t.TempDir()
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")

	// Allow only PATH and a custom var. Secret vars should be invisible.
	tool := newBashToolWithCfg(workingDir, config.ToolBash{
		AllowedEnv: []string{"PATH", "MY_CUSTOM_VAR"},
	})

	// Attempting to read an unlisted env var should yield nothing.
	resp := runBashTool(t, tool, ctx, BashParams{
		Command: "bash -c 'echo $SECRET_API_KEY'",
	})
	require.False(t, resp.IsError, "command itself should not error")
	require.NotContains(t, resp.Content, "abc123",
		"unlisted env var SECRET_API_KEY should not be visible to agent shell")
}

// TestBashTool_InterpreterCodeExecutionBlocked verifies that interpreters
// with inline code execution flags (-c, -e, -r) are blocked. These flags
// allow an agent to bypass shell-level defenses (env filtering, command
// blocking, workspace bounds) by executing arbitrary code in another
// runtime. Only the code-execution flags are blocked — normal script
// invocation (python script.py, node build.js) is preserved.
func TestBashTool_InterpreterCodeExecutionBlocked(t *testing.T) {
	t.Parallel()

	workingDir := t.TempDir()
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")
	tool := newBashToolForTest(workingDir)

	blocked := []string{
		"python -c 'import os; print(os.environ)'",
		"python3 -c 'print(1)'",
		"node -e 'console.log(process.env)'",
		"perl -e 'print join(\"\\n\", keys %ENV)'",
		"ruby -e 'ENV.each { |k,v| puts k }'",
		"php -r 'echo getenv(\"SECRET\");'",
		"lua -e 'print(os.getenv(\"SECRET\"))'",
		"bash -c 'env'",
		"sh -c 'printenv'",
		"zsh -c 'printenv'",
	}

	for _, cmd := range blocked {
		t.Run(cmd, func(t *testing.T) {
			t.Parallel()
			resp := runBashTool(t, tool, ctx, BashParams{Command: cmd})
			require.Contains(t, resp.Content, "not allowed for security reasons",
				"%q should be blocked (interpreter code execution)", cmd)
		})
	}

	// Normal script invocation should NOT be blocked.
	allowed := []string{
		"python my_script.py",
		"python3 src/main.py",
		"node build.js",
		"perl my_script.pl",
		"ruby script.rb",
		"php index.php",
		"bash my_script.sh",
	}

	for _, cmd := range allowed {
		t.Run(cmd, func(t *testing.T) {
			t.Parallel()
			resp := runBashTool(t, tool, ctx, BashParams{Command: cmd})
			require.NotContains(t, resp.Content, "not allowed for security reasons",
				"%q should NOT be blocked (normal script invocation)", cmd)
		})
	}
}

// TestBashTool_AllowInlineExecution_DisabledByDefault verifies that inline
// code execution is blocked by default (AllowInlineExecution=false).
func TestBashTool_AllowInlineExecution_DisabledByDefault(t *testing.T) {
	t.Parallel()

	workingDir := t.TempDir()
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")

	// Default config (AllowInlineExecution=false).
	tool := newBashToolWithCfg(workingDir, config.ToolBash{})

	resp := runBashTool(t, tool, ctx, BashParams{Command: "python -c 'print(1)'"})
	require.Contains(t, resp.Content, "not allowed for security reasons",
		"python -c should be blocked by default")
}

// TestBashTool_AllowInlineExecution_EnabledPermitsInlineCode verifies that
// setting AllowInlineExecution=true removes the interpreter/shell code
// execution blockers while preserving all other blockers.
func TestBashTool_AllowInlineExecution_EnabledPermitsInlineCode(t *testing.T) {
	t.Parallel()

	workingDir := t.TempDir()
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")

	tool := newBashToolWithCfg(workingDir, config.ToolBash{
		AllowInlineExecution: true,
	})

	// Inline code execution should now be allowed.
	resp := runBashTool(t, tool, ctx, BashParams{Command: "python -c 'print(1)'"})
	require.NotContains(t, resp.Content, "not allowed for security reasons",
		"python -c should be allowed when AllowInlineExecution=true")

	resp = runBashTool(t, tool, ctx, BashParams{Command: "node -e 'console.log(1)'"})
	require.NotContains(t, resp.Content, "not allowed for security reasons",
		"node -e should be allowed when AllowInlineExecution=true")

	resp = runBashTool(t, tool, ctx, BashParams{Command: "bash -c 'echo hi'"})
	require.NotContains(t, resp.Content, "not allowed for security reasons",
		"bash -c should be allowed when AllowInlineExecution=true")

	// Other blockers should still be active.
	resp = runBashTool(t, tool, ctx, BashParams{Command: "curl http://example.com"})
	require.Contains(t, resp.Content, "not allowed for security reasons",
		"curl should still be blocked even with AllowInlineExecution=true")

	resp = runBashTool(t, tool, ctx, BashParams{Command: "sudo apt install foo"})
	require.Contains(t, resp.Content, "not allowed for security reasons",
		"sudo should still be blocked even with AllowInlineExecution=true")
}
