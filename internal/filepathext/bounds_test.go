package filepathext

import (
	"os"
	"path/filepath"
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
			name:         "case-different paths on case-insensitive FS",
			path:         "/Home/User/Project/src/main.go",
			workspace:    "/home/user/project",
			expectInside: true,
		},
		{
			name:         "case-different workspace root",
			path:         "/home/user/PROJECT/src/main.go",
			workspace:    "/home/user/project",
			expectInside: true,
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
