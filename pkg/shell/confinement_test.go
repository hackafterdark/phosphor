package shell

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func shellQuote(s string) string {
	return "'" + s + "'"
}

func TestPathConfinementBlocksPostExpansionArguments(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	err := Run(t.Context(), RunOptions{
		Command:   "somebinnonexistentxyz " + shellQuote("/etc/passwd"),
		Cwd:       workspace,
		Workspace: workspace,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "outside workspace")
}

func TestPathConfinementDisabledWithoutWorkspace(t *testing.T) {
	t.Parallel()

	err := Run(t.Context(), RunOptions{
		Command: "somebinnonexistentxyz " + shellQuote("/etc/passwd"),
		Cwd:     t.TempDir(),
	})
	require.Error(t, err)
	require.NotContains(t, err.Error(), "outside workspace")
}

func TestPathConfinementTrustsTempRootByDefault(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	tempFile := filepath.Join(os.TempDir(), "phosphor-shell-confinement-probe")
	require.NoError(t, os.WriteFile(tempFile, []byte("x"), 0o644))

	err := Run(t.Context(), RunOptions{
		Command:   "somebinnonexistentxyz " + shellQuote(tempFile),
		Cwd:       workspace,
		Workspace: workspace,
	})
	require.Error(t, err)
	require.NotContains(t, err.Error(), "outside workspace")
}

func TestPathConfinementCanConfineTempRoot(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	tempFile := filepath.Join(os.TempDir(), "phosphor-shell-confinement-probe")
	require.NoError(t, os.WriteFile(tempFile, []byte("x"), 0o644))

	err := Run(t.Context(), RunOptions{
		Command:         "somebinnonexistentxyz " + shellQuote(tempFile),
		Cwd:             workspace,
		Workspace:       workspace,
		DisableTempRoot: true,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "outside workspace")
}

func TestPathConfinementExtraTrustedRoots(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	extra := filepath.Join(filepath.Dir(workspace), "phosphor-extra-root")
	require.NoError(t, os.MkdirAll(extra, 0o755))
	extraFile := filepath.Join(extra, "shared.txt")
	require.NoError(t, os.WriteFile(extraFile, []byte("x"), 0o644))

	err := Run(t.Context(), RunOptions{
		Command:           "somebinnonexistentxyz " + shellQuote(extraFile),
		Cwd:               workspace,
		Workspace:         workspace,
		DisableTempRoot:   true,
		ExtraTrustedRoots: []string{extra},
	})
	require.Error(t, err)
	require.NotContains(t, err.Error(), "outside workspace")

	err = Run(t.Context(), RunOptions{
		Command:         "somebinnonexistentxyz " + shellQuote(extraFile),
		Cwd:             workspace,
		Workspace:       workspace,
		DisableTempRoot: true,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "outside workspace")
}

func TestPathConfinementAllowsDeviceFiles(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	err := Run(t.Context(), RunOptions{
		Command:   "somebinnonexistentxyz " + shellQuote("/dev/null"),
		Cwd:       workspace,
		Workspace: workspace,
	})
	require.Error(t, err)
	require.False(t, strings.Contains(err.Error(), "outside workspace"), err.Error())
}
