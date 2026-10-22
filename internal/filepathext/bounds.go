package filepathext

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
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
	if insideRel(resolvedWorkspace, resolvedPath) {
		return true
	}
	// Case-folded fallback for case-insensitive filesystems: the workspace
	// and target may legitimately differ in case (e.g. C:\Users\test\Project
	// vs C:\Users\test\project) and still denote the same directory. It is
	// applied only where the hosting filesystem ignores case — always on
	// Windows, and probed at runtime elsewhere (the default macOS APFS layout
	// is case-insensitive). On case-sensitive filesystems, case-differing
	// siblings are *different* directories and must not false-pass here.
	if caseInsensitiveFS(absWorkspace) {
		return insideRel(strings.ToLower(resolvedWorkspace), strings.ToLower(resolvedPath))
	}
	return false
}

// caseProbeCache memoizes the probe result per workspace root; the property
// belongs to the filesystem, not the path, so one probe per root is enough.
var caseProbeCache sync.Map // string -> bool

// caseInsensitiveFS reports whether the filesystem hosting dir ignores case.
// Windows is treated as case-insensitive; everywhere else the workspace root
// itself is probed: if dir and its fully upper-cased spelling resolve to the
// same directory, the filesystem does not preserve case. Unknown (missing
// dir, network path) answers conservatively as case-sensitive, which keeps
// the bounds check strict.
func caseInsensitiveFS(dir string) bool {
	if runtime.GOOS == "windows" {
		return true
	}
	if v, ok := caseProbeCache.Load(dir); ok {
		return v.(bool)
	}
	insensitive := false
	if !isNetworkPath(dir) {
		if fi1, err1 := os.Stat(dir); err1 == nil {
			if upper := strings.ToUpper(dir); upper != dir {
				if fi2, err2 := os.Stat(upper); err2 == nil && os.SameFile(fi1, fi2) {
					insensitive = true
				}
			}
		}
	}
	caseProbeCache.Store(dir, insensitive)
	return insensitive
}

// insideRel reports whether target is at or below base, using raw (case-
// preserving) string comparison of the two already-resolved absolute paths.
func insideRel(base, target string) bool {
	rel, err := filepath.Rel(base, target)
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
// that falls back to using it as-is can be fooled by a symlinked intermediate
// directory when writing a brand-new file (the symlink is only followed later,
// at open time, by the kernel). We resolve the deepest existing ancestor with
// EvalSymlinks and re-append the still-missing components, which mirrors how
// the path will actually be opened.
func resolveSymlinks(absPath string) string {
	if isNetworkPath(absPath) {
		return filepath.Clean(absPath)
	}

	if resolved, err := filepath.EvalSymlinks(absPath); err == nil {
		return followReparseTargets(resolved)
	}

	root := filepath.Clean(absPath)
	var missing []string
	for {
		if resolved, err := filepath.EvalSymlinks(root); err == nil {
			result := followReparseTargets(resolved)
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

// followReparseTargets resolves reparse points that EvalSymlinks reports as
// resolved but leaves unresolved on Windows — notably NTFS junctions and
// mount points, which EvalSymlinks hands back unchanged. os.Readlink still
// reports the link target for those, so follow it until the path stops
// changing. The hop cap bounds cycles through self-referential links.
func followReparseTargets(path string) string {
	for range 16 {
		// codeql[go/path-injection] ignore -- Readlink is part of the
		// containment check itself: it only reads the link target for
		// comparison against the workspace root. No file is opened and
		// nothing is returned to the caller.
		target, err := os.Readlink(path)
		if err != nil || target == "" {
			return path
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(path), target)
		}
		path = filepath.Clean(target)
	}
	return path
}

// isNetworkPath reports whether a Windows path names a UNC resource. Checking
// such paths on disk can make the SMB redirecter start NetBIOS/DNS resolution
// and TCP connection attempts, so bounds checks compare them lexically instead
// of risking a long block while the operating system tries to contact the
// remote host.
func isNetworkPath(absPath string) bool {
	if runtime.GOOS != "windows" {
		return false
	}

	cleaned := filepath.ToSlash(filepath.Clean(absPath))
	return strings.HasPrefix(cleaned, "//")
}
