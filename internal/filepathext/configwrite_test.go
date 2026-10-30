package filepathext

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestConfigWrite_ProtectedPaths(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(ws, ".phosphor"), 0o755))

	protected := []string{
		filepath.Join(ws, "phosphor.json"),
		filepath.Join(ws, ".phosphor", "phosphor.json"),
	}
	for _, p := range protected {
		require.Error(t, ValidateConfigWrite(p, ws), p)
		require.True(t, IsProtectedConfigFile(p, ws), p)
	}
}

func TestConfigWrite_ReadsAndOtherWritesAllowed(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(ws, ".phosphor"), 0o755))

	allowed := []string{
		filepath.Join(ws, ".phosphor", "memory", "vault.db"),
		filepath.Join(ws, ".phosphor", "jobs", "nightly.md"),
		filepath.Join(ws, ".phosphor", "secret-rules.toml"),
		filepath.Join(ws, "other.json"),
		filepath.Join(ws, "PHOSPHOR.md"),
		filepath.Join(ws, "sub", "phosphor.json"),
	}
	for _, p := range allowed {
		require.NoError(t, ValidateConfigWrite(p, ws), p)
		require.False(t, IsProtectedConfigFile(p, ws), p)
	}
}

func TestConfigWrite_SiblingPrefixFalsePositive(t *testing.T) {
	t.Parallel()
	// A workspace named like the config dir must not trip the check, and a
	// sibling workspace's config must not leak across the boundary.
	ws := t.TempDir()
	other := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(other, ".phosphor"), 0o755))

	require.NoError(t, ValidateConfigWrite(filepath.Join(other, ".phosphor", "phosphor.json"), ws))
	require.NoError(t, ValidateConfigWrite(filepath.Join(other, "phosphor.json"), ws))
}

func TestConfigWrite_SymlinkedAlias(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	real := filepath.Join(ws, ".phosphor")
	require.NoError(t, os.MkdirAll(real, 0o755))
	link := filepath.Join(ws, "cfglink")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	// Writes through the symlink alias resolve to the protected file.
	require.Error(t, ValidateConfigWrite(filepath.Join(link, "phosphor.json"), ws))
}

func TestConfigWrite_EmptyInputs(t *testing.T) {
	t.Parallel()
	require.NoError(t, ValidateConfigWrite("", ""))
	require.NoError(t, ValidateConfigWrite("C:\\ws\\phosphor.json", ""))
	require.NoError(t, ValidateConfigWrite("", "C:\\ws"))
}

// TestConfigWrite_CaseFoldedVariant covers case-insensitive filesystems,
// where "Phosphor.json" names the same file as "phosphor.json": the guard
// must follow the filesystem's semantics, not the spelling in the path.
func TestConfigWrite_CaseFoldedVariant(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	if !caseInsensitiveFS(ws) {
		t.Skip("filesystem preserves case; spelling cannot alias the config file")
	}
	require.NoError(t, os.MkdirAll(filepath.Join(ws, ".phosphor"), 0o755))

	require.Error(t, ValidateConfigWrite(filepath.Join(ws, "Phosphor.json"), ws))
	require.Error(t, ValidateConfigWrite(filepath.Join(ws, ".PHOSPHOR", "phosphor.json"), ws))
}

// TestConfigWrite_DotDotDirNames checks that directory names merely starting
// with ".." (legal on POSIX, e.g. kubernetes-style "..data") are workspace
// content, not escapes: the escape check fires only on real parent escapes.
func TestConfigWrite_DotDotDirNames(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()

	// Nested configs are not the protected files; ".."-prefixed dirs are
	// ordinary workspace content, not outside the workspace.
	require.NoError(t, ValidateConfigWrite(filepath.Join(ws, "..data", "phosphor.json"), ws))
	require.NoError(t, ValidateConfigWrite(filepath.Join(ws, "..data", "notes.txt"), ws))
}
