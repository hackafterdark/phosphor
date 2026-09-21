package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"charm.land/fantasy"
	"github.com/stretchr/testify/require"

	"github.com/hackafterdark/phosphor/pkg/config"
)

const (
	testAWSKeyID     = "AKIAIOSVPK253PIP5TGP"
	testStripeSecret = "sk_live_125JKPSG25JQ253PIP5TG25P"
	sentinelPrefix   = "<redacted:gitleaks:"
)

// TestRedactSecrets_ReplacesCredentials proves detected secrets are swapped for
// a non-reusable sentinel and the raw value never survives in the output.
func TestRedactSecrets_ReplacesCredentials(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		in     string
		secret string
	}{
		{"aws_access_key_id", "AWS_ACCESS_KEY_ID := \"" + testAWSKeyID + "\"", testAWSKeyID},
		{"stripe_live", "stripe_key := \"" + testStripeSecret + "\"", testStripeSecret},
		{"inline_env_line", "SECRET := \"ghp_125JKPSG25JQ253PIP5TG25P25KPSG\"", "ghp_125JKPSG25JQ253PIP5TG25P25KPSG"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got := redactSecrets(c.in)
			require.NotContains(t, got, c.secret, "secret leaked through redaction")
			require.Contains(t, got, sentinelPrefix, "expected a redaction sentinel")
		})
	}
}

// TestRedactSecrets_LeavesCleanTextUnchanged proves ordinary output is returned
// byte-for-byte identical (no gratuitous rewriting / zero overhead on the hot path).
func TestRedactSecrets_LeavesCleanTextUnchanged(t *testing.T) {
	t.Parallel()

	clean := []string{
		"",
		"build succeeded\n3 packages compiled\n",
		"func main() { fmt.Println(\"hello\") }",
		"api_key := \"your_key_here\"", // low-entropy placeholder
	}
	for _, c := range clean {
		require.Equal(t, c, redactSecrets(c))
	}
}

// TestRedactSecrets_PathAllowlist proves known test/doc material is skipped so
// fixture credentials in tests do not get rewritten on read.
func TestRedactSecrets_PathAllowlist(t *testing.T) {
	t.Parallel()

	in := "AWS_ACCESS_KEY_ID := \"" + testAWSKeyID + "\""
	skip := []string{
		"internal/x/testdata/fixture.go",
		"test/fixtures/config.go",
		"docs/RUNBOOK.md",
		"pkg/x/thing_test.go",
	}
	for _, p := range skip {
		require.Equal(t, in, redactSecretsAt(in, p, "view"), "path %q should be allow-listed", p)
	}

	// A non-allowlisted path IS redacted.
	require.NotContains(t, redactSecretsAt(in, "app/config.go", "view"), testAWSKeyID)
}

// TestRedactSecrets_SentinelIsNotAUusableToken proves the emitted marker is not a
// syntactically valid credential, so echoing it into an edit cannot restore a key.
func TestRedactSecrets_SentinelIsNotAUusableToken(t *testing.T) {
	t.Parallel()

	require.Equal(t, "<redacted:gitleaks:aws-access-token>", redactionSentinel("aws-access-token"))
	require.Equal(t, "<redacted:gitleaks:unknown>", redactionSentinel(""))
}

// TestViewTool_RedactsFileSecrets is an end-to-end check that the view tool's
// read path scrubs a hardcoded credential from the returned content.
func TestViewTool_RedactsFileSecrets(t *testing.T) {
	t.Parallel()

	workingDir := t.TempDir()
	body := "package cfg\n\nconst Key = \"" + testAWSKeyID + "\"\n"
	require.NoError(t, os.WriteFile(filepath.Join(workingDir, "config.go"), []byte(body), 0o644))

	tool := NewViewTool(nil, &mockPermissionService{}, mockFileTrackerService{}, nil, workingDir)
	input, err := json.Marshal(ViewParams{FilePath: "config.go"})
	require.NoError(t, err)

	resp, err := tool.Run(context.Background(), fantasy.ToolCall{
		ID: "view-1", Name: ViewToolName, Input: string(input),
	})
	require.NoError(t, err)
	require.False(t, resp.IsError)
	require.NotContains(t, resp.Content, testAWSKeyID)
	require.Contains(t, resp.Content, sentinelPrefix)
}

// TestGrepTool_RedactsMatchedSecrets is an end-to-end check that grep scrubs a
// credential from matched line output.
func TestGrepTool_RedactsMatchedSecrets(t *testing.T) {
	t.Parallel()

	workingDir := t.TempDir()
	body := "package cfg\n\nconst Key = \"" + testAWSKeyID + "\"\n"
	require.NoError(t, os.WriteFile(filepath.Join(workingDir, "config.go"), []byte(body), 0o644))

	tool := NewGrepTool(workingDir, config.ToolGrep{})
	input, err := json.Marshal(GrepParams{Pattern: "AKIA[A-Z0-9]{10,}", Path: "."})
	require.NoError(t, err)

	resp, err := tool.Run(context.Background(), fantasy.ToolCall{
		ID: "grep-1", Name: GrepToolName, Input: string(input),
	})
	require.NoError(t, err)
	require.False(t, resp.IsError)
	require.NotContains(t, resp.Content, testAWSKeyID)
	require.Contains(t, resp.Content, sentinelPrefix)
}
