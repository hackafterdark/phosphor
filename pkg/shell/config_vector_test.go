package shell

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestRunConfigFileWriteProtection pins that workspace/project config files
// are read-only for confined children: shell redirects may not write them,
// while reads and writes to every other in-workspace path keep working.
// Config changes are reserved to direct user action.
func TestRunConfigFileWriteProtection(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(workspace, ".phosphor"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(workspace, "phosphor.json"), []byte("{}\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(workspace, ".phosphor", "phosphor.json"), []byte("{}\n"), 0o644))

	for _, cmd := range []string{
		"echo x > phosphor.json",
		"echo x > .phosphor/phosphor.json",
		"echo x >> phosphor.json",
		"echo x > \"$WS/phosphor.json\"",
		"echo x > \"$WS/.phosphor/phosphor.json\"",
	} {
		err := Run(t.Context(), RunOptions{
			Command:   cmd,
			Cwd:       workspace,
			Workspace: workspace,
			Env:       []string{"WS=" + filepath.ToSlash(workspace)},
		})
		require.Error(t, err, cmd)
		require.Contains(t, err.Error(), "configuration file", cmd)
	}

	// Reads of the config file stay unrestricted.
	for _, cmd := range []string{
		"cat phosphor.json",
		"cat < .phosphor/phosphor.json",
	} {
		err := Run(t.Context(), RunOptions{
			Command:   cmd,
			Cwd:       workspace,
			Workspace: workspace,
		})
		if err != nil {
			require.NotContains(t, err.Error(), "configuration file", cmd)
		}
	}

	// Ordinary in-workspace writes are unaffected.
	err := Run(t.Context(), RunOptions{
		Command:   "echo x > notes.txt",
		Cwd:       workspace,
		Workspace: workspace,
	})
	if err != nil {
		require.NotContains(t, err.Error(), "configuration file", err.Error())
	}
}
