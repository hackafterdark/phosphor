package memory

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/hackafterdark/phosphor/internal/home"
)

// Vault layout constants. The markdown directories are Obsidian-openable; the
// index directory is what a user tells Obsidian to exclude.
const (
	// VaultDirName is the vault folder inside a workspace's .phosphor directory.
	VaultDirName = "memory"
	// EntriesDirName holds one file per atomic fact (Tier B corpus).
	EntriesDirName = "entries"
	// IndexDirName holds the derived, disposable SQLite index.
	IndexDirName = ".phosphor-index"
	// IndexFileName is the derived database name.
	IndexFileName = "memory.db"
	// PhosphorDirName is the workspace-scoped state directory.
	PhosphorDirName = ".phosphor"
)

// GlobalVaultDir is the user-level vault: `~/.phosphor/memory`. It is the only
// scope that crosses projects.
func GlobalVaultDir() string {
	return filepath.Join(home.Dir(), ".phosphor", VaultDirName)
}

// ProjectVaultDir is the workspace-scoped vault: `<ws>/.phosphor/memory`.
func ProjectVaultDir(workspaceDir string) string {
	return filepath.Join(workspaceDir, PhosphorDirName, VaultDirName)
}

// IsVaultPath reports whether a target path lives inside either vault, in which
// case the generic edit/write/append tools must refuse it. Only the memory tool
// may mutate the vault, and that is what lets schema, sanitization, and index
// sync be guarantees instead of hopes.
func IsVaultPath(workspaceDir, target string) bool {
	if target == "" {
		return false
	}
	candidate := normalizeForCompare(target)
	for _, dir := range vaultDirs(workspaceDir) {
		if dir == "" {
			continue
		}
		base := normalizeForCompare(dir)
		if candidate == base || strings.HasPrefix(candidate, base+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

func vaultDirs(workspaceDir string) []string {
	dirs := []string{GlobalVaultDir()}
	if workspaceDir != "" {
		dirs = append(dirs, ProjectVaultDir(workspaceDir))
	}
	return dirs
}

// normalizeForCompare folds separators, trailing slashes, and case so a path
// written `\.phosphor\memory\x.md` and one written `.phosphor/memory/x.md`
// compare equal on Windows.
func normalizeForCompare(p string) string {
	slash := filepath.ToSlash(p)
	slash = strings.ReplaceAll(slash, `\\`, "/")
	slash = strings.TrimRight(slash, "/")
	if i := strings.IndexByte(slash, ':'); i > 0 && i <= 2 {
		// Fold the drive letter: `F:/x` and `f:/x` are the same location.
		slash = strings.ToUpper(slash[:i]) + slash[i:]
	}
	return slash
}

// EnsureVault creates the vault skeleton for a workspace, including the
// .gitignore that keeps the derived index and Obsidian's own state out of the
// repository.
func EnsureVault(workspaceDir string) error {
	for _, dir := range vaultDirs(workspaceDir) {
		if err := os.MkdirAll(filepath.Join(dir, EntriesDirName), 0o755); err != nil {
			return err
		}
	}
	return writeVaultGitignore(filepath.Join(ProjectVaultDir(workspaceDir), ".gitignore"))
}

func writeVaultGitignore(path string) error {
	const contents = "# Derived, rebuildable: delete it and a rescan recreates it.\n" +
		IndexDirName + "/\n" +
		".obsidian/\n" +
		"*.tmp\n" +
		"*.bak.*\n"
	if _, err := os.Stat(path); err == nil {
		// A user who edited their own ignore rules keeps them.
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(contents), 0o644)
}
