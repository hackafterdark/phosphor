package filepathext

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// SmartJoin joins two paths, treating the second path as absolute if it is an
// absolute path. It resolves and normalizes paths using heuristics to keep
// them inside the workspace. Use UnsafeSmartJoin for trusted extensions
// (e.g. MCP servers) that legitimately cross workspace bounds.
func SmartJoin(one, two string) string {
	return HeuristicClean(one, two)
}

// UnsafeSmartJoin joins two paths, treating the second path as absolute if it is an
// absolute path. Only use this for trusted extensions (e.g. MCP servers) that
// legitimately need cross-workspace access.
func UnsafeSmartJoin(one, two string) string {
	if SmartIsAbs(two) {
		return two
	}
	return filepath.Join(one, two)
}

// HeuristicClean resolves and normalizes a path target against a workspace root base.
// If the path looks like a workspace-relative path that was formatted as absolute
// (e.g. starting with a slash, or starting with a drive letter prefix), it corrects it
// to be relative to the workspace root.
func HeuristicClean(base, target string) string {
	if target == "" {
		return base
	}

	absBase, err := filepath.Abs(base)
	if err != nil {
		absBase = base
	}
	baseSlash := filepath.ToSlash(absBase)
	baseLower := strings.ToLower(baseSlash)

	targetSlash := filepath.ToSlash(target)

	// Heuristic 1: Unix-style drive letter prefix on Windows.
	// If it starts with /f/ where f is a drive letter, convert to f:/...
	if runtime.GOOS == "windows" {
		if len(targetSlash) >= 3 && targetSlash[0] == '/' && isAlpha(targetSlash[1]) && targetSlash[2] == '/' {
			targetSlash = string(targetSlash[1]) + ":" + targetSlash[2:]
		}
	}

	// Heuristic 2: Check if targetSlash is already inside baseSlash.
	// If so, return target cleaned as absolute.
	targetLower := strings.ToLower(targetSlash)
	if strings.HasPrefix(targetLower, baseLower) {
		if len(targetLower) == len(baseLower) {
			return absBase
		}
		if targetSlash[len(baseLower)] == '/' {
			return filepath.Clean(targetSlash)
		}
	}

	// Keep track of the stripped path
	stripped := targetSlash

	// Heuristic 3: Check if it has a drive letter prefix (e.g. f:/ or F:/).
	// If so, strip the drive letter prefix.
	if len(stripped) >= 3 && isAlpha(stripped[0]) && stripped[1] == ':' && stripped[2] == '/' {
		stripped = stripped[3:]
	}

	// Heuristic 4: Strip leading slashes to treat it as workspace-relative.
	stripped = strings.TrimLeft(stripped, "/")

	// Verify if we should apply the correction (either it's a file at root or the first directory component exists).
	if shouldCorrect(absBase, stripped) {
		return filepath.Clean(filepath.Join(absBase, stripped))
	}

	// If we shouldn't correct, return the target cleaned as absolute/relative as it was.
	if SmartIsAbs(target) {
		return filepath.Clean(target)
	}
	return filepath.Clean(filepath.Join(absBase, target))
}

func shouldCorrect(base, relPath string) bool {
	relPath = filepath.Clean(relPath)
	if relPath == "." || relPath == "/" || relPath == "" {
		return false
	}
	parts := strings.Split(filepath.ToSlash(relPath), "/")
	if len(parts) == 0 {
		return false
	}
	first := parts[0]
	if first == "" || first == "." || first == ".." {
		return false
	}
	if len(parts) == 1 {
		return true
	}
	// If the workspace base itself doesn't exist (e.g. in unit tests using mock paths),
	// we cannot perform the check on disk, so we default to allowing the correction.
	if _, err := os.Stat(base); err != nil {
		return true
	}
	if _, err := os.Stat(filepath.Join(base, first)); err == nil {
		return true
	}
	return false
}

func isAlpha(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// ValidatePath checks whether absPath is inside absWorkspace and returns an
// error if it is not. Both paths must be absolute before calling this function.
func ValidatePath(absPath, absWorkspace string) error {
	if !IsInside(absPath, absWorkspace) {
		return errors.New("path is outside workspace")
	}
	return nil
}

// SmartIsAbs checks if a path is absolute, considering both OS-specific and
// Unix-style paths.
func SmartIsAbs(path string) bool {
	switch runtime.GOOS {
	case "windows":
		return filepath.IsAbs(path) || strings.HasPrefix(filepath.ToSlash(path), "/")
	default:
		return filepath.IsAbs(path)
	}
}

// ResolveSearchPath resolves a search path against a working directory.
// If the path is empty, it returns the absolute path of the working directory.
// If the path is relative, it joins it with the working directory and returns
// the absolute path. Absolute paths are returned as-is (resolved to absolute).
func ResolveSearchPath(workingDir, searchPath string) (string, error) {
	if searchPath == "" {
		return filepath.Abs(workingDir)
	}

	if !SmartIsAbs(searchPath) {
		searchPath = filepath.Join(workingDir, searchPath)
	}

	return filepath.Abs(searchPath)
}
