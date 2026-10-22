package filepathext

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsInside(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		path         string
		workspace    string
		expectInside bool
	}{
		{
			name:         "same directory",
			path:         "/home/user/project",
			workspace:    "/home/user/project",
			expectInside: true,
		},
		{
			name:         "nested file inside workspace",
			path:         "/home/user/project/src/main.go",
			workspace:    "/home/user/project",
			expectInside: true,
		},
		{
			name:         "nested directory inside workspace",
			path:         "/home/user/project/src/pkg",
			workspace:    "/home/user/project",
			expectInside: true,
		},
		{
			name:         "parent directory outside workspace",
			path:         "/home/user",
			workspace:    "/home/user/project",
			expectInside: false,
		},
		{
			name:         "completely unrelated path",
			path:         "/etc/passwd",
			workspace:    "/home/user/project",
			expectInside: false,
		},
		{
			name:         "empty path",
			path:         "",
			workspace:    "/home/user/project",
			expectInside: false,
		},
		{
			name:         "empty workspace",
			path:         "/home/user/project/src/main.go",
			workspace:    "",
			expectInside: false,
		},
		{
			// Case-folding only applies on case-insensitive filesystems.
			// These roots do not exist on disk, so the probe conservatively
			// treats them as case-sensitive (strict) everywhere except
			// Windows, where the filesystem is case-insensitive by contract.
			name:         "case-different paths on case-insensitive FS",
			path:         "/Home/User/Project/src/main.go",
			workspace:    "/home/user/project",
			expectInside: runtime.GOOS == "windows",
		},
		{
			name:         "case-different workspace root",
			path:         "/home/user/PROJECT/src/main.go",
			workspace:    "/home/user/project",
			expectInside: runtime.GOOS == "windows",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.expectInside, IsInside(tt.path, tt.workspace))
		})
	}
}

func TestIsInside_RealPaths(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()

	// Create a nested structure
	nested := filepath.Join(tmpDir, "sub", "deep")
	require.NoError(t, os.MkdirAll(nested, 0o755))

	inside := IsInside(filepath.Join(tmpDir, "sub", "deep", "file.txt"), tmpDir)
	require.True(t, inside, "nested file should be inside workspace")

	outside := IsInside(filepath.Join(os.TempDir(), "outside.txt"), tmpDir)
	require.False(t, outside, "temp dir file should be outside workspace")

	sibling := IsInside(filepath.Join(os.TempDir()), tmpDir)
	require.False(t, sibling, "parent of workspace should be outside")
}

func TestIsInside_CaseInsensitiveFilesystems(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()

	// Re-spell the workspace root in a different case. On a case-insensitive
	// filesystem (Windows, default macOS APFS) this denotes the same
	// directory and must pass the bounds check; on a case-sensitive one it is
	// a different path and must not.
	flipped := flipCase(tmpDir)
	require.NotEqual(t, tmpDir, flipped)

	inside := IsInside(filepath.Join(flipped, "sub", "main.go"), tmpDir)
	require.Equal(t, caseInsensitiveFS(tmpDir), inside,
		"case folding must match the hosting filesystem's case behavior")
}

// flipCase flips the case of the first ASCII letter in p.
func flipCase(p string) string {
	for i := 0; i < len(p); i++ {
		c := p[i]
		if c >= 'A' && c <= 'Z' {
			return p[:i] + string(c+32) + p[i+1:]
		}
		if c >= 'a' && c <= 'z' {
			return p[:i] + string(c-32) + p[i+1:]
		}
	}
	return p
}

func TestIsInside_UncPaths(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("UNC paths are Windows-only")
	}
	t.Parallel()

	const (
		localWorkspace = `C:\workspace\project`
		remoteBase     = `\\server\share\project`
	)

	require.False(t, IsInside(`\\server\share\secret.txt`, localWorkspace),
		"UNC paths are outside a local workspace")
	require.True(t, IsInside(remoteBase+`\src\main.go`, remoteBase),
		"a path under the same UNC workspace should be inside")
	require.False(t, IsInside(remoteBase+`\outside\file.txt`, localWorkspace),
		"UNC paths must not be compared against a local drive root")
}
