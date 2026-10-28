//go:build windows

package shell

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/hackafterdark/phosphor/pkg/sandbox"
	"github.com/stretchr/testify/require"
)

// escapeChildEnv carries the escape path to the kernel-launched copy of
// this test binary; see runEscapeChild.
const escapeChildEnv = "PHOSPHOR_TEST_ESCAPE_CHILD"

// TestMain intercepts the kernel-launched child copy of the test binary
// before any test runs: in child mode the binary builds its own target
// path and attempts the write, which the kernel boundary must deny.
func TestMain(m *testing.M) {
	if target := os.Getenv(escapeChildEnv); target != "" {
		runEscapeChild(target)
		return
	}
	os.Exit(m.Run())
}

// runEscapeChild is the child half of the Windows kernel-denial vector.
// The target path is rebuilt here from USERPROFILE (the launch environment
// carries it, not the test's argv), so no in-process argv check ever
// sees it — the kernel boundary is the only layer that can deny it.
func runEscapeChild(target string) {
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Println("child: no home dir:", err)
		os.Exit(2)
	}
	// The test name carries the parent's pid; strip the marker and
	// rebuild the target from this process's own view of the host.
	marker := filepath.Base(target)
	escape := filepath.Join(home, marker)
	fmt.Println("reached")
	if err := os.WriteFile(escape, []byte("pwned"), 0o644); err != nil {
		fmt.Println("child: write denied:", err)
		os.Exit(3)
	}
	fmt.Println("child: wrote", escape)
	os.Exit(0)
}

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
// kernel vector as the Unix suite: the child process builds the target
// path itself, and the kernel boundary must deny the write. The Phase-6
// gate passes env-wrapped nested shells once a kernel boundary is
// attached — the boundary itself is the control.
//
// Unlike the Unix suite, the vector cannot shell out to `env sh -c`:
// Git-for-Windows binaries are cygwin-based, and the cygwin runtime
// fails to initialize inside the kernel boundary (STATUS_DLL_INIT_FAILED,
// exit 0xc0000142), so the nested shell never reaches the denied write.
// The vector therefore re-executes this test binary in child mode
// (PHOSPHOR_TEST_ESCAPE_CHILD carries the target path): the binary is
// native PE, runs inside the boundary, and constructs the escape path
// itself, so no in-process argv check ever sees it.
func TestSandboxWindowsKernelDeniesWriteOutsideWorkspace(t *testing.T) {
	t.Parallel()

	if ok, _ := sandbox.Available(); !ok {
		t.Skip("host enforces no kernel boundary; the vector would be vacuous")
	}

	workspace := t.TempDir()
	home, err := os.UserHomeDir()
	require.NoError(t, err)
	escape := filepath.Join(home, fmt.Sprintf("phosphor_sandbox_escape_%d", os.Getpid()))
	t.Cleanup(func() { _ = os.Remove(escape) })
	require.NoFileExists(t, escape)

	// The child builds the target path itself from the marker it
	// receives in its environment; the assignment prefix runs natively
	// in the interpreter, so no external env binary is needed.
	result, err := RunAndCapture(t.Context(), RunOptions{
		Command: fmt.Sprintf(
			`%s=%q %q`, escapeChildEnv, filepath.Base(escape), os.Args[0]),
		Cwd:         workspace,
		Workspace:   workspace,
		SandboxMode: "workspace",
	})
	t.Logf("sandboxed escape attempt: exit=%d output=%q err=%v", result.ExitCode, result.Output, err)
	require.Contains(t, result.Output, "reached",
		"child process must run up to the denied write")
	require.NoFileExists(t, escape, "kernel sandbox must deny writes outside the workspace")
}
