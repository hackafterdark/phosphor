package shell

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestEscapeHatchGateDenies runs each gated argv[0] under workspace mode
// and pins the refusal contract: the sandbox refusal marker on stderr and
// exit status 126, which the bash tool recognizes as a sandbox denial and
// pairs with escalation guidance. The gate refuses before exec, so the
// binaries do not need to exist on the test host.
func TestEscapeHatchGateDenies(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	for _, tc := range []struct {
		name    string
		command string
	}{
		{"wsl", `wsl sh -c 'echo hi'`},
		{"cmd", `cmd /c echo hi`},
		{"powershell", `powershell -Command "Get-Host"`},
		{"pwsh", `pwsh -c "echo hi"`},
		{"sh -c", `sh -c 'echo hi'`},
		{"bash -c", `bash -c 'echo hi'`},
		{"bash -lc", `bash -lc 'echo hi'`},
		{"busybox sh -c", `busybox sh -c 'echo hi'`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			result, err := RunAndCapture(t.Context(), RunOptions{
				Command:     tc.command,
				Cwd:         workspace,
				Workspace:   workspace,
				SandboxMode: "workspace",
			})
			require.NoError(t, err) // the shell itself reports the refusal
			require.Equal(t, 126, result.ExitCode)
			require.Contains(t, result.Output, SandboxRefusalMarker)
		})
	}
}

// TestEscapeHatchHandlerNilPolicyPassesThrough pins that an escalated (or
// sandbox-off) run is unconstrained by the gate: with no sandbox policy
// the middleware delegates every gated argv straight to the next handler.
// Handler-level on purpose — execing cmd/wsl to prove pass-through would
// depend on host binaries and pathguard argument parsing.
func TestEscapeHatchHandlerNilPolicyPassesThrough(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{
		{"wsl", "sh", "-c", "id"},
		{"cmd", "/c", "echo hi"},
		{"powershell", "-Command", "Get-Host"},
		{"sh", "-c", "echo hi"},
	} {
		passed := false
		handler := escapeHatchHandler(nil)(func(_ context.Context, got []string) error {
			passed = true
			require.Equal(t, args, got)
			return nil
		})
		require.NoError(t, handler(context.Background(), args))
		require.True(t, passed, "nil sandbox policy must leave %q ungated", args)
	}
}

// TestEscapeHatchGateAllowsScriptDispatch confirms the gate covers inline
// execution only: `sh script.sh` runs through script dispatch with the
// pathguard confining the script file itself.
func TestEscapeHatchGateAllowsScriptDispatch(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell script dispatch")
	}

	workspace := t.TempDir()
	script := filepath.Join(workspace, "script.sh")
	require.NoError(t, os.WriteFile(script, []byte("#!/bin/sh\necho hi\n"), 0o644))

	result, err := RunAndCapture(t.Context(), RunOptions{
		Command:     `sh script.sh`,
		Cwd:         workspace,
		Workspace:   workspace,
		SandboxMode: "workspace",
	})
	require.NoError(t, err)
	require.Equal(t, 0, result.ExitCode)
	require.Contains(t, result.Output, "hi")
}
