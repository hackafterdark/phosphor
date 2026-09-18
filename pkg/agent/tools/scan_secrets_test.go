package tools

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"charm.land/fantasy"
	"github.com/stretchr/testify/require"
)

// Synthetic, non-functional credentials shaped to match gitleaks' high-precision
// vendor rules. They are inert test fixtures, not real keys.
const (
	scanTestGithubPAT = "ghp_125JKPSG25JQ253PIP5TG25P25KPSG"
	scanTestAWSKey    = "AKIAIOSVPK253PIP5TGP"
)

func runScanSecrets(t *testing.T, workingDir string, params ScanSecretsParams) fantasy.ToolResponse {
	t.Helper()
	tool := NewScanSecretsTool(workingDir)
	input, err := json.Marshal(params)
	require.NoError(t, err)
	resp, err := tool.Run(t.Context(), fantasy.ToolCall{ID: "scan-1", Name: ScanSecretsToolName, Input: string(input)})
	require.NoError(t, err)
	return resp
}

// TestScanSecrets_ReportsHighEntropyPAT proves a synthetic credential is surfaced
// and, critically, that the raw secret never appears anywhere in the tool output.
func TestScanSecrets_ReportsHighEntropyPAT(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeScanFile(t, dir, "config.go",
		"package cfg\n\nconst GitHubToken = \""+scanTestGithubPAT+"\"\nconst AWSKey = \""+scanTestAWSKey+"\"\n")

	resp := runScanSecrets(t, dir, ScanSecretsParams{Path: "."})
	require.False(t, resp.IsError)

	require.Contains(t, resp.Content, "potential secret", "expected a finding to be reported")
	require.Contains(t, resp.Content, "Masked:")
	require.Contains(t, resp.Content, scanSecretsBullet, "masked value should carry the bullet mask")
	require.Contains(t, resp.Content, "<redacted:gitleaks:", "the source line preview must carry the redaction sentinel")

	// The raw credential must never be echoed back to the model.
	require.NotContains(t, resp.Content, scanTestGithubPAT)
	require.NotContains(t, resp.Content, scanTestAWSKey)

	// The masked rendering must not be the raw value verbatim.
	require.NotEqual(t, scanTestGithubPAT, resp.Content)

	var meta ScanSecretsResponseMetadata
	require.NoError(t, json.Unmarshal([]byte(resp.Metadata), &meta))
	require.Greater(t, meta.NumberOfFindings, 0)
	require.False(t, meta.ScanHistory)
}

// TestScanSecrets_CleanDirectoryReportsNothing proves a directory without any
// credential returns the canonical zero-finding sentence.
func TestScanSecrets_CleanDirectoryReportsNothing(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeScanFile(t, dir, "main.go", "package main\n\nfunc main() { println(\"hello world\") }\n")
	writeScanFile(t, dir, "api_key.go", "package cfg\n\nconst APIKey = \"your_key_here\"\n")

	resp := runScanSecrets(t, dir, ScanSecretsParams{Path: "."})
	require.False(t, resp.IsError)
	require.Contains(t, resp.Content, "No unignored secrets detected")
	require.NotContains(t, resp.Content, "potential secret")

	var meta ScanSecretsResponseMetadata
	require.NoError(t, json.Unmarshal([]byte(resp.Metadata), &meta))
	require.Equal(t, 0, meta.NumberOfFindings)
}

// TestScanSecrets_GitleaksIgnoreFiltering proves files matched by a
// .gitleaksignore file are excluded from the scan.
func TestScanSecrets_GitleaksIgnoreFiltering(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeScanFile(t, dir, "real.go", "package cfg\n\nconst Token = \""+scanTestGithubPAT+"\"\n")
	writeScanFile(t, dir, "fixture.go", "package cfg\n\nconst Token = \""+scanTestAWSKey+"\"\n")
	writeScanFile(t, dir, ".gitleaksignore", "fixture.go\n")

	resp := runScanSecrets(t, dir, ScanSecretsParams{Path: "."})
	require.False(t, resp.IsError)

	// The ignored fixture credential is gone; the unignored one is still reported.
	require.NotContains(t, resp.Content, scanTestAWSKey)
	require.NotContains(t, resp.Content, "fixture.go")
	require.Contains(t, resp.Content, "real.go")
	require.NotContains(t, resp.Content, scanTestGithubPAT, "even reported secrets stay masked")

	var meta ScanSecretsResponseMetadata
	require.NoError(t, json.Unmarshal([]byte(resp.Metadata), &meta))
	require.Equal(t, 1, meta.NumberOfFindings)
}

// TestScanSecrets_HistoryMode scans commit diffs when asked, and again never
// leaks the raw credential. It skips when git is unavailable in the environment.
func TestScanSecrets_HistoryMode(t *testing.T) {
	t.Parallel()

	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not available on PATH")
	}

	dir := t.TempDir()
	git := func(args ...string) (string, error) {
		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = dir
		out, err := cmd.Output()
		return string(out), err
	}
	if _, err := git("init", "-q"); err != nil {
		t.Skipf("could not init a git repo: %v", err)
	}

	// Write the secret, commit it, then delete it from the working tree so only
	// history (not the working tree) still contains the credential.
	writeScanFile(t, dir, "config.go", "package cfg\n\nconst Token = \""+scanTestGithubPAT+"\"\n")
	if _, err := git("add", "--", "config.go"); err != nil {
		t.Skipf("git add failed: %v", err)
	}
	if _, err := git("-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-q", "-m", "add secret"); err != nil {
		t.Skipf("git commit failed: %v", err)
	}
	require.NoError(t, os.Remove(filepath.Join(dir, "config.go")))

	// Working-tree scan finds nothing now that the file is gone.
	worktree := runScanSecrets(t, dir, ScanSecretsParams{Path: "."})
	require.Contains(t, worktree.Content, "No unignored secrets detected")

	// History scan surfaces the committed secret, masked, without leaking it.
	history := runScanSecrets(t, dir, ScanSecretsParams{Path: ".", ScanHistory: true})
	require.False(t, history.IsError)
	require.Contains(t, history.Content, "potential secret", "history should surface the committed credential")
	require.NotContains(t, history.Content, scanTestGithubPAT, "raw credential must never appear in output")

	var meta ScanSecretsResponseMetadata
	require.NoError(t, json.Unmarshal([]byte(history.Metadata), &meta))
	require.True(t, meta.ScanHistory)
	require.Greater(t, meta.NumberOfFindings, 0)
}

// TestScanSecrets_RejectsOutsideWorkspace proves the scanner is confined to the
// workspace and refuses to audit paths outside it.
func TestScanSecrets_RejectsOutsideWorkspace(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	outside := t.TempDir()
	writeScanFile(t, outside, "leak.go", "package cfg\n\nconst Token = \""+scanTestGithubPAT+"\"\n")

	resp := runScanSecrets(t, dir, ScanSecretsParams{Path: filepath.Join(outside, "leak.go")})
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "outside workspace")
}

// TestScanSecrets_MaskingShape is a focused unit check of the masking helper that
// renders the credential: it keeps only the vendor prefix and the trailing two
// characters and never returns the value verbatim.
func TestScanSecrets_MaskingShape(t *testing.T) {
	t.Parallel()

	masked := maskSecret(scanTestGithubPAT)
	require.True(t, strings.HasPrefix(masked, "ghp_"), "vendor prefix should be preserved")
	require.True(t, strings.HasSuffix(masked, "SG"), "final two characters should be preserved")
	require.Contains(t, masked, scanSecretsBullet)
	require.NotContains(t, masked, scanTestGithubPAT)
	require.NotEqual(t, scanTestGithubPAT, masked)
}

func writeScanFile(t *testing.T, dir, name, content string) {
	t.Helper()
	full := filepath.Join(dir, name)
	require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
	require.NoError(t, os.WriteFile(full, []byte(content), 0o644))
}
