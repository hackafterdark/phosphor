package filepathext

import (
	"os"
	"path/filepath"
	"strings"
)

// IsInside reports whether absPath is located within the directory tree rooted
// at absWorkspace. Both paths must be absolute before calling this function.
// Symlinks in both paths are resolved to prevent bypass through symlink
// traversal, and the resolution is applied to the deepest existing ancestor so
// that a path whose *final* component does not yet exist (a file about to be
// created) still has any symlinked intermediate directory followed. It returns
// false if either path is empty or if the comparison fails for any reason
// (including cross-device boundaries on some platforms).
func IsInside(absPath, absWorkspace string) bool {
	if absPath == "" || absWorkspace == "" {
		return false
	}
	// Resolve symlinks to prevent bypass through symlink traversal. If a path
	// doesn't exist on disk, fall back to using it as-is.
	resolvedPath := resolveSymlinks(absPath)
	resolvedWorkspace := resolveSymlinks(absWorkspace)
	rel, err := filepath.Rel(strings.ToLower(resolvedWorkspace), strings.ToLower(resolvedPath))
	if err != nil {
		return false
	}
	if rel == ".." {
		return false
	}
	return !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

// resolveSymlinks resolves every symlink component of absPath, even when the
// final (or a trailing) component does not exist on disk. os.EvalSymlinks
// refuses to resolve a path containing a missing component, so a naive caller
// that falls back to the raw path can be fooled by a symlinked intermediate
// directory when writing a brand-new file (the symlink is only followed later,
// at open time, by the kernel). We resolve the deepest existing ancestor with
// EvalSymlinks and re-append the still-missing components, which mirrors how
// the path will actually be opened.
func resolveSymlinks(absPath string) string {
	if resolved, err := filepath.EvalSymlinks(absPath); err == nil {
		return resolved
	}

	root := filepath.Clean(absPath)
	var missing []string
	for {
		if resolved, err := filepath.EvalSymlinks(root); err == nil {
			result := resolved
			for i := len(missing) - 1; i >= 0; i-- {
				result = filepath.Join(result, missing[i])
			}
			return result
		}
		parent := filepath.Dir(root)
		if parent == root {
			// Nothing on this path exists; return the cleaned original.
			return root
		}
		missing = append(missing, filepath.Base(root))
		root = parent
	}
}
