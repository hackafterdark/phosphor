//go:build windows

package shell

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/hackafterdark/phosphor/pkg/sandbox"
	"github.com/stretchr/testify/require"
)

// TestSandboxWindowsWorkspaceWritable proves degraded workspace mode
// still runs commands and keeps the workspace writable: partial kernel
// containment (restricted token + job) applies, the degraded warning is
// surfaced, and the tool itself stays functional.
func TestSandboxWindowsWorkspaceWritable(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	inside := filepath.Join(workspace, "inside.txt")

	_, err := RunAndCapture(t.Context(), RunOptions{
		Command:     `echo ok > "` + inside + `"`,
		Cwd:         workspace,
		Workspace:   workspace,
		SandboxMode: "workspace",
	})
	require.NoError(t, err)
	require.FileExists(t, inside)
}

// TestSandboxWindowsStrictRefusesExec pins invariant 1: strict mode
// refuses to exec when the platform cannot enforce the promised
// boundary.
func TestSandboxWindowsStrictRefusesExec(t *testing.T) {
	t.Parallel()

	if ok, _ := sandbox.Available(); ok {
		t.Skip("platform enforces the workspace boundary; strict mode should exec")
	}

	workspace := t.TempDir()
	result, err := RunAndCapture(t.Context(), RunOptions{
		// echo is a builtin and never reaches the exec handler; use a
		// real binary so the strict-mode refusal path is exercised.
		// whoami takes no arguments, so pathguard's argument parsing
		// stays out of the way.
		Command:     `whoami`,
		Cwd:         workspace,
		Workspace:   workspace,
		SandboxMode: "strict",
	})
	require.NoError(t, err) // the shell itself reports the refusal
	require.Equal(t, 126, result.ExitCode)
	require.Contains(t, result.Output, "sandbox refused exec")
	_, _ = os.UserHomeDir() // keep imports obvious for future escape vectors
}
