package filepathext

import (
	"fmt"
	"path/filepath"
	"strings"
)

// IsGitMetadataPath reports whether absPath names repository metadata — a
// .git directory or anything beneath it, at the workspace root or inside a
// nested/submodule checkout. .git content is executable by construction:
// hooks fire on the next git invocation, and config keys such as
// core.fsmonitor, credential.helper, and diff.<driver>.command run whatever
// they name. An agent-authored hook or config line is therefore remote code
// execution deferred to the next human git command, so the agent may read
// .git but never write it; repository state changes go through git commands.
//
// absPath and absWorkspace must both be absolute.
func IsGitMetadataPath(absPath, absWorkspace string) bool {
	if absPath == "" || absWorkspace == "" {
		return false
	}
	// Resolve symlinks so a swapped intermediate directory cannot smuggle a
	// protected path past the check, mirroring IsInside's behavior.
	absPath = resolveSymlinks(absPath)
	absWorkspace = resolveSymlinks(absWorkspace)

	rel, err := filepath.Rel(absWorkspace, absPath)
	if err != nil {
		return false
	}
	rel = filepath.ToSlash(rel)
	if rel == ".." || strings.HasPrefix(rel, "../") {
		return false
	}
	fold := caseInsensitiveFS(absWorkspace)
	for _, part := range strings.Split(rel, "/") {
		if part == ".git" || (fold && strings.EqualFold(part, ".git")) {
			return true
		}
	}
	// .git itself may be a symlink to the real metadata directory; writes to
	// the target are writes to the git directory even though no component of
	// the path is named .git.
	if gitDir := resolveSymlinks(filepath.Join(absWorkspace, ".git")); insideRel(gitDir, absPath) {
		return true
	}
	return false
}

// ValidateGitMetadataWrite rejects writes whose target is repository
// metadata. The message routes the agent to the sanctioned path: git
// commands for repository state, direct user action for anything else.
func ValidateGitMetadataWrite(absPath, absWorkspace string) error {
	if IsGitMetadataPath(absPath, absWorkspace) {
		return fmt.Errorf(
			"Security violation: %s is repository metadata (.git) the agent may not modify. "+
				"Hooks and git config are executed by the next git invocation, so they are "+
				"read-only for agent tools; change repository state through git commands, or "+
				"ask the user to edit it directly.",
			absPath)
	}
	return nil
}
