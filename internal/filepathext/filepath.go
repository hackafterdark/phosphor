package filepathext

import (
	"path/filepath"
	"runtime"
	"strings"
)

// SmartJoin joins two paths, treating the second path as absolute if it is an
// absolute path.
func SmartJoin(one, two string) string {
	if SmartIsAbs(two) {
		return two
	}
	return filepath.Join(one, two)
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
