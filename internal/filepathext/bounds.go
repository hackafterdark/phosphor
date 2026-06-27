package filepathext

import (
	"os"
	"path/filepath"
	"strings"
)

// IsInside reports whether absPath is located within the directory tree rooted
// at absWorkspace. Both paths must be absolute before calling this function.
// Symlinks in both paths are resolved via EvalSymlinks to prevent bypass
// through symlink traversal, but if either path doesn't exist on disk the
// check still proceeds using the original paths. It returns false if either
// path is empty or if the comparison fails for any reason (including
// cross-device boundaries on some platforms).
func IsInside(absPath, absWorkspace string) bool {
	if absPath == "" || absWorkspace == "" {
		return false
	}
	// Resolve symlinks to prevent bypass through symlink traversal. If a
	// path doesn't exist on disk, fall back to using it as-is.
	resolvedPath := absPath
	if rp, err := filepath.EvalSymlinks(absPath); err == nil {
		resolvedPath = rp
	}
	resolvedWorkspace := absWorkspace
	if rw, err := filepath.EvalSymlinks(absWorkspace); err == nil {
		resolvedWorkspace = rw
	}
	rel, err := filepath.Rel(strings.ToLower(resolvedWorkspace), strings.ToLower(resolvedPath))
	if err != nil {
		return false
	}
	if rel == ".." {
		return false
	}
	return !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}
