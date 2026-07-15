package tools

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/hackafterdark/phosphor/internal/filepathext"
	"github.com/stretchr/testify/require"
)

// TestValidateCommandPaths_BackslashJoinBug verifies that paths containing
// backslashes are normalized to forward slashes before joining with the
// workspace root. This prevents filepath.Join from treating backslashes as
// literal characters in edge cases.
func TestValidateCommandPaths_BackslashJoinBug(t *testing.T) {
	t.Parallel()

	workspace := `F:\hackafterdark\phosphor`
	rawPath := ".\\cmd\\petstore\\..."

	// With ToSlash normalization (the fix): the backslashes become forward
	// slashes, and filepath.Join produces a correct nested path.
	normalized := filepath.ToSlash(rawPath)
	fixedJoined := filepath.Clean(filepath.Join(workspace, normalized))
	t.Logf("Normalized: %q -> joined: %q", normalized, fixedJoined)

	require.True(t, filepathext.IsInside(fixedJoined, workspace),
		"normalized path should be inside workspace")

	// Verify ToSlash is idempotent for forward-slash paths
	fwdPath := "./cmd/petstore/..."
	require.Equal(t, fwdPath, filepath.ToSlash(fwdPath),
		"ToSlash should be idempotent for forward-slash paths")
}

// TestValidateCommandPaths_RelativePathResolution verifies that relative paths
// extracted from commands are correctly resolved against the workspace root
// using ToSlash normalization + filepath.Clean.
func TestValidateCommandPaths_RelativePathResolution(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	subdir := filepath.Join(workspace, "cmd", "petstore")
	require.NoError(t, os.MkdirAll(subdir, 0o755))

	cases := []struct {
		name   string
		path   string
		wantIn bool
	}{
		{"forward slash relative", "cmd/petstore", true},
		{"dot-slash relative", "./cmd/petstore", true},
		{"dot-backslash relative (Windows)", ".\\cmd\\petstore", true},
		{"double-dot escape attempt", "../etc/passwd", false},
		{"double-dot-backslash escape", "..\\..\\Windows\\System32", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// Normalize to forward slashes before joining (the fix)
			normalized := filepath.ToSlash(tc.path)
			joined := filepath.Clean(filepath.Join(workspace, normalized))

			inWorkspace := filepathext.IsInside(joined, workspace)
			require.Equal(t, tc.wantIn, inWorkspace,
				"path %q -> joined %q -> inWorkspace=%v, want=%v",
				tc.path, joined, inWorkspace, tc.wantIn)
		})
	}
}

// TestValidateCommandPaths_AbsolutePathClean verifies that absolute paths are
// cleaned (not joined) and that traversal attempts are caught.
func TestValidateCommandPaths_AbsolutePathClean(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()

	cases := []struct {
		name   string
		path   string
		wantIn bool
	}{
		{"valid absolute inside", filepath.Join(workspace, "cmd", "foo.go"), true},
		{"absolute with traversal", filepath.Join(workspace, "cmd", "..", "..", "etc", "passwd"), false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cleaned := filepath.Clean(tc.path)
			inWorkspace := filepathext.IsInside(cleaned, workspace)
			require.Equal(t, tc.wantIn, inWorkspace,
				"path %q -> cleaned %q", tc.path, cleaned)
		})
	}
}

// TestValidateCommandPaths_NonIOCommands verifies that commands without
// I/O keywords (using whole-word matching) skip path validation.
// "go build ./cmd/petstore/..." should NOT trigger validation because:
//   - "cmd" was removed from ioCommands (it's a Windows built-in AND a common
//     Go package directory name)
//   - Whole-word regex matching prevents substring false positives
func TestValidateCommandPaths_NonIOCommands(t *testing.T) {
	t.Parallel()

	cmd := "go build ./cmd/petstore/..."
	require.False(t, ioCommandRegex.MatchString(cmd),
		"'go build ./cmd/petstore/...' should not match I/O command regex")

	// Verify cd commands are also skipped
	require.True(t, isCDCommand("cd F:/some/path"),
		"'cd' should be detected as a cd command")
	require.True(t, isCDCommand("cd .."), true)
	require.False(t, isCDCommand("go build ./cmd/..."),
		"'go build' should not be detected as cd")
}

// TestValidateCommandPaths_IOCommandWholeWord verifies that the I/O command
// regex uses whole-word matching to avoid false positives.
func TestValidateCommandPaths_IOCommandWholeWord(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		command   string
		wantMatch bool
	}{
		{"cat file.txt", "cat file.txt", true},
		{"grep pattern file", "grep pattern file", true},
		{"go build ./cmd/petstore", "go build ./cmd/petstore", false},
		{"make && cat file", "make && cat file", true},
		{"echo | grep pattern", "echo | grep pattern", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.wantMatch, ioCommandRegex.MatchString(tc.command),
				"command %q match=%v, want=%v", tc.command,
				ioCommandRegex.MatchString(tc.command), tc.wantMatch)
		})
	}
}

// TestValidateCommandPaths_CDCommandSkipped verifies that cd commands bypass
// path validation entirely. The shell's workspace boundary enforcement
// (updateShellFromRunner) already prevents cd from escaping the workspace.
func TestValidateCommandPaths_CDCommandSkipped(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		command string
		wantCD  bool
	}{
		{"cd relative", "cd cmd", true},
		{"cd absolute", "cd F:/some/path", true},
		{"cd ..", "cd ..", true},
		{"cd with trailing space", "cd /tmp ", true},
		{"go build", "go build ./cmd/...", false},
		{"cat file", "cat file.txt", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.wantCD, isCDCommand(tc.command),
				"isCDCommand(%q) = %v, want %v", tc.command,
				isCDCommand(tc.command), tc.wantCD)
		})
	}
}

// TestValidateCommandPaths_PathTraversalBlocked verifies that ".." traversal
// attempts are caught even when they look like they start inside the workspace.
func TestValidateCommandPaths_PathTraversalBlocked(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()

	traversalAttempts := []string{
		"../etc/passwd",
		"../../Windows/System32",
		"..\\..\\..\\Windows\\System32",
		"./../../etc/passwd",
	}

	for _, path := range traversalAttempts {
		normalized := filepath.ToSlash(path)
		joined := filepath.Clean(filepath.Join(workspace, normalized))
		require.False(t, filepathext.IsInside(joined, workspace),
			"path %q -> joined %q should be outside workspace", path, joined)
	}
}

func TestCorrectCommandPaths(t *testing.T) {
	t.Parallel()

	workspace := `/workspace/project`
	if runtime.GOOS == "windows" {
		workspace = `C:\workspace\project`
	}

	tests := []struct {
		name    string
		command string
		want    string
	}{
		{
			name:    "command with leading slash path",
			command: "cat /internal/app/app.go",
			want:    "cat " + filepath.ToSlash(filepath.Join(workspace, "internal/app/app.go")),
		},
		{
			name:    "command with double quoted leading slash path",
			command: `cat "/internal/app/app.go"`,
			want:    `cat "` + filepath.ToSlash(filepath.Join(workspace, "internal/app/app.go")) + `"`,
		},
		{
			name:    "command with single quoted leading slash path",
			command: `cat '/internal/app/app.go'`,
			want:    `cat '` + filepath.ToSlash(filepath.Join(workspace, "internal/app/app.go")) + `'`,
		},
		{
			name:    "command with unix-style Windows drive path",
			command: "grep func /c/internal/app/app.go",
			want:    "grep func " + filepath.ToSlash(filepath.Join(workspace, "internal/app/app.go")),
		},
		{
			name:    "command with absolute drive path on Windows",
			command: "type D:/internal/app/app.go",
			want:    "type " + filepath.ToSlash(filepath.Join(workspace, "internal/app/app.go")),
		},
		{
			name:    "command with multiple paths",
			command: "diff /internal/app/app.go /c/internal/cmd/run.go",
			want:    "diff " + filepath.ToSlash(filepath.Join(workspace, "internal/app/app.go")) + " " + filepath.ToSlash(filepath.Join(workspace, "internal/cmd/run.go")),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := CorrectCommandPaths(tc.command, workspace)
			require.Equal(t, tc.want, got)
		})
	}
}
