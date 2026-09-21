package pathguard

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/hackafterdark/phosphor/internal/filepathext"
	"github.com/stretchr/testify/require"
)

// TestCommandEscapesWorkspace_BackslashJoinBug verifies that paths containing
// backslashes are normalized to forward slashes before joining with the
// workspace root. This prevents filepath.Join from treating backslashes as
// literal characters in edge cases.
func TestCommandEscapesWorkspace_BackslashJoinBug(t *testing.T) {
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

// TestCommandEscapesWorkspace_RelativePathResolution verifies that relative paths
// extracted from commands are correctly resolved against the workspace root
// using ToSlash normalization + filepath.Clean.
func TestCommandEscapesWorkspace_RelativePathResolution(t *testing.T) {
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
			if runtime.GOOS != "windows" && strings.Contains(tc.path, "\\") {
				t.Skip("backslash path semantics are Windows-only")
			}
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

// TestCommandEscapesWorkspace_AbsolutePathClean verifies that absolute paths are
// cleaned (not joined) and that traversal attempts are caught.
func TestCommandEscapesWorkspace_AbsolutePathClean(t *testing.T) {
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

// TestCommandEscapesWorkspace_NonIOCommands verifies that ordinary build/test
// commands whose operands cannot escape the workspace pass validation without
// error, and that cd is recognised as a directory-change (not a file access).
func TestCommandEscapesWorkspace_NonIOCommands(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()

	for _, cmd := range []string{
		`go build ./cmd/petstore/...`,
		`go vet ./...`,
		`go test ./internal/agent -run TestFoo`,
		`git status`,
	} {
		require.NoError(t, ValidateCommandPaths(cmd, workspace), cmd)
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
			if runtime.GOOS != "windows" && strings.Contains(tok, "\\") {
				t.Skip("backslash path semantics are Windows-only")
			}
			require.Equal(t, want, isEscapablePathToken(tok), "token %q", tok)
		})
	}
}

// TestCommandEscapesWorkspace_CDCommandSkipped verifies that cd commands bypass
// path validation entirely. The shell's workspace boundary enforcement
// (updateShellFromRunner) already prevents cd from escaping the workspace.
func TestCommandEscapesWorkspace_CDCommandSkipped(t *testing.T) {
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

// TestCommandEscapesWorkspace_PathTraversalBlocked verifies that ".." traversal
// attempts are caught even when they look like they start inside the workspace.
func TestCommandEscapesWorkspace_PathTraversalBlocked(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()

	traversalAttempts := []string{
		"../etc/passwd",
		"../../Windows/System32",
		"..\\..\\..\\Windows\\System32",
		"./../../etc/passwd",
	}

	for _, path := range traversalAttempts {
		if runtime.GOOS != "windows" && strings.Contains(path, "\\") {
			continue
		}

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
			if runtime.GOOS != "windows" && strings.Contains(tc.command, "/c/") {
				t.Skip("unix-style Windows drive paths are Windows-only")
			}
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

// TestCommandEscapesWorkspace_BlocksEscapesRegardlessOfIOKeyword verifies that
// workspace-escaping paths are caught even when the command does not contain a
// recognised I/O keyword, closing the bypass where cp/tee/dd/redirections and
// UNC/drive/file-URL smuggety skipped validation entirely.
func TestCommandEscapesWorkspace_BlocksEscapesRegardlessOfIOKeyword(t *testing.T) {
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
			if runtime.GOOS != "windows" && strings.Contains(cmd, "\\") {
				t.Skip("backslash path semantics are Windows-only")
			}
			err := ValidateCommandPaths(cmd, workspace)
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
			require.NoError(t, ValidateCommandPaths(cmd, workspace))
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

// TestCommandEscapesWorkspace_BlocksTilde verifies that home-expansion paths are
// rejected: the shell expands "~" at execution time to the user profile, which
// lives outside the workspace, so the static checker must not trust it.
func TestCommandEscapesWorkspace_BlocksTilde(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()

	for _, cmd := range []string{
		`cat ~/secret.txt`,
		`cat ~/.ssh/id_ed25519`,
		`echo hi > ~/.bashrc`,
	} {
		t.Run(cmd, func(t *testing.T) {
			t.Parallel()
			require.Error(t, ValidateCommandPaths(cmd, workspace), cmd)
		})
	}

	require.NoError(t, ValidateCommandPaths(`cat ./notes.txt`, workspace))
}

// TestCommandEscapesWorkspace_BlocksEnvVarPaths verifies that a path embedding a
// home/system environment variable is rejected (its value is unknown until
// execution and points outside the workspace), while trusted temporary-directory
// variables, unrelated variables, and non-path uses are allowed.
func TestCommandEscapesWorkspace_BlocksEnvVarPaths(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()

	blocked := []string{
		`cat $HOME/.ssh/id_ed25519`,
		`cat ${HOME}/secrets`,
		`echo x > %USERPROFILE%\out.txt`,
		`cp a.txt $APPDATA\..\x`,
	}
	for _, cmd := range blocked {
		t.Run(cmd, func(t *testing.T) {
			t.Parallel()
			require.Error(t, ValidateCommandPaths(cmd, workspace), cmd)
		})
	}

	allowed := []string{
		`echo $BUILD_TAG`,          // no path separator, not a path
		`cat $CUSTOM_DIR/file.txt`, // unknown variable, not an outside target
		`go build -ldflags -X main.v=$VER`,
		`tee $TMPDIR/leak`, // the OS temporary directory is trusted by default
	}
	for _, cmd := range allowed {
		t.Run(cmd, func(t *testing.T) {
			t.Parallel()
			require.NoError(t, ValidateCommandPaths(cmd, workspace), cmd)
		})
	}
}

// TestConfinement_BlockedPostExpansion is the core Phase 1 suite. The argv
// passed to Blocked is what the interpreter hands the exec handler AFTER it has
// already substituted $VAR, $(…), backticks, globs and brace expansion, so
// these cases simulate the escapes that are invisible to the static,
// pre-expansion pass. In particular the {cat /etc/passwd} style argv is exactly
// what `cat $LEAK` becomes once the model sets LEAK=/etc/passwd in the same
// command.
func TestConfinement_BlockedPostExpansion(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	inside := filepath.Join(workspace, "sub", "f.go")
	outside := filepath.Join(filepath.Dir(filepath.Clean(os.TempDir())), "phosphor-pathguard-outside")
	require.NoError(t, os.MkdirAll(filepath.Dir(inside), 0o755))
	require.NoError(t, os.WriteFile(inside, []byte("x"), 0o644))

	conf := Confinement{WorkspaceRoot: workspace, TrustTempRoots: true}

	t.Run("program path itself is not inspected", func(t *testing.T) {
		require.NoError(t, conf.Blocked([]string{"/usr/bin/cat", "./sub/f.go"}, workspace))
	})
	t.Run("relative in-tree operand allowed", func(t *testing.T) {
		require.NoError(t, conf.Blocked([]string{"cat", "./sub/f.go"}, workspace))
	})
	t.Run("in-tree absolute operand allowed", func(t *testing.T) {
		require.NoError(t, conf.Blocked([]string{"cat", inside}, workspace))
	})
	t.Run("bare relative file allowed", func(t *testing.T) {
		require.NoError(t, conf.Blocked([]string{"echo", "hi"}, workspace))
	})
	t.Run("go style patterns allowed", func(t *testing.T) {
		require.NoError(t, conf.Blocked([]string{"go", "test", "./internal/agent", "-run", "TestFoo"}, workspace))
	})
	t.Run("remote url allowed", func(t *testing.T) {
		require.NoError(t, conf.Blocked([]string{"gh", "api", "https://api.github.com/x/y"}, workspace))
	})
	t.Run("expanded var resolving outside is blocked", func(t *testing.T) {
		// cat $LEAK  where the model set LEAK=/etc/passwd earlier in the line.
		err := conf.Blocked([]string{"cat", "/etc/passwd"}, workspace)
		require.Error(t, err)
		require.Contains(t, err.Error(), "outside workspace")
	})
	t.Run("absolute outside operand blocked", func(t *testing.T) {
		require.Error(t, conf.Blocked([]string{"cat", outside}, workspace))
	})
	t.Run("command substitution produced absolute blocked", func(t *testing.T) {
		// cp $(echo /root)secret ...  -> argv contains /root/secret after expansion.
		require.Error(t, conf.Blocked([]string{"cp", "/root/secret.txt", "./dst"}, workspace))
	})
	t.Run("tilde operand blocked", func(t *testing.T) {
		require.Error(t, conf.Blocked([]string{"cat", "~/.ssh/id_ed25519"}, workspace))
	})
	t.Run("traversal operand blocked", func(t *testing.T) {
		require.Error(t, conf.Blocked([]string{"cat", "../../../../etc/passwd"}, workspace))
	})
	t.Run("filesystem root argument blocked", func(t *testing.T) {
		// find /  ls /  rm -rf /  — the whole-FS recon/destroy vectors.
		require.Error(t, conf.Blocked([]string{"find", "/"}, workspace))
		require.Error(t, conf.Blocked([]string{"ls", "/"}, workspace))
		require.Error(t, conf.Blocked([]string{"rm", "-rf", "/"}, workspace))
	})
	t.Run("unquoted home env var blocked", func(t *testing.T) {
		require.Error(t, conf.Blocked([]string{"cat", "$HOME/x"}, workspace))
	})
}

// TestConfinement_DeviceFiles covers the well-known device nodes that stay
// reachable even though they are written as absolute paths.
func TestConfinement_DeviceFiles(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	conf := Confinement{WorkspaceRoot: workspace, TrustTempRoots: true}

	for _, dev := range []string{"/dev/null", "/dev/stdin", "/dev/stdout", "/dev/stderr", "/dev/zero"} {
		t.Run(dev, func(t *testing.T) {
			require.NoError(t, conf.Blocked([]string{"tee", dev}, workspace))
			require.NoError(t, conf.Blocked([]string{"cmd", ">", dev}, workspace))
		})
	}
}

// TestConfinement_TempRootIsTrusted verifies the default OS temp directory is a
// trusted root, and that disabling the trust re-confines it.
func TestConfinement_TempRootIsTrusted(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	tempFile := filepath.Join(os.TempDir(), "phosphor-confinement-probe")
	require.NoError(t, os.WriteFile(tempFile, []byte("x"), 0o644))

	trusted := Confinement{WorkspaceRoot: workspace, TrustTempRoots: true}
	require.NoError(t, trusted.Blocked([]string{"cp", "./src", tempFile}, workspace),
		"writes into the OS temp dir must be permitted when temp trust is on")

	untrusted := Confinement{WorkspaceRoot: workspace, TrustTempRoots: false}
	require.Error(t, untrusted.Blocked([]string{"cp", "./src", tempFile}, workspace),
		"the same path must be confined once temp trust is disabled")
}

// TestConfinement_ExtraRoots verifies the config-driven trusted roots work.
func TestConfinement_ExtraRoots(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	extra := t.TempDir()
	extraFile := filepath.Join(extra, "shared", "lib.go")
	require.NoError(t, os.MkdirAll(filepath.Dir(extraFile), 0o755))

	conf := Confinement{WorkspaceRoot: workspace, ExtraRoots: []string{extra}}
	require.NoError(t, conf.Blocked([]string{"cp", extraFile, "./dst"}, workspace))
	// Without the extra root the very same path is confined.
	strict := Confinement{WorkspaceRoot: workspace}
	require.Error(t, strict.Blocked([]string{"cp", extraFile, "./dst"}, workspace))
}

// TestConfinement_DisabledWhenNoWorkspace pins the trusted hook runner case: a
// zero-value Confinement (empty WorkspaceRoot) never blocks anything.
func TestConfinement_DisabledWhenNoWorkspace(t *testing.T) {
	t.Parallel()

	var conf Confinement
	require.NoError(t, conf.Blocked([]string{"cat", "/etc/passwd"}, "/anywhere"))
	require.NoError(t, conf.Blocked([]string{"rm", "-rf", "/"}, "/"))
}

// TestConfinement_TempVarResolvesOutsideStillBlocked pins that trusting temp
// roots does not become a wildcard for temp environment variables: a literal
// $HOME reference is always rejected and a temp-variable reference is only
// tolerated when temp trust is enabled.
func TestConfinement_TempVarResolvesOutsideStillBlocked(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()

	// HOME is never tolerated.
	require.Error(t, Confinement{WorkspaceRoot: workspace}.Blocked([]string{"cat", "$HOME/x"}, workspace))
	require.Error(t, Confinement{WorkspaceRoot: workspace, TrustTempRoots: true}.Blocked([]string{"cat", "$HOME/x"}, workspace))

	// A literal (unexpanded) temp variable is tolerated only with temp trust.
	require.NoError(t, Confinement{WorkspaceRoot: workspace, TrustTempRoots: true}.Blocked([]string{"cat", "$TMPDIR/x"}, workspace))
	require.Error(t, Confinement{WorkspaceRoot: workspace, TrustTempRoots: false}.Blocked([]string{"cat", "$TMPDIR/x"}, workspace))
}

// TestValidateCommandPaths_ExpansionAware is the regression suite for the
// expansion bypass class: a path that only becomes absolute after $VAR /
// inline-assignment substitution — including paths reached through shell
// redirection operands and library-native builtins (source/.) that the argv-only
// Confinement.Blocked never inspects. It is written to be meaningful on every
// operating system: the out-of-workspace target is derived from the live temp
// layout so it is correct on whatever platform runs the suite. The two cases
// that hinge on a Windows drive variable are gated to GOOS=windows.
func TestValidateCommandPaths_ExpansionAware(t *testing.T) {
	// Not t.Parallel(): the ambient-environment cases use t.SetEnv.
	workspace := t.TempDir()

	// An absolute directory that provably sits outside the workspace on any OS.
	outside := filepath.Join(filepath.Dir(filepath.Clean(os.TempDir())), "phosphor-pathguard-vector")
	slash := filepath.ToSlash(outside)

	// An ambient variable whose value is an out-of-workspace absolute path but
	// which is deliberately absent from the known-outside allowlist (so the old,
	// allowlist-only classifier let it through).
	t.Setenv("PHOSPHOR_VECTOR_LEAK", slash)
	// An unset variable that must keep resolving as "not an outside target".
	const unsetVar = "PHOSPHOR_VECTOR_UNSET_DOES_NOT_EXIST_9F3A2C"

	t.Run("inline assignment feeding jq file operand", func(t *testing.T) {
		require.Error(t, ValidateCommandPaths("d="+slash+"; jq -r -R . $d/hosts", workspace))
	})
	t.Run("inline assignment feeding input redirection", func(t *testing.T) {
		require.Error(t, ValidateCommandPaths("d="+slash+"; cat < $d/hosts", workspace))
	})
	t.Run("inline assignment feeding source/dot operand", func(t *testing.T) {
		require.Error(t, ValidateCommandPaths("d="+slash+"; . $d/hosts", workspace))
	})
	t.Run("nested assignment feeding path-prefixed dispatch", func(t *testing.T) {
		require.Error(t, ValidateCommandPaths("d="+slash+"; p=$d/hosts; \"$p\"", workspace))
	})
	t.Run("inline assignment feeding output redirection", func(t *testing.T) {
		require.Error(t, ValidateCommandPaths("d="+slash+"; echo x > $d/out.txt", workspace))
	})
	t.Run("ambient non-allowlisted var as file operand", func(t *testing.T) {
		require.Error(t, ValidateCommandPaths("cat $PHOSPHOR_VECTOR_LEAK/hosts", workspace))
	})
	t.Run("ambient non-allowlisted var as redirection operand", func(t *testing.T) {
		require.Error(t, ValidateCommandPaths("cat < $PHOSPHOR_VECTOR_LEAK/hosts", workspace))
	})
	t.Run("ambient var via braces form", func(t *testing.T) {
		require.Error(t, ValidateCommandPaths("cat ${PHOSPHOR_VECTOR_LEAK}/hosts", workspace))
	})

	t.Run("unset variable is not an outside target", func(t *testing.T) {
		require.NoError(t, ValidateCommandPaths("cat $PHOSPHOR_VECTOR_UNSET_DOES_NOT_EXIST_9F3A2C/file.txt", workspace))
	})
	t.Run("in-workspace relative operands stay allowed", func(t *testing.T) {
		require.NoError(t, ValidateCommandPaths("cat ./notes.txt", workspace))
		require.NoError(t, ValidateCommandPaths("jq . ./data.json", workspace))
		require.NoError(t, ValidateCommandPaths("echo hi > notes/out.txt", workspace))
		require.NoError(t, ValidateCommandPaths("go build ./cmd/petstore/...", workspace))
	})
	t.Run("trusted temp variables stay allowed", func(t *testing.T) {
		require.NoError(t, ValidateCommandPaths("cat $TMP/file", workspace))
		require.NoError(t, ValidateCommandPaths("tee $TMPDIR/leak", workspace))
	})
	t.Run("bare home variable without a path is not rewritten", func(t *testing.T) {
		// "$HOME" alone carries no separator, so it is never treated as a path
		// operand; unrelated arguments must not be inflated into absolute paths.
		require.NoError(t, ValidateCommandPaths(`grep $HOME $PHOSPHOR_VECTOR_UNSET_DOES_NOT_EXIST_9F3A2C`, workspace))
	})
	t.Run("device file redirection targets stay allowed", func(t *testing.T) {
		// The classic "2>/dev/null" discard redirect must not be flagged as an
		// out-of-workspace write, on any OS. On Windows the validator resolves
		// it to a rooted \dev\null form, which the device-file allowlist maps
		// back before the bounds check runs.
		require.NoError(t, ValidateCommandPaths("rg pattern . 2>/dev/null", workspace))
		require.NoError(t, ValidateCommandPaths("make > /dev/null 2>&1", workspace))
		require.NoError(t, ValidateCommandPaths("tee /dev/null < ./notes.txt", workspace))
		require.NoError(t, ValidateCommandPaths(`echo x > "\dev\null"`, workspace))
		require.NoError(t, ValidateCommandPaths("echo x > nul", workspace))
	})

	if runtime.GOOS == "windows" {
		t.Run("windows system root var reached via redirection", func(t *testing.T) {
			require.Error(t, ValidateCommandPaths(`cat < $SystemRoot\System32\drivers\etc\hosts`, workspace))
		})
		t.Run("windows drive via inline assignment and jq", func(t *testing.T) {
			require.Error(t, ValidateCommandPaths(`d=C:\Windows\System32\drivers\etc; jq -r -R . $d/hosts`, workspace))
		})
	} else {
		t.Run("posix etc/passwd literal stays blocked", func(t *testing.T) {
			require.Error(t, ValidateCommandPaths("cat /etc/passwd", workspace))
		})
		t.Run("posix home var reached via redirection is blocked", func(t *testing.T) {
			require.Error(t, ValidateCommandPaths("cat < $HOME/.ssh/id_ed25519", workspace))
		})
	}
}
