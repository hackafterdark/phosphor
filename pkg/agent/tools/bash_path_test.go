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

// TestValidateCommandPaths_NonIOCommands verifies that ordinary build/test
// commands whose operands cannot escape the workspace pass validation without
// error, and that cd is recognised as a directory-change (not a file access).
func TestValidateCommandPaths_NonIOCommands(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()

	for _, cmd := range []string{
		`go build ./cmd/petstore/...`,
		`go vet ./...`,
		`go test ./internal/agent -run TestFoo`,
		`git status`,
	} {
		require.NoError(t, validateCommandPaths(cmd, workspace), cmd)
	}

	require.True(t, isCDCommand("cd F:/some/path"),
		"'cd' should be detected as a cd command")
	require.True(t, isCDCommand("cd .."))
	require.False(t, isCDCommand("go build ./cmd/..."),
		"'go build' should not be detected as cd")
}

// TestIsEscapablePathToken pins the token classifier that decides which
// tokens are worth resolving against the workspace bounds. Only tokens that
// could actually address an outside location (traversal, absolute, UNC or a
// leading "~") are escapable; ordinary words, in-tree relative operands and
// import paths are not.
func TestIsEscapablePathToken(t *testing.T) {
	t.Parallel()

	cases := map[string]bool{
		"file.txt":                     false,
		"cmd/petstore":                 false,
		"github.com/x/y":               false,
		"./cmd/petstore/...":           false,
		"s/a/b":                        false,
		"../../etc/passwd":             true,
		"/etc/passwd":                  true,
		`~/secret/id`:                  true,
		`\\host\share\x`:               true,
		"//host/share/x":               true,
		`.\\..\\..\\Windows\\System32`: true,
	}

	for tok, want := range cases {
		t.Run(tok, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, want, isEscapablePathToken(tok), "token %q", tok)
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

// TestCorrectCommandPaths_PreservesURLsDomainsAndRelative is a regression
// suite for the old regex-based pass, which corrupted any token containing a
// slash by stripping its leading segment and rewriting the remainder against
// the workspace. That broke remote URLs (https://… turned into a bogus "s:…"
// drive path), Go import paths (github.com/x/y), relative operands (./…) and
// quoted JSON. These inputs must now pass through byte-for-byte unchanged.
func TestCorrectCommandPaths_PreservesURLsDomainsAndRelative(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()

	unchanged := []string{
		`go build ./cmd/petstore/...`,
		`go get github.com/x/y`,
		`go test ./internal/agent -run TestFoo`,
		`cd probe; go run .`,
		`git clone github.com/o/r.git dest/`,
		`echo "hello world" > out.txt`,
		`cat cmd/petstore/main.go`,
		`gh api "https://api.github.com/repos/hackafterdark/phosphor/code-scanning/alerts?state=open&per_page=100" --jq '.[]' 2>&1 | head -c 6000`,
	}

	for _, cmd := range unchanged {
		t.Run(cmd, func(t *testing.T) {
			t.Parallel()
			got := CorrectCommandPaths(cmd, workspace)
			require.Equal(t, cmd, got, "command must not be rewritten")
		})
	}
}

// TestValidateCommandPaths_BlocksEscapesRegardlessOfIOKeyword verifies that
// workspace-escaping paths are caught even when the command does not contain a
// recognised I/O keyword, closing the bypass where cp/tee/dd/redirections and
// UNC/drive/file-URL smuggety skipped validation entirely.
func TestValidateCommandPaths_BlocksEscapesRegardlessOfIOKeyword(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()

	blocked := []string{
		`cat ../../etc/passwd`,
		`cat /etc/passwd`,
		`echo x > ../../etc/cron`,
		`cp secret.txt ../../etc/x`,
		`echo pwned | tee ../../etc/y`,
		`cp a.txt ../../root/.ssh/id_ed25519`,
		`cat //169.254.255.205/c$/win.ini`,
		`cat "\\server\share\secret.txt"`,
		`cat junk:/../../../etc/passwd`,
		`cat file://../../../etc/passwd`,
		`sed -i s/a/b/ /etc/passwd`,
		`tar -czf ../../etc/x.tar.gz .`,
	}
	for _, cmd := range blocked {
		t.Run("block:"+cmd, func(t *testing.T) {
			t.Parallel()
			err := validateCommandPaths(cmd, workspace)
			require.Error(t, err, "expected escape to be blocked")
			require.Contains(t, err.Error(), "outside workspace")
		})
	}

	allowed := []string{
		`go build ./cmd/petstore/...`,
		`go test ./internal/agent -run TestFoo`,
		`go get github.com/x/y`,
		`git clone github.com/o/r.git dest/`,
		`cat cmd/petstore/main.go`,
		`echo hi > notes/out.txt`,
		`gh api repos/hackafterdark/phosphor/code-scanning/alerts --jq '.[]' 2>&1`,
		`gh api "https://api.github.com/repos/x/y?per_page=100" 2>&1 | head -c 6000`,
	}
	for _, cmd := range allowed {
		t.Run("allow:"+cmd, func(t *testing.T) {
			t.Parallel()
			require.NoError(t, validateCommandPaths(cmd, workspace))
		})
	}
}

// TestIsRemoteURL pins the classifier that separates genuine remote URLs from
// lookalikes. A scheme is only trusted when it is in the allowlist and the
// token carries no ".." traversal, so file:// and traversal-bearing URLs are
// returned as false and remain subject to path validation.
func TestIsRemoteURL(t *testing.T) {
	t.Parallel()

	cases := map[string]bool{
		`https://api.github.com/repos/x/y`: true,
		`http://example.com/a/b`:           true,
		`git://host/repo.git`:              true,
		`https://x/../../etc/passwd`:       false, // traversal disqualifies
		`file:///etc/passwd`:               false, // file:// never trusted
		`s3://bucket/key`:                  false, // not in allowlist
		`github.com/x/y`:                   false, // import path, no scheme
		`repos/x/y`:                        false, // API path without leading slash
		`//host/share/x`:                   false, // UNC is not a URL
	}
	for tok, want := range cases {
		t.Run(tok, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, want, isRemoteURL(tok))
		})
	}
}

// TestValidateCommandPaths_BlocksTilde verifies that home-expansion paths are
// rejected: the shell expands "~" at execution time to the user profile, which
// lives outside the workspace, so the static checker must not trust it.
func TestValidateCommandPaths_BlocksTilde(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()

	for _, cmd := range []string{
		`cat ~/secret.txt`,
		`cat ~/.ssh/id_ed25519`,
		`echo hi > ~/.bashrc`,
	} {
		t.Run(cmd, func(t *testing.T) {
			t.Parallel()
			require.Error(t, validateCommandPaths(cmd, workspace), cmd)
		})
	}

	require.NoError(t, validateCommandPaths(`cat ./notes.txt`, workspace))
}

// TestValidateCommandPaths_BlocksEnvVarPaths verifies that a path embedding a
// home/temp/system environment variable is rejected (its value is unknown
// until execution and points outside the workspace), while unrelated variables
// and non-path uses are allowed.
func TestValidateCommandPaths_BlocksEnvVarPaths(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()

	blocked := []string{
		`cat $HOME/.ssh/id_ed25519`,
		`cat ${HOME}/secrets`,
		`echo x > %USERPROFILE%\out.txt`,
		`tee $TMPDIR/leak`,
		`cp a.txt $APPDATA\..\x`,
	}
	for _, cmd := range blocked {
		t.Run(cmd, func(t *testing.T) {
			t.Parallel()
			require.Error(t, validateCommandPaths(cmd, workspace), cmd)
		})
	}

	allowed := []string{
		`echo $BUILD_TAG`,          // no path separator, not a path
		`cat $CUSTOM_DIR/file.txt`, // unknown variable, not an outside target
		`go build -ldflags -X main.v=$VER`,
	}
	for _, cmd := range allowed {
		t.Run(cmd, func(t *testing.T) {
			t.Parallel()
			require.NoError(t, validateCommandPaths(cmd, workspace), cmd)
		})
	}
}
