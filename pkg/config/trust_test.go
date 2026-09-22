package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// useTrustStoreFile points the trust store at a throwaway file and resets the
// in-process gate so a test observes a clean state.
func useTrustStoreFile(t *testing.T) string {
	t.Helper()
	resetWorkspaceTrustStateForTest()
	path := filepath.Join(t.TempDir(), "trusted_workspaces.json")
	trustMu.Lock()
	trustedWorkspacesPathOverride = path
	trustMu.Unlock()
	t.Cleanup(resetWorkspaceTrustStateForTest)
	return path
}

// makeToolingWorkspace creates a workspace that declares repo-local MCP config so
// the trust gate treats it as bringing custom tooling.
func makeToolingWorkspace(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".mcp.json"), []byte("{}"), 0o600))
	return dir
}

func TestTrust_Store_TrustedVsUntrusted(t *testing.T) {
	path := useTrustStoreFile(t)

	store := TrustedWorkspaces()
	dir := filepath.Join(t.TempDir(), "repo")

	require.False(t, store.IsTrusted(dir), "a fresh path must not be trusted")

	require.NoError(t, store.Trust(dir))
	require.True(t, store.IsTrusted(dir), "path must be trusted after Trust")

	// The trust decision is stable across a brand-new store reading the same file.
	reloaded := TrustedWorkspaces()
	require.True(t, reloaded.IsTrusted(dir), "trust must persist to the file and be readable")

	require.NoError(t, store.Untrust(dir))
	require.False(t, store.IsTrusted(dir), "path must not be trusted after Untrust")

	_, err := os.Stat(path)
	require.NoError(t, err, "the trust file must exist after a write")
}

func TestTrust_Store_Normalization(t *testing.T) {
	useTrustStoreFile(t)
	store := TrustedWorkspaces()
	base := t.TempDir()

	trailing := base + string(filepath.Separator)
	require.NoError(t, store.Trust(trailing))

	// A relative reference to the same directory resolves to the same key.
	require.True(t, store.IsTrusted(base), "trailing-separator and clean forms must match")

	// An unrelated sibling directory is not trusted.
	other := filepath.Join(t.TempDir(), "other")
	require.NoError(t, os.MkdirAll(other, 0o700))
	require.False(t, store.IsTrusted(other))
}

func TestTrust_RepoDefinesCustomTooling(t *testing.T) {
	useTrustStoreFile(t)

	plain := filepath.Join(t.TempDir(), "plain")
	require.NoError(t, os.MkdirAll(plain, 0o700))
	require.False(t, RepoDefinesCustomTooling(plain), "an empty workspace declares no tooling")

	settings := filepath.Join(t.TempDir(), "settings")
	require.NoError(t, os.MkdirAll(filepath.Join(settings, ".phosphor"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(settings, ".phosphor", "settings.json"), []byte("{}"), 0o600))
	require.True(t, RepoDefinesCustomTooling(settings), "a repo-local workspace config counts as custom tooling")

	jobs := filepath.Join(t.TempDir(), "jobs")
	require.NoError(t, os.MkdirAll(filepath.Join(jobs, ".phosphor", "jobs"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(jobs, ".phosphor", "jobs", "task.md"), []byte("# task"), 0o600))
	require.True(t, RepoDefinesCustomTooling(jobs), "a scheduled project task counts as custom tooling")
}

func TestTrust_Allowed_WhenNoTooling(t *testing.T) {
	useTrustStoreFile(t)
	plain := t.TempDir()
	require.True(t, WorkspaceToolingAllowed(plain), "a workspace with no repo tooling needs no trust")
}

func TestTrust_Allowed_WhenTrusted(t *testing.T) {
	useTrustStoreFile(t)
	dir := makeToolingWorkspace(t)
	require.NoError(t, TrustWorkspace(dir))
	require.True(t, WorkspaceToolingAllowed(dir))
}

func TestTrust_Denied_HeadlessUntrusted(t *testing.T) {
	useTrustStoreFile(t)
	dir := makeToolingWorkspace(t)

	// Non-interactive, no --trust: repo tooling is ignored.
	require.False(t, WorkspaceToolingAllowed(dir),
		"headless untrusted workspace must fall back to native-only tools")
}

func TestTrust_Allowed_ByTrustFlag(t *testing.T) {
	useTrustStoreFile(t)
	dir := makeToolingWorkspace(t)

	SetWorkspaceTrustRequested(dir, true)
	require.True(t, WorkspaceToolingAllowed(dir), "--trust trusts the workspace without a prompt")
	require.True(t, IsWorkspaceTrusted(dir), "--trust must persist the workspace as trusted")
}

func TestTrust_Prompt_YesTrusts(t *testing.T) {
	useTrustStoreFile(t)
	dir := makeToolingWorkspace(t)

	var asked int
	SetWorkspaceTrustInteractive(true)
	SetWorkspaceTrustPrompt(func(_, _ string) bool {
		asked++
		return true
	})

	require.True(t, WorkspaceToolingAllowed(dir))
	require.Equal(t, 1, asked, "the operator must be prompted exactly once")
	require.True(t, IsWorkspaceTrusted(dir), "an accepted prompt must persist trust")

	// A second consultation is served from the cache and does not re-prompt.
	require.True(t, WorkspaceToolingAllowed(dir))
	require.Equal(t, 1, asked, "the decision must be cached")
}

func TestTrust_Prompt_NoDenies(t *testing.T) {
	useTrustStoreFile(t)
	dir := makeToolingWorkspace(t)

	asked := 0
	SetWorkspaceTrustInteractive(true)
	SetWorkspaceTrustPrompt(func(_, _ string) bool {
		asked++
		return false
	})

	require.False(t, WorkspaceToolingAllowed(dir))
	require.Equal(t, 1, asked)
	require.False(t, IsWorkspaceTrusted(dir), "a declined prompt must not persist trust")
}

func TestTrust_ConfigFilesWithExecutableSectionsAreDetected(t *testing.T) {
	useTrustStoreFile(t)

	dir := filepath.Join(t.TempDir(), "cfgrepo")
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".phosphor"), 0o700))

	// A workspace config that defines an MCP server is an executable surface even
	// though no dedicated mcp/settings file exists.
	wsCfg := filepath.Join(dir, ".phosphor", "phosphor.json")
	require.NoError(t, os.WriteFile(wsCfg, []byte(`{"mcp":{"evil":{"command":"netcat","args":["-l"]}}}`), 0o600))
	require.True(t, RepoDefinesCustomTooling(dir), "a config-defined MCP server counts as custom tooling")

	// The same file without any executable section is just settings.
	require.NoError(t, os.WriteFile(wsCfg, []byte(`{"options":{"log_level":"debug"}}`), 0o600))
	require.False(t, RepoDefinesCustomTooling(dir), "a config without mcp/hooks declares no tooling")

	// Hooks are shell commands and count as well, including via phosphor.json.
	require.NoError(t, os.WriteFile(wsCfg, []byte(`{}`), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "phosphor.json"), []byte(`{"hooks":{"PreToolUse":[{"type":"command","command":"./gate.sh"}]}}`), 0o600))
	require.True(t, RepoDefinesCustomTooling(dir), "repo-defined hooks count as custom tooling")
}

func TestTrust_CacheDoesNotStaleBlessLateTooling(t *testing.T) {
	useTrustStoreFile(t)

	// First consultation: the workspace declares nothing, so it is allowed.
	dir := filepath.Join(t.TempDir(), "late")
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".phosphor"), 0o700))
	require.True(t, WorkspaceToolingAllowed(dir))

	// Tooling appears mid-session (agent write, git operation, config reload).
	// A stale "allow" from the no-tooling state must not bless it without consent.
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".phosphor", "mcp.json"), []byte(`{"srv":{"command":"evil"}}`), 0o600))
	require.False(t, WorkspaceToolingAllowed(dir),
		"tooling added after the first consultation must face a fresh trust decision")
}

// TestTrust_TrustFlagDoesNotBleedToOtherWorkspaces models the multi-workspace
// server-daemon case: a single --trust grants consent for one workspace, and that
// consent must not be reused to silently auto-trust (and persist) a different
// workspace opened later in the same process. The daemon is headless, so the
// second workspace falls to the deny default rather than inheriting the first one's
// --trust.
func TestTrust_TrustFlagDoesNotBleedToOtherWorkspaces(t *testing.T) {
	useTrustStoreFile(t)

	root := t.TempDir()
	a := filepath.Join(root, "a")
	b := filepath.Join(root, "b")
	require.NoError(t, os.MkdirAll(a, 0o700))
	require.NoError(t, os.MkdirAll(b, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(a, ".mcp.json"), []byte("{}"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(b, ".mcp.json"), []byte("{}"), 0o600))

	// Operator trusted only workspace A via --trust; no interactive prompt is set up.
	SetWorkspaceTrustRequested(a, true)

	require.True(t, WorkspaceToolingAllowed(a), "the --trust'd workspace runs its repo tooling")
	require.True(t, IsWorkspaceTrusted(a))

	require.False(t, WorkspaceToolingAllowed(b),
		"a different workspace must not inherit workspace A's --trust")
	require.False(t, IsWorkspaceTrusted(b),
		"the bleed-through must not persist workspace B as trusted")
}
