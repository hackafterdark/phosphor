package model

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hackafterdark/phosphor/internal/ui/common"
	"github.com/hackafterdark/phosphor/internal/workspace"
	"github.com/stretchr/testify/require"
)

// writeFile creates a file with the given contents, creating parent
// directories as needed.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}

func TestReadGitBranch_WorktreeCheckout(t *testing.T) {
	t.Parallel()

	// The workspace's .git entry is a gitfile naming the real git dir,
	// as written by `git worktree add` and submodules.
	gitDir := filepath.Join(t.TempDir(), "repo", ".git")
	writeFile(t, filepath.Join(gitDir, "HEAD"), "ref: refs/heads/feature/x\n")

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".git"), "gitdir: "+gitDir+"\n")

	require.Equal(t, "feature/x", readGitBranch(dir))
}

func TestReadGitBranch_RelativeGitfile(t *testing.T) {
	t.Parallel()

	// Gitfiles may hold a path relative to the workspace directory.
	gitDir := filepath.Join(t.TempDir(), "repo", ".git")
	writeFile(t, filepath.Join(gitDir, "HEAD"), "ref: refs/heads/main\n")

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".git"), "gitdir: .git-worktrees/one\n")
	writeFile(t, filepath.Join(dir, ".git-worktrees", "one", "HEAD"), "ref: refs/heads/relbranch\n")

	require.Equal(t, "relbranch", readGitBranch(dir))
}

func TestReadGitBranch_BareRepo(t *testing.T) {
	t.Parallel()

	// Opening the workspace on the git directory itself (e.g. a bare repo)
	// must still report the branch from HEAD's contents.
	gitDir := filepath.Join(t.TempDir(), "repo.git")
	writeFile(t, filepath.Join(gitDir, "HEAD"), "ref: refs/heads/trunk\n")

	require.Equal(t, "trunk", readGitBranch(gitDir))
}

func TestReadGitBranch_InvalidHead(t *testing.T) {
	t.Parallel()

	// A HEAD that is a directory (or otherwise unreadable) must not make
	// the bare-repo path report a directory name as the branch.
	gitDir := filepath.Join(t.TempDir(), "repo.git")
	require.NoError(t, os.MkdirAll(filepath.Join(gitDir, "HEAD"), 0o755))

	require.Equal(t, "", readGitBranch(gitDir))
}

func TestReadGitBranch_DetachedHead(t *testing.T) {
	t.Parallel()

	gitDir := filepath.Join(t.TempDir(), "repo.git")
	writeFile(t, filepath.Join(gitDir, "HEAD"), "8f14e45fceea167a5a36dedd4bea2543\n")

	// SHA-1 hex, 40 chars: rendered truncated to the short-SHA length.
	branch := readGitBranch(gitDir)
	require.Equal(t, 7, len(branch))
	require.True(t, strings.HasPrefix("8f14e45fceea167a5a36dedd4bea2543", branch))
}

func TestReadGitBranch_NotARepo(t *testing.T) {
	t.Parallel()

	require.Equal(t, "", readGitBranch(t.TempDir()))
}

// branchTestWorkspace is a workspace stub pinned to a fixed directory.
type branchTestWorkspace struct {
	workspace.Workspace
	dir string
}

func (w *branchTestWorkspace) WorkingDir() string { return w.dir }

func TestGitBranch_RefreshesAfterCheckout(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".git", "HEAD"), "ref: refs/heads/main\n")

	ui := &UI{com: &common.Common{Workspace: &branchTestWorkspace{dir: dir}}}
	require.Equal(t, "main", ui.gitBranch())

	// Within the refresh window the cached value is served, even after
	// the checkout changes underneath the program.
	writeFile(t, filepath.Join(dir, ".git", "HEAD"), "ref: refs/heads/feature/x\n")
	require.Equal(t, "main", ui.gitBranch())

	// Once the refresh interval elapses the new branch is picked up.
	ui.gitBranchCheckedAt = time.Now().Add(-2 * gitBranchRefresh)
	require.Equal(t, "feature/x", ui.gitBranch())
}
