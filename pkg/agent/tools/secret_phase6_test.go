package tools

import (
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zricethezav/gitleaks/v8/report"
)

// useRedaction installs a read-path redaction snapshot for one test and restores
// the previous state (and clears the token store) on cleanup. These tests mutate
// process-global state, so they intentionally do not run in parallel.
func useRedaction(t *testing.T, opts RedactionPolicyOptions) {
	t.Helper()
	ResetRedactionPolicyForTest()
	t.Cleanup(ResetRedactionPolicyForTest)
	SetRedactionPolicy(opts)
}

func ptrBool(b bool) *bool { return &b }

// ---------------------------------------------------------------------------
// Context-flagged scan API / FP modes
// ---------------------------------------------------------------------------

func TestScanModeForPath(t *testing.T) {
	cases := []struct {
		path, source string
		want         ScanMode
	}{
		{"internal/app/app.go", "view", ScanCodeFile},
		{"src/main.py", "view", ScanCodeFile},
		{"components/App.tsx", "grep", ScanCodeFile},
		{"bin/run.sh", "bash", ScanCodeFile},
		{".env", "view", ScanFull},
		{"config/prod.env", "view", ScanFull},
		{"settings.json", "view", ScanFull},
		{"deploy/secrets.yaml", "view", ScanFull},
		{"", "bash", ScanFull},
		{"", "tool", ScanFull},
	}
	for _, c := range cases {
		require.Equal(t, c.want, scanModeForPath(c.path, c.source), "path %q", c.path)
	}
}

// filterFindings is exercised directly against synthetic detector output so the
// FP-mode contract is asserted independently of any particular rule's regex.
func TestFilterFindings_CodeFileDropsGenericOnly(t *testing.T) {
	in := []report.Finding{
		{RuleID: "generic-api-key"},
		{RuleID: "generic-secret"},
		{RuleID: "aws-access-token"},
		{RuleID: "stripe-access-token"},
		{RuleID: "private-key"},
		{RuleID: "jwt"},
		{RuleID: "curl-auth-header"},
	}

	full := filterFindings(append([]report.Finding{}, in...), ScanFull)
	require.Len(t, full, len(in), "full mode keeps everything")

	got := filterFindings(append([]report.Finding{}, in...), ScanCodeFile)
	kept := map[string]bool{}
	for _, f := range got {
		kept[f.RuleID] = true
	}
	require.False(t, kept["generic-api-key"], "generic family suppressed in code mode")
	require.False(t, kept["generic-secret"], "generic family suppressed in code mode")
	for _, hi := range []string{"aws-access-token", "stripe-access-token", "private-key", "jwt", "curl-auth-header"} {
		require.True(t, kept[hi], "high-precision %s must survive code mode", hi)
	}
}

func TestFilterFindings_FullModeIsIdentity(t *testing.T) {
	in := []report.Finding{{RuleID: "generic-api-key"}, {RuleID: "jwt"}}
	out := filterFindings(in, ScanFull)
	require.Len(t, out, 2)
}

func TestIsGenericRule(t *testing.T) {
	pol := defaultRedactionPolicy()
	require.True(t, pol.isGenericRule("generic-api-key"))
	require.True(t, pol.isGenericRule("generic-anything"), "prefix match")
	require.False(t, pol.isGenericRule("aws-access-token"))
	require.False(t, pol.isGenericRule(""), "empty is never generic")

	SetRedactionPolicy(RedactionPolicyOptions{ExtraGenericRules: []string{"weird-internal-token"}})
	t.Cleanup(ResetRedactionPolicyForTest)
	require.True(t, currentRedactionPolicy().isGenericRule("weird-internal-token"))
}

// The concrete end-to-end expression of code-file FP mode: a generic KEY=value
// credential that the write path would block is left visible by the read path
// when it is known to be source code, while a vendor-prefixed key is still
// redacted in the same file.
func TestCodeFileMode_SuppressesGenericKeepsVendor(t *testing.T) {
	code := "func main() {\n\tconst apiKey = \"Ab3d5Ef7G9H1J3K5L7N9P1Q3R5S7T9V1W3\"\n\tconst aws = \"AKIAIOSVPK253PIP5TGP\"\n}\n"

	gotCode := redactSecretsAt(code, "src/config.go", "view")
	require.Contains(t, gotCode, "Ab3d5Ef7G9H1J3K5L7N9P1Q3R5S7T9V1W3",
		"the generic KEY=value hit is FP noise on code and must survive code-file mode")
	require.NotContains(t, gotCode, "AKIAIOSVPK253PIP5TGP",
		"the high-precision AWS key must still be redacted in code-file mode")

	// The same bytes read as a config file (full mode) redact both.
	gotFull := redactSecretsAt(code, "deploy/config.json", "view")
	require.NotContains(t, gotFull, "Ab3d5Ef7G9H1J3K5L7N9P1Q3R5S7T9V1W3")
	require.NotContains(t, gotFull, "AKIAIOSVPK253PIP5TGP")
}

// code-file FP mode off restores the full check set even for source code.
func TestCodeFileMode_DisabledScansGeneric(t *testing.T) {
	useRedaction(t, RedactionPolicyOptions{CodeFileFPEnabled: ptrBool(false)})

	code := "const apiKey = \"Ab3d5Ef7G9H1J3K5L7N9P1Q3R5S7T9V1W3\"\n"
	got := redactSecretsAt(code, "src/config.go", "view")
	require.NotContains(t, got, "Ab3d5Ef7G9H1J3K5L7N9P1Q3R5S7T9V1W3")
}

// ---------------------------------------------------------------------------
// Token store
// ---------------------------------------------------------------------------

func TestTokenStore_RoundTrip(t *testing.T) {
	s := newTokenStore(time.Hour, 100)
	tok := s.Issue("aws-access-token", "AKIAIOSVPK253PIP5TGP")
	require.True(t, tokenPatternRe.MatchString(tok), "issued text must be a token")
	require.NotContains(t, tok, "AKIAIOSVPK253PIP5TGP", "the token must not carry the value")

	out, n := s.Restore("key := " + tok)
	require.Equal(t, 1, n)
	require.Equal(t, "key := AKIAIOSVPK253PIP5TGP", out)
}

func TestTokenStore_StableIDPerValue(t *testing.T) {
	s := newTokenStore(time.Hour, 100)
	a := s.Issue("dotenv", "same-value")
	b := s.Issue("dotenv", "same-value")
	c := s.Issue("dotenv", "other-value")
	require.Equal(t, a, b, "the same value gets one stable token")
	require.NotEqual(t, a, c, "distinct values get distinct tokens")
}

func TestTokenStore_UnknownTokenLeftInPlace(t *testing.T) {
	s := newTokenStore(time.Hour, 100)
	out, n := s.Restore("text <secret:foo:sec_zzz> tail")
	require.Equal(t, 0, n)
	require.Contains(t, out, "<secret:foo:sec_zzz>", "an unresolvable token stays inert")
}

func TestTokenStore_Expiry(t *testing.T) {
	s := newTokenStore(50*time.Millisecond, 100)
	tok := s.Issue("dotenv", "value123")
	out, n := s.Restore(tok)
	require.Equal(t, 1, n)
	require.Equal(t, "value123", out)

	time.Sleep(60 * time.Millisecond)
	out2, n2 := s.Restore(tok)
	require.Equal(t, 0, n2, "expired token must fail to resolve")
	require.Equal(t, tok, out2, "expired token stays as an inert sentinel")
}

func TestTokenStore_CapIsBounded(t *testing.T) {
	s := newTokenStore(time.Hour, 32)
	for i := 0; i < 500; i++ {
		s.Issue("dotenv", "v"+strconv.Itoa(i))
	}
	s.mu.Lock()
	grown := len(s.entries)
	s.mu.Unlock()
	require.LessOrEqual(t, grown, 32, "the store must never exceed its cap")
}

func TestTokenStore_ResetClears(t *testing.T) {
	s := newTokenStore(time.Hour, 100)
	tok := s.Issue("dotenv", "abc")
	s.Reset()
	out, n := s.Restore(tok)
	require.Equal(t, 0, n)
	require.Equal(t, tok, out)
}

// ---------------------------------------------------------------------------
// Read-path tokenization + trusted-write restore
// ---------------------------------------------------------------------------

func TestRedact_AtIssuesTokensWhenEnabled(t *testing.T) {
	useRedaction(t, RedactionPolicyOptions{TokenizationEnabled: ptrBool(true)})

	got := redactSecretsAt("AWS_ACCESS_KEY_ID=\"AKIAIOSVPK253PIP5TGP\"", "deploy/app.env", "view")
	require.NotContains(t, got, "AKIAIOSVPK253PIP5TGP")
	require.Contains(t, got, "<secret:", "full-mode findings become tokens")

	// The token resolves back to the original credential.
	out, n := secretTokens.Restore(got)
	require.Greater(t, n, 0)
	require.Contains(t, out, "AKIAIOSVPK253PIP5TGP")
}

func TestRedact_AtStaticSentinelsWhenDisabled(t *testing.T) {
	// Tokenization off (the shipped default) keeps the byte-identical sentinel.
	got := redactSecretsAt("AWS_ACCESS_KEY_ID=\"AKIAIOSVPK253PIP5TGP\"", "deploy/app.env", "view")
	require.NotContains(t, got, "AKIAIOSVPK253PIP5TGP")
	require.Contains(t, got, "<redacted:gitleaks:", "default path emits the static sentinel")
	require.NotContains(t, got, "<secret:")
}

func TestRedact_SensitiveDotenvRoundTrips(t *testing.T) {
	useRedaction(t, RedactionPolicyOptions{TokenizationEnabled: ptrBool(true)})

	in := "DATABASE_URL=postgres://u:p@h/db\nLOG_LEVEL=debug\n"
	red := redactSensitiveContentByPath(in, ".env")
	require.NotContains(t, red, "postgres://u:p@h/db")
	require.Contains(t, red, "DATABASE_URL=")
	require.Contains(t, red, "<secret:dotenv:", "dotenv values become reversible tokens")

	// Writing that read output back to a trusted .env restores the real values.
	restored := restoreSecretTokensForWrite(red, ".env")
	require.Contains(t, restored, "postgres://u:p@h/db")
	require.Contains(t, restored, "LOG_LEVEL=debug")
}

func TestRestore_OnlyForTrustedTargets(t *testing.T) {
	useRedaction(t, RedactionPolicyOptions{TokenizationEnabled: ptrBool(true)})

	tok := secretTokens.Issue("dotenv", "super-secret-value")

	// A non-trusted destination keeps the inert token.
	require.Equal(t, tok, restoreSecretTokensForWrite(tok, "src/handler.go"))
	require.Equal(t, tok, restoreSecretTokensForWrite(tok, "README.md"))

	// A trusted sensitive destination gets the plaintext back.
	require.Equal(t, "super-secret-value", restoreSecretTokensForWrite(tok, ".env"))
	require.Equal(t, "super-secret-value", restoreSecretTokensForWrite(tok, "deploy/.env.production"))
	require.Equal(t, "super-secret-value", restoreSecretTokensForWrite(tok, ".envrc"))
}

func TestRestore_NoOpWhenTokenizationOff(t *testing.T) {
	// Even a trusted target is left untouched when tokenization is disabled, so
	// shipped writes are byte-identical to before this feature existed.
	tok := secretTokens.Issue("dotenv", "value-abc")
	require.Equal(t, tok, restoreSecretTokensForWrite(tok, ".env"))
}

func TestCheckSecrets_IgnoresOurTokensButBlocksTypedSecrets(t *testing.T) {
	// A token we issued is inert text and must never block a legitimate write.
	require.NoError(t, checkSecretsAt("API_TOKEN=<secret:aws-access-token:sec_ABCDEF>", ".env"))

	// A credential the model typed verbatim is still caught.
	require.Error(t, checkSecretsAt("AWS_ACCESS_KEY_ID=\"AKIAIOSVPK253PIP5TGP\"", "app.go"))

	// Token + a real typed secret: stripping the token must not hide the secret.
	require.Error(t, checkSecretsAt(
		"API_TOKEN=<secret:generic-api-key:sec_XYZ>\nAWS_ACCESS_KEY_ID=\"AKIAIOSVPK253PIP5TGP\"", "app.go"))
}

func TestStripTokens(t *testing.T) {
	in := "key <secret:dotenv:sec_ABC> value"
	out := stripTokens(in)
	require.NotContains(t, out, "<secret:")
	require.Contains(t, out, "«tok»")
	// Absent tokens leave content untouched.
	require.Equal(t, "plain", stripTokens("plain"))
}

func TestSanitizeTokenKind(t *testing.T) {
	require.Equal(t, "aws-access-token", sanitizeTokenKind("aws-access-token"))
	require.Equal(t, "a_b_c", sanitizeTokenKind("a/b.c"))
	require.Equal(t, "lower", sanitizeTokenKind("LOWER"))
	require.Equal(t, tokenKindSecret, sanitizeTokenKind(""))
}

// ---------------------------------------------------------------------------
// Read-path toggle
// ---------------------------------------------------------------------------

func TestRedactSecretsForTool_HonoursReadToggle(t *testing.T) {
	useRedaction(t, RedactionPolicyOptions{SecretsEnabled: ptrBool(false)})

	in := "AWS_ACCESS_KEY_ID=\"AKIAIOSVPK253PIP5TGP\""
	require.Equal(t, in, redactSecretsForTool(in, "app.go", "view"),
		"the read-path surface must not scan when the toggle is off")

	// The provider-boundary entrypoint is unconditional regardless of the toggle.
	require.NotContains(t, redactSecrets(in), "AKIAIOSVPK253PIP5TGP")
}
