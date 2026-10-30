package filepathext

import (
	"fmt"
	"path/filepath"
	"strings"
)

// IsProtectedConfigFile reports whether absPath names a workspace/project
// config file the agent must not write. absPath and absWorkspace must both be
// absolute; comparison is case-folded for case-insensitive filesystems.
//
// The match is exact (basename + position), never a glob: .phosphor/ is
// otherwise live territory (SQLite memory DB, sessions, cron jobs) and only
// these two JSON files are protected.
func IsProtectedConfigFile(absPath, absWorkspace string) bool {
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

	// Case-fold for case-insensitive filesystems, where "Phosphor.json"
	// names the same file as "phosphor.json"; exact match elsewhere.
	fold := caseInsensitiveFS(absWorkspace)
	for _, protected := range []string{"phosphor.json", ".phosphor/phosphor.json"} {
		if rel == protected || (fold && strings.EqualFold(rel, protected)) {
			return true
		}
	}
	return false
}

// ValidateConfigWrite rejects writes whose target is a protected config file.
// It returns nil for every other in-workspace path. The message is written
// for the agent: it states the deny reason and routes the change to the user.
func ValidateConfigWrite(absPath, absWorkspace string) error {
	if IsProtectedConfigFile(absPath, absWorkspace) {
		return fmt.Errorf(
			"Security violation: %s is a configuration file the agent may not modify. "+
				"Config changes must come from direct user action; ask the user to edit it "+
				"or use the in-app configuration. Reads are unaffected.",
			filepath.Base(absPath))
	}
	return nil
}
