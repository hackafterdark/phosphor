//go:build !windows

package shell

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/hackafterdark/phosphor/pkg/sandbox"
	"github.com/stretchr/testify/require"
)

// TestSandboxKernelDeniesWriteOutsideWorkspace drives the exact bypass the
// kernel boundary exists for: a child process constructs the target path
// itself, so no in-process argv/redirect check ever sees it. The write must
// fail and the file must not exist.
func TestSandboxKernelDeniesWriteOutsideWorkspace(t *testing.T) {
	t.Parallel()

	if ok, _ := sandbox.Available(); !ok {
		t.Skip("platform sandbox unavailable on this host")
	}

	workspace := t.TempDir()
	home, err := os.UserHomeDir()
	require.NoError(t, err)
	escape := filepath.Join(home, fmt.Sprintf("phosphor_sandbox_escape_%d", os.Getpid()))
	t.Cleanup(func() { _ = os.Remove(escape) })
	require.NoFileExists(t, escape)

	// BlockFuncs nil mirrors the hook trust posture that lets `sh -c`
	// through: the point is that even when the in-process blocklist is
	// bypassed, the kernel still denies the escape. The nested shell is
	// spawned by `env` at the kernel level (the Phase-6 gate sees only
	// `env` and confines its children through the inherited boundary),
	// so the kernel sandbox itself — not the escape-hatch gate — is what
	// denies the write.
	result, err := RunAndCapture(t.Context(), RunOptions{
		Command:     fmt.Sprintf(`env sh -c 'echo pwned > %s'`, escape),
		Cwd:         workspace,
		Workspace:   workspace,
		SandboxMode: "workspace",
	})
	t.Logf("sandboxed escape attempt: exit=%d output=%q err=%v", result.ExitCode, result.Output, err)
	require.NoFileExists(t, escape, "kernel sandbox must deny writes outside the workspace")
}

// TestSandboxAllowsWorkspaceWrite confirms the workspace stays writable
// inside the sandbox, so the boundary is confinement and not a plain deny.
func TestSandboxAllowsWorkspaceWrite(t *testing.T) {
	t.Parallel()

	if ok, _ := sandbox.Available(); !ok {
		t.Skip("platform sandbox unavailable on this host")
	}

	workspace := t.TempDir()
	inside := filepath.Join(workspace, "inside.txt")

	_, err := RunAndCapture(t.Context(), RunOptions{
		Command:     fmt.Sprintf(`echo ok > %s`, inside),
		Cwd:         workspace,
		Workspace:   workspace,
		SandboxMode: "workspace",
	})
	require.NoError(t, err)
	require.FileExists(t, inside)
}
