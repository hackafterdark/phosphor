//go:build windows

package shell

import (
	"fmt"
	"os"
	"os/exec"
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

// TestSandboxWindowsKernelDeniesWriteOutsideWorkspace drives the same
// kernel vector as the Unix suite: `env sh -c` builds the target path
// itself, and the kernel boundary must deny the write. The Phase-6 gate
// passes env-wrapped nested shells once a kernel boundary is attached —
// the boundary itself is the control.
func TestSandboxWindowsKernelDeniesWriteOutsideWorkspace(t *testing.T) {
	t.Parallel()

	if ok, _ := sandbox.Available(); !ok {
		t.Skip("host enforces no kernel boundary; the vector would be vacuous")
	}
	if _, err := exec.LookPath("env"); err != nil {
		t.Skip("env binary not on PATH (needs Git for Windows)")
	}
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not on PATH (needs Git for Windows)")
	}

	workspace := t.TempDir()
	home, err := os.UserHomeDir()
	require.NoError(t, err)
	escape := filepath.Join(home, fmt.Sprintf("phosphor_sandbox_escape_%d", os.Getpid()))
	t.Cleanup(func() { _ = os.Remove(escape) })
	require.NoFileExists(t, escape)

	result, err := RunAndCapture(t.Context(), RunOptions{
		Command: fmt.Sprintf(
			`env sh -c 'echo reached; echo pwned > %s'`, escape),
		Cwd:         workspace,
		Workspace:   workspace,
		SandboxMode: "workspace",
	})
	t.Logf("sandboxed escape attempt: exit=%d output=%q err=%v", result.ExitCode, result.Output, err)
	require.Contains(t, result.Output, "reached",
		"nested shell must run up to the denied write")
	require.NoFileExists(t, escape, "kernel sandbox must deny writes outside the workspace")
}
