package filepathext

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGitGuard_ProtectsRepositoryMetadata(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(ws, ".git", "hooks"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(ws, "sub", "repo", ".git"), 0o755))

	protected := []string{
		filepath.Join(ws, ".git", "hooks", "pre-commit"),
		filepath.Join(ws, ".git", "config"),
		filepath.Join(ws, ".git"),
		filepath.Join(ws, "sub", "repo", ".git", "HEAD"),
	}
	for _, p := range protected {
		require.Error(t, ValidateGitMetadataWrite(p, ws), p)
		require.True(t, IsGitMetadataPath(p, ws), p)
	}
}

func TestGitGuard_LeavesLookalikesAlone(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()

	allowed := []string{
		filepath.Join(ws, ".gitignore"),
		filepath.Join(ws, ".gitattributes"),
		filepath.Join(ws, ".github", "workflows", "ci.yml"),
		filepath.Join(ws, "git", "hooks"),
		filepath.Join(ws, "my.git.gitdir"),
		filepath.Join(ws, "src", "main.go"),
	}
	for _, p := range allowed {
		require.NoError(t, ValidateGitMetadataWrite(p, ws), p)
		require.False(t, IsGitMetadataPath(p, ws), p)
	}
}

func TestGitGuard_SiblingWorkspaceNotChecked(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	other := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(other, ".git"), 0o755))

	// Outside paths are the workspace bounds check's business, not this one.
	require.NoError(t, ValidateGitMetadataWrite(filepath.Join(other, ".git", "config"), ws))
}

func TestGitGuard_SymlinkedGitDir(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	real := filepath.Join(ws, "real-git")
	require.NoError(t, os.MkdirAll(real, 0o755))
	link := filepath.Join(ws, ".git")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	// A symlink swapped in for .git still resolves under the guard.
	require.Error(t, ValidateGitMetadataWrite(filepath.Join(real, "hooks", "pre-commit"), ws))
} // TestGitGuard_CaseFoldedGitComponent covers case-insensitive filesystems,
// where .GIT and .git name the same directory: the guard must follow the
// filesystem's semantics, not the spelling in the path.
func TestGitGuard_CaseFoldedGitComponent(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	if !caseInsensitiveFS(ws) {
		t.Skip("filesystem preserves case; spelling cannot alias .git")
	}
	require.NoError(t, os.MkdirAll(filepath.Join(ws, ".git", "hooks"), 0o755))

	require.Error(t, ValidateGitMetadataWrite(filepath.Join(ws, ".GIT", "hooks", "pre-commit"), ws))
	require.Error(t, ValidateGitMetadataWrite(filepath.Join(ws, ".Git", "config"), ws))
}
