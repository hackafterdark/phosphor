package tools

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hackafterdark/phosphor/pkg/config"
	"github.com/hackafterdark/phosphor/pkg/otel"
	"github.com/stretchr/testify/require"
	otelgo "go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

func TestValidateGitInvocation_UnknownSubcommand(t *testing.T) {
	t.Parallel()
	_, _, err := validateGitInvocation("filter-branch", nil, t.TempDir())
	require.Error(t, err)
	_, _, err = validateGitInvocation("config", nil, t.TempDir())
	require.Error(t, err)
	_, _, err = validateGitInvocation("credential", nil, t.TempDir())
	require.Error(t, err)
}

func TestValidateGitInvocation_DisallowedFlags(t *testing.T) {
	t.Parallel()
	wd := t.TempDir()
	for _, tc := range []struct {
		sub  string
		args []string
	}{
		{"status", []string{"--help"}},
		{"log", []string{"--config"}},
		{"push", []string{"--force"}},
		{"push", []string{"-f"}},
		{"push", []string{"--force-with-lease"}},
		{"push", []string{"--exec"}},
		{"commit", []string{"--amend"}},
		{"branch", []string{"-d", "topic"}},
		{"tag", []string{"-d", "v1"}},
		{"diff", []string{"--ext-diff"}},
		{"merge", []string{"--exec"}},
	} {
		_, _, err := validateGitInvocation(tc.sub, tc.args, wd)
		require.Error(t, err, "%s %v", tc.sub, tc.args)
	}
}

func TestValidateGitInvocation_ReadOnlyTiers(t *testing.T) {
	t.Parallel()
	wd := t.TempDir()
	cases := []struct {
		sub  string
		args []string
	}{
		{"status", nil},
		{"status", []string{"-s", "-b"}},
		{"log", []string{"--oneline", "-n", "5"}},
		{"diff", []string{"--stat"}},
		{"diff", []string{"HEAD~1", "--", "file.txt"}},
		{"show", []string{"HEAD"}},
		{"branch", []string{"-a"}},
		{"remote", []string{"-v"}},
		{"tag", nil},
		{"tag", []string{"-l"}},
		{"rev-parse", []string{"--short", "HEAD"}},
		{"stash", []string{"list"}},
	}
	for _, tc := range cases {
		argv, tier, err := validateGitInvocation(tc.sub, tc.args, wd)
		require.NoError(t, err, "%s %v", tc.sub, tc.args)
		require.Equal(t, gitTierReadOnly, tier, "%s %v", tc.sub, tc.args)
		require.Equal(t, tc.sub, argv[0])
	}
}

func TestValidateGitInvocation_LocalTiers(t *testing.T) {
	t.Parallel()
	wd := t.TempDir()
	cases := []struct {
		sub  string
		args []string
		tier gitTier
	}{
		{"add", []string{"file.txt"}, gitTierLocal},
		{"commit", []string{"-m", "msg"}, gitTierLocal},
		{"branch", []string{"new-topic"}, gitTierLocal},
		{"tag", []string{"v1.2.3"}, gitTierLocal},
		{"checkout", []string{"-b", "new-topic"}, gitTierLocal},
		{"stash", nil, gitTierLocal},
		{"stash", []string{"pop"}, gitTierLocal},
		{"fetch", []string{"origin"}, gitTierLocal},
	}
	for _, tc := range cases {
		_, tier, err := validateGitInvocation(tc.sub, tc.args, wd)
		require.NoError(t, err, "%s %v", tc.sub, tc.args)
		require.Equal(t, tc.tier, tier, "%s %v", tc.sub, tc.args)
	}
}

func TestValidateGitInvocation_GatedTiers(t *testing.T) {
	t.Parallel()
	wd := t.TempDir()
	cases := []struct {
		sub  string
		args []string
	}{
		{"checkout", []string{"topic"}},
		{"checkout", []string{"--", "file.txt"}},
		{"pull", []string{"origin"}},
		{"merge", []string{"topic"}},
		{"rebase", []string{"origin/main"}},
		{"reset", []string{"--hard"}},
		{"clean", []string{"-f", "-d"}},
	}
	for _, tc := range cases {
		_, tier, err := validateGitInvocation(tc.sub, tc.args, wd)
		require.NoError(t, err, "%s %v", tc.sub, tc.args)
		require.Equal(t, gitTierGated, tier, "%s %v", tc.sub, tc.args)
	}
}

func TestValidateGitInvocation_PathBoundaries(t *testing.T) {
	t.Parallel()
	wd := t.TempDir()

	// Workspace-external paths are rejected.
	_, _, err := validateGitInvocation("add", []string{"../../etc/passwd"}, wd)
	require.Error(t, err)
	_, _, err = validateGitInvocation("add", []string{filepath.Join(t.TempDir(), "outside.txt")}, wd)
	require.Error(t, err)

	// Paths inside the workspace are accepted and separated by `--`.
	argv, _, err := validateGitInvocation("add", []string{"a/b/c.txt"}, wd)
	require.NoError(t, err)
	require.Equal(t, []string{"add", "--", "a/b/c.txt"}, argv)

	// A dash-prefixed filename cannot masquerade as a flag: it fails
	// flag validation before reaching the path allowlist.
	_, _, err = validateGitInvocation("add", []string{"-rf"}, wd)
	require.Error(t, err)
}

func TestValidateGitInvocation_RevPathSeparator(t *testing.T) {
	t.Parallel()
	wd := t.TempDir()

	// `--` after the separator validates paths; before it validates revs.
	argv, _, err := validateGitInvocation("log", []string{"--oneline", "HEAD", "--", "src/main.go"}, wd)
	require.NoError(t, err)
	require.Equal(t, []string{"log", "--oneline", "HEAD", "--", "src/main.go"}, argv)

	// Without `--`, positionals must be revs; path-like input is accepted
	// as a rev only (git interprets unseparated args itself).
	_, _, err = validateGitInvocation("log", []string{"--oneline", "--", "..", "file"}, wd)
	require.Error(t, err) // ".." fails rev safety

	// Double separator is rejected.
	_, _, err = validateGitInvocation("diff", []string{"--", "a", "--", "b"}, wd)
	require.Error(t, err)
}

func TestValidateGitInvocation_RemotePinning(t *testing.T) {
	t.Parallel()
	wd := t.TempDir()

	// Missing remote defaults to origin.
	argv, _, err := validateGitInvocation("fetch", nil, wd)
	require.NoError(t, err)
	require.Equal(t, []string{"fetch", "origin"}, argv)

	// Explicit origin is allowed; anything else is rejected.
	argv, _, err = validateGitInvocation("push", []string{"origin", "topic"}, wd)
	require.NoError(t, err)
	require.Equal(t, []string{"push", "origin", "topic"}, argv)
	_, _, err = validateGitInvocation("push", []string{"upstream", "topic"}, wd)
	require.Error(t, err)

	// URL-like and path-like remote arguments are rejected.
	for _, bad := range []string{
		"https://github.com/evil/repo.git",
		"ssh://git@github.com/evil/repo.git",
		"git@github.com:evil/repo.git",
		"/tmp/other/repo",
		"./local-clone",
		"~/other-repo",
	} {
		_, _, err = validateGitInvocation("push", []string{bad}, wd)
		require.Error(t, err, bad)
	}

	// Force-prefixed and deletion refspecs are rejected.
	for _, bad := range []string{"+topic", ":topic"} {
		_, _, err = validateGitInvocation("push", []string{"origin", bad}, wd)
		require.Error(t, err, bad)
	}

	// Legitimate refspecs pass.
	_, _, err = validateGitInvocation("push", []string{"origin", "HEAD:refs/heads/topic"}, wd)
	require.NoError(t, err)
}

func TestValidateGitInvocation_BranchNames(t *testing.T) {
	t.Parallel()
	wd := t.TempDir()
	for _, bad := range []string{"..", "a..b", "feature@{0}", "-x", "has space", ""} {
		_, _, err := validateGitInvocation("checkout", []string{"-b", bad}, wd)
		require.Error(t, err, bad)
	}
	_, _, err := validateGitInvocation("checkout", []string{"-b", "feat/ok.name-1"}, wd)
	require.NoError(t, err)
	_, _, err = validateGitInvocation("checkout", []string{"-b"}, wd)
	require.Error(t, err) // missing branch name
	_, _, err = validateGitInvocation("checkout", []string{"-b", "a", "b"}, wd)
	require.Error(t, err) // exactly one branch name
	_, _, err = validateGitInvocation("checkout", []string{"-b", "a", "--", "file"}, wd)
	require.Error(t, err) // -b and paths cannot mix
}

func TestValidateGitInvocation_ValueFlags(t *testing.T) {
	t.Parallel()
	wd := t.TempDir()
	_, _, err := validateGitInvocation("commit", []string{"-m"}, wd)
	require.Error(t, err) // missing value
	_, _, err = validateGitInvocation("log", []string{"-n", "10"}, wd)
	require.NoError(t, err)
}

func TestGitNeutralizedEnv(t *testing.T) {
	t.Setenv("GIT_DIR", "/tmp/hostile")
	t.Setenv("GIT_SSH_COMMAND", "malicious-wrapper")
	t.Setenv("GIT_PAGER", "hostile-pager")
	t.Setenv("MY_HARMLESS_VAR", "keep-me")

	env := gitNeutralizedEnv()
	joined := strings.Join(env, "\n")

	// Repo-repointing and exec-injection variables are stripped.
	require.NotContains(t, joined, "GIT_DIR=")
	require.NotContains(t, joined, "GIT_SSH_COMMAND=")
	// Host variables unrelated to git are kept.
	require.Contains(t, joined, "MY_HARMLESS_VAR=keep-me")

	// Neutralizing overrides are injected via GIT_CONFIG_*.
	require.Contains(t, joined, "GIT_CONFIG_COUNT=4")
	require.Contains(t, joined, "GIT_CONFIG_KEY_0=core.hooksPath")
	require.Contains(t, joined, "GIT_CONFIG_VALUE_0="+os.DevNull)
	require.Contains(t, joined, "GIT_CONFIG_KEY_1=core.fsmonitor")
	require.Contains(t, joined, "GIT_CONFIG_VALUE_1=false")
	require.Contains(t, joined, "GIT_CONFIG_KEY_2=safe.bareRepository")
	require.Contains(t, joined, "GIT_CONFIG_VALUE_2=explicit")
	require.Contains(t, joined, "GIT_CONFIG_KEY_3=core.sshCommand")

	// core.sshCommand resolves to the system ssh binary (or "ssh").
	var sshVal string
	for _, kv := range env {
		if strings.HasPrefix(kv, "GIT_CONFIG_VALUE_3=") {
			sshVal = strings.TrimPrefix(kv, "GIT_CONFIG_VALUE_3=")
		}
	}
	if p, err := exec.LookPath("ssh"); err == nil {
		require.Equal(t, p, sshVal)
	} else {
		require.Equal(t, "ssh", sshVal)
	}

	// Pager/editor/prompt suppression overrides any host value.
	require.Contains(t, joined, "GIT_PAGER=cat")
	require.Contains(t, joined, "GIT_EDITOR=false")
	require.Contains(t, joined, "GIT_TERMINAL_PROMPT=0")
	require.Contains(t, joined, "GIT_MERGE_AUTOEDIT=no")
}

func TestGitExec_NeutralizesHostileRepoConfig(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	ctx := context.Background()

	// init -b is supported on modern git; fall back for older versions.
	if _, code, err := gitExec(ctx, dir, 10*time.Second, "init", "-b", "main", "."); err != nil || code != 0 {
		out, code, err := gitExec(ctx, dir, 10*time.Second, "init", ".")
		require.NoError(t, err)
		require.Equal(t, 0, code, out)
	}
	_, _, _ = gitExec(ctx, dir, 10*time.Second, "config", "user.email", "test@example.com")
	_, _, _ = gitExec(ctx, dir, 10*time.Second, "config", "user.name", "Test")

	// A hostile core.fsmonitor that would execute a command must be
	// neutralized by the host-side override.
	if _, code, err := gitExec(ctx, dir, 10*time.Second, "config", "core.fsmonitor", "touch pwned"); err != nil || code != 0 {
		t.Fatalf("failed to set hostile config: %v", err)
	}

	out, code, err := gitExec(ctx, dir, 10*time.Second, "status", "--porcelain")
	require.NoError(t, err)
	require.Equal(t, 0, code, out)
	_, statErr := os.Stat(filepath.Join(dir, "pwned"))
	require.True(t, os.IsNotExist(statErr), "hostile fsmonitor executed despite neutralization")
}

func TestPushTargetBranch(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	ctx := context.Background()
	if _, code, err := gitExec(ctx, dir, 10*time.Second, "init", "-b", "main", "."); err != nil || code != 0 {
		_, code, err = gitExec(ctx, dir, 10*time.Second, "init", ".")
		require.NoError(t, err)
		require.Equal(t, 0, code)
	}
	_, _, _ = gitExec(ctx, dir, 10*time.Second, "commit", "--allow-empty", "-m", "init")

	// Current branch decides when no refspec is given.
	branch, err := pushTargetBranch(ctx, dir, []string{"push", "origin"})
	require.NoError(t, err)
	require.Equal(t, "main", branch)

	// Refspecs decide the target.
	branch, err = pushTargetBranch(ctx, dir, []string{"push", "origin", "topic"})
	require.NoError(t, err)
	require.Equal(t, "topic", branch)
	branch, err = pushTargetBranch(ctx, dir, []string{"push", "origin", "HEAD:refs/heads/topic"})
	require.NoError(t, err)
	require.Equal(t, "refs/heads/topic", branch)
}

func TestPushTouchesWorkflows(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	ctx := context.Background()
	if _, code, err := gitExec(ctx, dir, 10*time.Second, "init", "-b", "main", "."); err != nil || code != 0 {
		_, code, err = gitExec(ctx, dir, 10*time.Second, "init", ".")
		require.NoError(t, err)
		require.Equal(t, 0, code)
	}
	_, _, _ = gitExec(ctx, dir, 10*time.Second, "commit", "--allow-empty", "-m", "init")

	// No workflow changes: ungated.
	require.False(t, pushTouchesWorkflows(ctx, dir, "main"))

	// A workflow file added on a topic branch: gated.
	_, _, _ = gitExec(ctx, dir, 10*time.Second, "checkout", "-b", "topic")
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".github", "workflows"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".github", "workflows", "ci.yml"), []byte("on: push\n"), 0o644))
	_, _, _ = gitExec(ctx, dir, 10*time.Second, "add", ".github/workflows/ci.yml")
	_, _, _ = gitExec(ctx, dir, 10*time.Second, "commit", "-m", "add workflow")
	require.True(t, pushTouchesWorkflows(ctx, dir, "main"))
}

func TestValidateGitInvocation_Init(t *testing.T) {
	t.Parallel()
	wd := t.TempDir()

	// git init with no args is accepted.
	argv, tier, err := validateGitInvocation("init", nil, wd)
	require.NoError(t, err)
	require.Equal(t, []string{"init"}, argv)
	require.Equal(t, gitTierLocal, tier)

	// The initial branch name may be set.
	argv, _, err = validateGitInvocation("init", []string{"-b", "main"}, wd)
	require.NoError(t, err)
	require.Equal(t, []string{"init", "-b", "main"}, argv)

	// Disallowed init flags and path arguments are rejected.
	for _, args := range [][]string{
		{"--separate-git-dir", "/tmp/elsewhere"},
		{"--template", "/tmp/tpl"},
		{"--bare"},
		{"--initial-branch=main", "--exec-path", "/tmp"},
		{"subdir"},
		{filepath.Join(t.TempDir(), "repo")},
	} {
		_, _, err = validateGitInvocation("init", args, wd)
		require.Error(t, err, "%v", args)
	}
}

func TestValidateGitInvocation_CloneUnsupported(t *testing.T) {
	t.Parallel()
	_, _, err := validateGitInvocation("clone", []string{"https://github.com/x/y.git"}, t.TempDir())
	require.Error(t, err)
}

func TestGitWorkspacePrecondition(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	ctx := context.Background()

	// Greenfield workspace: init is allowed, everything else is not.
	fresh := t.TempDir()
	require.NoError(t, gitWorkspacePrecondition(ctx, fresh, "init"))
	require.Error(t, gitWorkspacePrecondition(ctx, fresh, "status"))
	require.Error(t, gitWorkspacePrecondition(ctx, fresh, "push"))

	// Initialized repo without origin: local operations are unlocked;
	// the repo precondition itself passes (origin checks live in the
	// pin gate).
	repo := t.TempDir()
	_, code, err := gitExec(ctx, repo, 10*time.Second, "init", ".")
	require.NoError(t, err)
	require.Equal(t, 0, code)
	require.NoError(t, gitWorkspacePrecondition(ctx, repo, "status"))
	require.NoError(t, gitWorkspacePrecondition(ctx, repo, "commit"))
	require.NoError(t, gitWorkspacePrecondition(ctx, repo, "push"))

	// init in an already-initialized repo is rejected.
	require.Error(t, gitWorkspacePrecondition(ctx, repo, "init"))
}

func TestPinOriginRemote(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	ctx := context.Background()

	// No origin remote: remote operations fail closed.
	repo := t.TempDir()
	_, code, err := gitExec(ctx, repo, 10*time.Second, "init", ".")
	require.NoError(t, err)
	require.Equal(t, 0, code)
	approveCalls := 0
	approve := func(string) (bool, error) { approveCalls++; return true, nil }
	_, err = pinOriginRemote(ctx, repo, "", approve)
	require.Error(t, err)
	require.Equal(t, 0, approveCalls)

	// New origin: explicit approval required; approval pins the URL.
	upstream := t.TempDir()
	_, code, err = gitExec(ctx, repo, 10*time.Second, "remote", "add", "origin", upstream)
	require.NoError(t, err)
	require.Equal(t, 0, code)
	pinned, err := pinOriginRemote(ctx, repo, "", approve)
	require.NoError(t, err)
	require.Equal(t, 1, approveCalls)
	require.Equal(t, upstream, pinned)

	// Unchanged origin: no re-approval.
	again, err := pinOriginRemote(ctx, repo, pinned, approve)
	require.NoError(t, err)
	require.Equal(t, pinned, again)
	require.Equal(t, 1, approveCalls)

	// Changed origin (e.g. .git/config rewritten): re-approval required.
	other := t.TempDir()
	_, code, err = gitExec(ctx, repo, 10*time.Second, "remote", "set-url", "origin", other)
	require.NoError(t, err)
	require.Equal(t, 0, code)
	pinned, err = pinOriginRemote(ctx, repo, pinned, approve)
	require.NoError(t, err)
	require.Equal(t, 2, approveCalls)
	require.Equal(t, other, pinned)

	// Denial of the pin stops the remote operation.
	_, err = pinOriginRemote(ctx, repo, "", func(string) (bool, error) { return false, nil })
	require.ErrorIs(t, err, errOriginPinDenied)
}

func TestGitWorktreeAwareness(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	ctx := context.Background()

	// Main checkout with one commit and an origin remote.
	main := t.TempDir()
	_, code, err := gitExec(ctx, main, 10*time.Second, "init", ".")
	require.NoError(t, err)
	require.Equal(t, 0, code)
	_, _, _ = gitExec(ctx, main, 10*time.Second, "commit", "--allow-empty", "-m", "init")
	_, code, err = gitExec(ctx, main, 10*time.Second, "remote", "add", "origin", "git@github.com:owner/repo.git")
	require.NoError(t, err)
	require.Equal(t, 0, code)

	// Linked worktree: .git is a pointer file, not a directory.
	wt := filepath.Join(t.TempDir(), "linked")
	_, code, err = gitExec(ctx, main, 10*time.Second, "worktree", "add", "--detach", wt)
	require.Equal(t, 0, code, "git worktree add failed")
	info, err := os.Stat(filepath.Join(wt, ".git"))
	require.NoError(t, err)
	require.False(t, info.IsDir(), "fixture must produce a .git pointer file")

	// The worktree is recognized as a repository; init is rejected and
	// local operations pass the precondition.
	require.True(t, IsGitRepo(wt))
	require.Error(t, gitWorkspacePrecondition(ctx, wt, "init"))
	require.NoError(t, gitWorkspacePrecondition(ctx, wt, "status"))
	require.NoError(t, gitWorkspacePrecondition(ctx, wt, "commit"))

	// Origin pinning works from the worktree (shared repo config).
	approve := func(string) (bool, error) { return true, nil }
	pinned, err := pinOriginRemote(ctx, wt, "", approve)
	require.NoError(t, err)
	require.Equal(t, "git@github.com:owner/repo.git", pinned)

	// Basic local operations execute in the worktree.
	_, code, err = gitExec(ctx, wt, 10*time.Second, "status", "--porcelain")
	require.NoError(t, err)
	require.Equal(t, 0, code)
	_, code, err = gitExec(ctx, wt, 10*time.Second, "commit", "--allow-empty", "-m", "worktree commit")
	require.NoError(t, err)
	require.Equal(t, 0, code)

	// The git tool allowlist never reaches git worktree management, so
	// an agent inside a worktree cannot list, prune, or modify sibling
	// worktrees or the main checkout.
	_, _, err = validateGitInvocation("worktree", nil, wt)
	require.Error(t, err)
	_, _, err = validateGitInvocation("worktree", []string{"list"}, wt)
	require.Error(t, err)
	_, _, err = validateGitInvocation("worktree", []string{"prune"}, wt)
	require.Error(t, err)
}

func TestGitSecretGate_BlocksCommitAndPush(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	ctx := context.Background()
	dir := t.TempDir()
	_, code, err := gitExec(ctx, dir, 10*time.Second, "init", ".")
	require.NoError(t, err)
	require.Equal(t, 0, code)
	_, _, _ = gitExec(ctx, dir, 10*time.Second, "config", "user.email", "test@example.com")
	_, _, _ = gitExec(ctx, dir, 10*time.Second, "config", "user.name", "Test")

	// Synthetic AWS credential pair staged for commit. (AKIAIOSFODNN7EXAMPLE
	// would not trip the gate: gitleaks allowlists the canonical AWS docs
	// example key, so the fixture uses a random-shaped key.) The secret is
	// assembled at runtime so the literal never appears in source or diff.
	const secret = "vR7mNqXkP2" + "wLd9BfHs4TzYcJ6UeGh3AnQt5oS1Wp"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.txt"),
		[]byte("aws_access_key_id = AKIALMJK3FV7QZX9BNRD\naws_secret_access_key = "+secret+"\n"), 0o644))
	_, code, err = gitExec(ctx, dir, 10*time.Second, "add", "config.txt")
	require.NoError(t, err)
	require.Equal(t, 0, code)

	// Commit gate: staged diff trips the scanner before the commit runs.
	err = gitSecretGate(ctx, dir, nil, "diff", "--cached")
	require.Error(t, err)
	// The error names the rule and file but never echoes the secret.
	require.Contains(t, err.Error(), "rule ")
	require.Contains(t, err.Error(), "config.txt")
	require.NotContains(t, err.Error(), secret)

	// Push gate: the same credential reached history, so the outgoing
	// commit diff trips the scanner.
	_, code, err = gitExec(ctx, dir, 10*time.Second, "commit", "-m", "add config")
	require.NoError(t, err)
	require.Equal(t, 0, code)
	err = gitSecretGate(ctx, dir, nil, "log", "-p")
	require.Error(t, err)
	require.NotContains(t, err.Error(), secret)
}

func TestGitSecretGate_CleanDiffPasses(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	ctx := context.Background()
	dir := t.TempDir()
	_, code, err := gitExec(ctx, dir, 10*time.Second, "init", ".")
	require.NoError(t, err)
	require.Equal(t, 0, code)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "README.md"),
		[]byte("# project\n"), 0o644))
	_, code, err = gitExec(ctx, dir, 10*time.Second, "add", "README.md")
	require.NoError(t, err)
	require.Equal(t, 0, code)
	require.NoError(t, gitSecretGate(ctx, dir, nil, "diff", "--cached"))
}

func TestGitSecretGate_IgnoresDiffMetadata(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	ctx := context.Background()
	dir := t.TempDir()
	_, code, err := gitExec(ctx, dir, 10*time.Second, "init", ".")
	require.NoError(t, err)
	require.Equal(t, 0, code)
	_, _, _ = gitExec(ctx, dir, 10*time.Second, "config", "user.email", "test@example.com")
	_, _, _ = gitExec(ctx, dir, 10*time.Second, "config", "user.name", "Test")

	// Added lines full of commit-SHA-shaped hex and diff-header text
	// must not trip the gate: only added file content is scanned.
	hexBlob := strings.Repeat("0123456789abcdef", 3) // 48 hex chars
	require.NoError(t, os.WriteFile(filepath.Join(dir, "notes.txt"),
		[]byte("baseline = "+hexBlob+"\n"), 0o644))
	_, code, err = gitExec(ctx, dir, 10*time.Second, "add", "notes.txt")
	require.NoError(t, err)
	require.Equal(t, 0, code)
	_, code, err = gitExec(ctx, dir, 10*time.Second, "commit", "-m", "baseline")
	require.NoError(t, err)
	require.Equal(t, 0, code)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "notes.txt"),
		[]byte("baseline = "+hexBlob+"\nmirror = "+hexBlob+"\n"), 0o644))
	_, code, err = gitExec(ctx, dir, 10*time.Second, "add", "notes.txt")
	require.NoError(t, err)
	require.Equal(t, 0, code)
	_, code, err = gitExec(ctx, dir, 10*time.Second, "commit", "-m", "hex again")
	require.NoError(t, err)
	require.Equal(t, 0, code)
	require.NoError(t, gitSecretGate(ctx, dir, nil, "log", "-p"))

	// A real synthetic secret on an added line is still blocked, and the
	// error names the offending file without echoing the secret. The
	// secret is assembled at runtime so the literal never appears in
	// source or diff.
	const secret = "vR7mNqXkP2" + "wLd9BfHs4TzYcJ6UeGh3AnQt5oS1Wp"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "app.env"),
		[]byte("API_TOKEN="+secret+"\n"), 0o644))
	_, code, err = gitExec(ctx, dir, 10*time.Second, "add", "app.env")
	require.NoError(t, err)
	require.Equal(t, 0, code)
	err = gitSecretGate(ctx, dir, nil, "diff", "--cached")
	require.Error(t, err)
	require.Contains(t, err.Error(), "app.env")
	require.NotContains(t, err.Error(), secret)
}

func TestPushDiffBase(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	ctx := context.Background()
	dir := t.TempDir()
	_, code, err := gitExec(ctx, dir, 10*time.Second, "init", "-b", "main", ".")
	if err != nil || code != 0 {
		_, code, err = gitExec(ctx, dir, 10*time.Second, "init", ".")
		require.NoError(t, err)
		require.Equal(t, 0, code)
	}
	_, _, _ = gitExec(ctx, dir, 10*time.Second, "commit", "--allow-empty", "-m", "init")

	// Fresh repo with no commits and no remotes: every commit is
	// outgoing, so there is no base.
	empty := t.TempDir()
	_, code, err = gitExec(ctx, empty, 10*time.Second, "init", ".")
	require.NoError(t, err)
	require.Equal(t, 0, code)
	require.Equal(t, "", pushDiffBase(ctx, empty, "topic", "main"))

	// Default branch present: commits after it are outgoing.
	require.Equal(t, "main", pushDiffBase(ctx, dir, "topic", "main"))
}

func TestAppendCommitTrailer(t *testing.T) {
	t.Parallel()
	// Trailer lands on the last -m paragraph.
	argv := appendCommitTrailer([]string{"commit", "-m", "title", "-m", "body"}, "Phosphor-Trace: abc123")
	require.Equal(t, []string{"commit", "-m", "title", "-m", "body\n\nPhosphor-Trace: abc123"}, argv)

	// No message (editor commit) and empty trailer are left untouched.
	require.Equal(t, []string{"commit"}, appendCommitTrailer([]string{"commit"}, "Phosphor-Trace: abc"))
	require.Equal(t, []string{"commit", "-m", "msg"}, appendCommitTrailer([]string{"commit", "-m", "msg"}, ""))
}

func TestGitTraceTrailer(t *testing.T) {
	// Disabled (default): the global tracer is a no-op, so the span
	// context is invalid and no trailer is produced.
	offCtx, offSpan := otel.StartSpan(context.Background(), "execute_tool git")
	defer offSpan.End()
	require.Equal(t, "", gitTraceTrailer(offCtx))

	// Enabled: with a real (sampled) tracer provider installed, the
	// trailer carries the trace id.
	old := otelgo.GetTracerProvider()
	tp := sdktrace.NewTracerProvider()
	otelgo.SetTracerProvider(tp)
	defer func() {
		otelgo.SetTracerProvider(old)
		_ = tp.Shutdown(context.Background())
	}()

	onCtx, onSpan := otel.StartSpan(context.Background(), "execute_tool git")
	defer onSpan.End()
	trailer := gitTraceTrailer(onCtx)
	require.True(t, strings.HasPrefix(trailer, "Phosphor-Trace: "))
	sc := trace.SpanContextFromContext(onCtx)
	require.Equal(t, "Phosphor-Trace: "+sc.TraceID().String(), trailer)

	// The trailer is appended to the commit message via the argv helper.
	argv := appendCommitTrailer([]string{"commit", "-m", "msg"}, trailer)
	require.Contains(t, argv[2], trailer)
}

func TestGitSteerError_UnsupportedSteer(t *testing.T) {
	t.Parallel()

	err := gitSteerError(errors.New("subcommand \"clone\" is not allowed"))
	require.Contains(t, err.Error(), "subcommand \"clone\" is not allowed")
	require.Contains(t, err.Error(), "sandbox=\"escalate\"")
	require.NotContains(t, err.Error(), "Do NOT retry")
}

func TestGitSteerError_PolicySteer(t *testing.T) {
	t.Parallel()

	err := gitSteerError(errors.New("Security violation: potential secret detected (rule github-pat)"))
	require.Contains(t, err.Error(), "Do NOT retry this command via bash or escalation")
	require.NotContains(t, err.Error(), "sandbox=\"escalate\"")
}

func TestBashDescriptionRepoToolingRedirect(t *testing.T) {
	t.Parallel()

	// With mediated git/gh tools available, bash describes the redirect
	// and omits the raw git/gh workflow guidance.
	on := bashDescription("/ws", config.ToolBash{}, nil, "test-model", true)
	require.Contains(t, on, "<repo_tooling>")
	require.NotContains(t, on, "<git_commits>")
	require.NotContains(t, on, "<pull_requests>")

	// Without them, the legacy bash-side guidance is preserved verbatim.
	off := bashDescription("/ws", config.ToolBash{}, nil, "test-model", false)
	require.NotContains(t, off, "<repo_tooling>")
	require.Contains(t, off, "<git_commits>")
}

func TestGitPushGateTier(t *testing.T) {
	t.Parallel()

	// Default policy: every push stays gated, feature branches included.
	require.True(t, gitPushGateTier(gitTierLocal, "topic", "main", false, config.GitPushPolicyPrompt))
	// feature_branches: non-default branches run unattended.
	require.False(t, gitPushGateTier(gitTierLocal, "topic", "main", false, config.GitPushPolicyFeatureBranches))
	// Default-branch and workflow-touching pushes stay gated under any policy.
	require.True(t, gitPushGateTier(gitTierLocal, "main", "main", false, config.GitPushPolicyFeatureBranches))
	require.True(t, gitPushGateTier(gitTierLocal, "topic", "main", true, config.GitPushPolicyFeatureBranches))
	// Unknown values fall back to the strict default.
	require.True(t, gitPushGateTier(gitTierLocal, "topic", "main", false, "yolo"))
}

func TestGitToolSeedsPinnedOrigin(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	ctx := context.Background()

	// Repositories with an existing origin are seeded at construction so
	// remote operations proceed without a redundant pin prompt.
	repo := t.TempDir()
	_, code, err := gitExec(ctx, repo, 10*time.Second, "init", ".")
	require.NoError(t, err)
	require.Equal(t, 0, code)
	upstream := t.TempDir()
	_, code, err = gitExec(ctx, repo, 10*time.Second, "remote", "add", "origin", upstream)
	require.NoError(t, err)
	require.Equal(t, 0, code)
	seed, ok := seedPinnedOrigin(ctx, repo)
	require.True(t, ok)
	require.Equal(t, upstream, seed)

	// The seed feeds pinOriginRemote directly: no approval fires for the
	// unchanged origin, but a mid-session URL change still does.
	approveCalls := 0
	approve := func(string) (bool, error) { approveCalls++; return true, nil }
	pinned, err := pinOriginRemote(ctx, repo, seed, approve)
	require.NoError(t, err)
	require.Equal(t, 0, approveCalls)
	other := t.TempDir()
	_, code, err = gitExec(ctx, repo, 10*time.Second, "remote", "set-url", "origin", other)
	require.NoError(t, err)
	require.Equal(t, 0, code)
	pinned, err = pinOriginRemote(ctx, repo, pinned, approve)
	require.NoError(t, err)
	require.Equal(t, 1, approveCalls)
	require.Equal(t, other, pinned)

	// Greenfield workspaces seed nothing; the origin stays gated until
	// the user configures one.
	empty := t.TempDir()
	_, ok = seedPinnedOrigin(ctx, empty)
	require.False(t, ok)
}
