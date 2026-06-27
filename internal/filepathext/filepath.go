package filepathext

import (
	"errors"
	"path/filepath"
	"runtime"
	"strings"
)

// SmartJoin joins two paths, treating the second path as absolute if it is an
// absolute path. It does not validate against a workspace — use ValidatePath
// separately for that. Use UnsafeSmartJoin for trusted extensions (e.g. MCP
// servers) that legitimately cross workspace bounds.
func SmartJoin(one, two string) string {
	if SmartIsAbs(two) {
		return two
	}
	return filepath.Join(one, two)
}

// UnsafeSmartJoin is an alias for SmartJoin, provided for clarity when the
// caller intends to bypass workspace validation. Only use this for trusted
// extensions (e.g. MCP servers) that legitimately need cross-workspace access.
func UnsafeSmartJoin(one, two string) string {
	return SmartJoin(one, two)
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
