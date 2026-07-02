package agent

import (
	"context"
	"testing"

	"charm.land/fantasy"
	"github.com/hackafterdark/phosphor/internal/config"
	"github.com/hackafterdark/phosphor/internal/hooks"
	"github.com/hackafterdark/phosphor/internal/permission"
	"github.com/stretchr/testify/require"
)

// fakeTool records the context it was invoked with so tests can assert on
// values stamped onto it by the hookedTool decorator.
type fakeTool struct {
	name   string
	called bool
	gotCtx context.Context
	resp   fantasy.ToolResponse
}

func (f *fakeTool) Info() fantasy.ToolInfo {
	return fantasy.ToolInfo{Name: f.name}
}

func (f *fakeTool) Run(ctx context.Context, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
	f.called = true
	f.gotCtx = ctx
	return f.resp, nil
}

func (f *fakeTool) ProviderOptions() fantasy.ProviderOptions     { return nil }
func (f *fakeTool) SetProviderOptions(_ fantasy.ProviderOptions) {}

// newRunner builds a hooks.Runner from a single HookConfig, running the
// config-loader path that compiles the matcher regex.
func newRunner(t *testing.T, cmd string) *hooks.Runner {
	t.Helper()
	cfg := &config.Config{
		Hooks: map[string][]config.HookConfig{
			hooks.EventPreToolUse: {{Command: cmd}},
		},
	}
	require.NoError(t, cfg.ValidateHooks())
	return hooks.NewRunner(cfg.Hooks[hooks.EventPreToolUse], t.TempDir(), t.TempDir())
}

func TestHookedTool_AllowStampsHookApproval(t *testing.T) {
	t.Parallel()

	inner := &fakeTool{name: "view", resp: fantasy.NewTextResponse("ok")}
	runner := newRunner(t, `echo '{"decision":"allow"}'`)
	tool := newHookedTool(inner, runner, "")

	_, err := tool.Run(t.Context(), fantasy.ToolCall{ID: "call-1", Name: "view"})
	require.NoError(t, err)
	require.True(t, inner.called, "inner tool should have run")

	// The inner tool's permission service can now treat call-1 as pre-approved.
	svc := permission.NewPermissionService(t.TempDir(), false, nil)
	granted, err := svc.Request(inner.gotCtx, permission.CreatePermissionRequest{
		SessionID:  "s1",
		ToolCallID: "call-1",
		ToolName:   "view",
		Action:     "read",
		Path:       t.TempDir(),
	})
	require.NoError(t, err)
	require.True(t, granted, "hook allow should bypass the permission prompt")
}

func TestHookedTool_SilentDoesNotStampApproval(t *testing.T) {
	t.Parallel()

	inner := &fakeTool{name: "view", resp: fantasy.NewTextResponse("ok")}
	runner := newRunner(t, `exit 0`) // no stdout, no decision
	tool := newHookedTool(inner, runner, "")

	_, err := tool.Run(t.Context(), fantasy.ToolCall{ID: "call-2", Name: "view"})
	require.NoError(t, err)
	require.True(t, inner.called)

	// With no hook opinion, a fresh permission request has nothing stamped
	// and must fall through to the normal flow. We verify by checking that
	// the context does not look pre-approved for this call ID: sending a
	// request that no subscriber resolves will block until cancelled.
	svc := permission.NewPermissionService(t.TempDir(), false, nil)
	ctx, cancel := context.WithCancel(inner.gotCtx)
	cancel()
	granted, err := svc.Request(ctx, permission.CreatePermissionRequest{
		SessionID:  "s1",
		ToolCallID: "call-2",
		ToolName:   "view",
		Action:     "read",
		Path:       t.TempDir(),
	})
	require.Error(t, err, "no approval stamped => request should reach the prompt path")
	require.False(t, granted)
}

func TestHookedTool_DenySkipsInnerTool(t *testing.T) {
	t.Parallel()

	inner := &fakeTool{name: "bash"}
	runner := newRunner(t, `echo "blocked" >&2; exit 2`)
	tool := newHookedTool(inner, runner, "")

	resp, err := tool.Run(t.Context(), fantasy.ToolCall{ID: "call-3", Name: "bash"})
	require.NoError(t, err)
	require.False(t, inner.called, "denied call must not reach the inner tool")
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "blocked")
}

func TestWrapToolsWithHooks(t *testing.T) {
	t.Parallel()

	runner := newRunner(t, `exit 0`)
	inputs := []fantasy.AgentTool{&fakeTool{name: "a"}, &fakeTool{name: "b"}}

	t.Run("top-level agent wraps every tool", func(t *testing.T) {
		t.Parallel()
		out := wrapToolsWithHooks(inputs, runner, false, "")
		require.Len(t, out, len(inputs))
		for i, tool := range out {
			_, ok := tool.(*hookedTool)
			require.Truef(t, ok, "tool %d should be a *hookedTool", i)
		}
	})

	t.Run("sub-agent skips the wrap", func(t *testing.T) {
		t.Parallel()
		out := wrapToolsWithHooks(inputs, runner, true, "")
		require.Equal(t, inputs, out, "sub-agent tools should be returned unwrapped")
		for _, tool := range out {
			_, isHooked := tool.(*hookedTool)
			require.False(t, isHooked, "sub-agent tool should not be wrapped")
		}
	})

	t.Run("nil runner skips the wrap for both agent kinds when not fiduciary", func(t *testing.T) {
		t.Parallel()
		require.Equal(t, inputs, wrapToolsWithHooks(inputs, nil, false, ""))
		require.Equal(t, inputs, wrapToolsWithHooks(inputs, nil, true, ""))
	})

	t.Run("fiduciary forces wrap even with nil runner and sub-agent", func(t *testing.T) {
		t.Parallel()
		outMain := wrapToolsWithHooks(inputs, nil, false, "fiduciary")
		require.Len(t, outMain, len(inputs))
		for i, tool := range outMain {
			_, ok := tool.(*hookedTool)
			require.Truef(t, ok, "tool %d should be a *hookedTool", i)
		}

		outSub := wrapToolsWithHooks(inputs, nil, true, "fiduciary")
		require.Len(t, outSub, len(inputs))
		for i, tool := range outSub {
			_, ok := tool.(*hookedTool)
			require.Truef(t, ok, "tool %d should be a *hookedTool", i)
		}
	})
}

func TestHookedTool_FiduciaryGuardrails(t *testing.T) {
	t.Parallel()

	inner := &fakeTool{name: "bash", resp: fantasy.NewTextResponse("ok")}
	tool := newHookedTool(inner, nil, "fiduciary")

	tests := []struct {
		name      string
		cmd       string
		shouldErr bool
		reason    string
	}{
		{
			name:      "safe command",
			cmd:       `{"command": "echo hello"}`,
			shouldErr: false,
		},
		{
			name:      "restricted command sudo",
			cmd:       `{"command": "sudo apt-get update"}`,
			shouldErr: true,
			reason:    "Restricted Command \"sudo\"",
		},
		{
			name:      "restricted command useradd",
			cmd:       `{"command": "useradd admin"}`,
			shouldErr: true,
			reason:    "Restricted Command \"useradd\"",
		},
		{
			name:      "restricted path /etc",
			cmd:       `{"command": "cat /etc/passwd"}`,
			shouldErr: true,
			reason:    "Restricted Path \"/etc\"",
		},
		{
			name:      "restricted path ~/.ssh",
			cmd:       `{"command": "ls ~/.ssh"}`,
			shouldErr: true,
			reason:    "Restricted Path \"~/.ssh\"",
		},
		{
			name:      "restricted path Windows style",
			cmd:       `{"command": "type C:\\windows\\system32\\drivers\\etc\\hosts"}`,
			shouldErr: true,
			reason:    "Restricted Path \"\\\\etc\"",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inner.called = false
			resp, err := tool.Run(t.Context(), fantasy.ToolCall{ID: "call-fid", Name: "bash", Input: tt.cmd})
			require.NoError(t, err)

			if tt.shouldErr {
				require.False(t, inner.called, "inner tool should not have run")
				require.True(t, resp.IsError, "response should be an error")
				require.True(t, resp.StopTurn, "should stop the turn")
				require.Contains(t, resp.Content, "Fiduciary Policy Violation")
				require.Contains(t, resp.Content, tt.reason)
			} else {
				require.True(t, inner.called, "inner tool should have run")
				require.False(t, resp.IsError, "response should not be an error")
			}
		})
	}
}
