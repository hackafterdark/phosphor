package shell

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// Audit finding (low, #10): `pwd` after an unprovable `cd` leaked the outside
// path to the transcript, because the cd/pwd builtins are dispatched inside
// the interpreter and never reach the exec/open gates. The fix installs a
// CallHandler (which sees builtins) that refuses any command while the cwd
// sits outside the trusted roots, and bounds-checks cd targets post-expansion.

func TestPwdAfterUnprovableCdCannotLeakOutsidePath(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	sibling := filepath.Join(filepath.Dir(workspace), "pwd-leak-sibling")
	require.NoError(t, os.MkdirAll(sibling, 0o755))

	var buf bytes.Buffer
	err := Run(t.Context(), RunOptions{
		// Two-hop relative traversal: neither `cd ..` nor `cd sibling`
		// carries a statically provable outside path, so the static pass
		// waves them through; the runtime CallHandler must stop the
		// sequence before pwd prints the outside directory.
		Command:         "cd .. && cd pwd-leak-sibling && pwd",
		Cwd:             workspace,
		Workspace:       workspace,
		Stdout:          &buf,
		DisableTempRoot: true,
		Env:             os.Environ(),
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "outside workspace")
	require.Empty(t, buf.String(), "the outside path must never reach the transcript")
}

func TestPwdAfterEnvVarCdCannotLeakOutsidePath(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	outside := t.TempDir()

	var buf bytes.Buffer
	err := Run(t.Context(), RunOptions{
		Command:         "cd $MYDIR && pwd",
		Cwd:             workspace,
		Workspace:       workspace,
		Stdout:          &buf,
		DisableTempRoot: true,
		Env:             append(os.Environ(), "MYDIR="+outside),
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "outside workspace")
	require.Empty(t, buf.String(), "the outside path must never reach the transcript")
}

func TestLegitimateCdIntoSubdirStillWorks(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	sub := filepath.Join(workspace, "sub")
	require.NoError(t, os.MkdirAll(sub, 0o755))

	var buf bytes.Buffer
	err := Run(t.Context(), RunOptions{
		Command:   "cd sub && pwd",
		Cwd:       workspace,
		Workspace: workspace,
		Stdout:    &buf,
		Env:       os.Environ(),
	})
	require.NoError(t, err)
	require.Contains(t, buf.String(), "sub", "legitimate in-workspace cd must still work")
}

func TestCdAndBackInSameCommandStillWorks(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	sub := filepath.Join(workspace, "sub")
	require.NoError(t, os.MkdirAll(sub, 0o755))

	var buf bytes.Buffer
	err := Run(t.Context(), RunOptions{
		Command:   "cd sub && cd .. && pwd",
		Cwd:       workspace,
		Workspace: workspace,
		Stdout:    &buf,
		Env:       os.Environ(),
	})
	require.NoError(t, err)
}

func TestBareCdUsesInterpreterHome(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	outside := t.TempDir()

	// A bare `cd` lands in the interpreter's HOME, so the gate must read
	// HOME from the handler env, not the process env: with HOME pointing
	// outside the workspace, the cd must be refused.
	var buf bytes.Buffer
	err := Run(t.Context(), RunOptions{
		Command:         "cd && pwd",
		Cwd:             workspace,
		Workspace:       workspace,
		Stdout:          &buf,
		DisableTempRoot: true,
		Env:             append(os.Environ(), "HOME="+outside),
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "outside workspace")
	require.Empty(t, buf.String(), "the outside path must never reach the transcript")

	// And when HOME points inside the workspace, the bare cd is fine.
	buf.Reset()
	err = Run(t.Context(), RunOptions{
		Command:   "cd && pwd",
		Cwd:       workspace,
		Workspace: workspace,
		Stdout:    &buf,
		Env:       append(os.Environ(), "HOME="+workspace),
	})
	require.NoError(t, err)
}

func TestCdDashUsesValidatedOldPwd(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	outside := t.TempDir()

	// `cd -` jumps to OLDPWD and prints it. If OLDPWD names a directory
	// outside the workspace, the jump must be refused before the target
	// is entered or printed.
	var buf bytes.Buffer
	err := Run(t.Context(), RunOptions{
		Command:         "cd - && pwd",
		Cwd:             workspace,
		Workspace:       workspace,
		Stdout:          &buf,
		DisableTempRoot: true,
		Env:             append(os.Environ(), "OLDPWD="+outside),
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "outside workspace")
	require.Empty(t, buf.String(), "the outside path must never reach the transcript")
}
