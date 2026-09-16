package tools

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// awsKeyForTest is a gitleaks-detectable AWS access key id used to prove the
// content detector (B) still fires on files that are exempt from the blunt
// whole-value rule (A), such as committed .env templates.
const awsKeyForTest = "AKIAIOSVPK253PIP5TGP"

// usePolicy installs a sensitive-file policy for the duration of a test and
// restores the previous one after, so tests that flip the master switch or add
// globs do not leak into their neighbours.
func usePolicy(t *testing.T, enabled bool, extras []string) {
	t.Helper()
	prev := sensitivePolicy.Load()
	t.Cleanup(func() { sensitivePolicy.Store(prev) })
	SetSensitiveFilePolicy(enabled, extras)
}

func TestClassifySensitiveFile(t *testing.T) {
	usePolicy(t, true, nil)

	cases := []struct {
		name      string
		path      string
		wantTier  sensitiveTier
		wantShape sensitiveShape
	}{
		{".env", ".env", tierSensitive, shapeDotenv},
		{".env.production", "deploy/.env.production", tierSensitive, shapeDotenv},
		{"prod.env", "config/prod.env", tierSensitive, shapeDotenv},
		{".envrc-mixed", ".envrc", tierMixed, shapeDotenv},
		{"credentials-json", "gcp/credentials.json", tierSensitive, shapeJSON},
		{"service-account-json", "svc/my-service-account.json", tierSensitive, shapeJSON},
		{"secrets-dot-star", "config/secrets.yaml", tierSensitive, shapeJSON},
		{"pem-opaque", "certs/server.pem", tierSensitive, shapeOpaque},
		{"id-rsa-opaque", "~/.ssh/id_rsa", tierSensitive, shapeOpaque},
		{"template-exempt", "docs/.env.example", tierTemplate, shapeNone},
		{"ordinary-source", "internal/app/app.go", tierNone, shapeNone},
		{"ordinary-json", "pkg/api/response.json", tierNone, shapeNone},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tier, shape := classifySensitiveFile(c.path)
			require.Equal(t, c.wantTier, tier, "tier")
			require.Equal(t, c.wantShape, shape, "shape")
		})
	}
}

func TestRedactSensitiveContentByPath_DotenvKeepsKeys(t *testing.T) {
	usePolicy(t, true, nil)

	in := "" +
		"# a comment mentioning KEY=value must stay\n" +
		"\n" +
		"DATABASE_URL=postgres://localhost/app\n" +
		"export STRIPE_API_KEY=sk_live_abc123def456\n" +
		"EMPTY_OK=\n" +
		"  INDENTED_KEY=  spaced value here\n"
	out := redactSensitiveContentByPath(in, ".env")

	// Keys survive, values become the sentinel.
	require.Contains(t, out, "DATABASE_URL="+valueSentinel)
	require.Contains(t, out, "export STRIPE_API_KEY="+valueSentinel)
	require.NotContains(t, out, "postgres://localhost/app")
	require.NotContains(t, out, "sk_live_abc123def456")
	// An empty value is not a secret and is left visible; the comment and blank
	// line are untouched.
	require.Contains(t, out, "EMPTY_OK=\n")
	require.Contains(t, out, "# a comment mentioning KEY=value must stay")
}

func TestRedactSensitiveContentByPath_JSON(t *testing.T) {
	usePolicy(t, true, nil)

	in := "{\n" +
		"  \"type\": \"service_account\",\n" +
		"  \"project_id\": \"my-proj\",\n" +
		"  \"private_key_id\": 1234567890,\n" +
		"  \"private_key\": \"-----BEGIN PRIVATE KEY-----\\nMIIFAKE\\n\",\n" +
		"  \"client_email\": \"svc@proj.iam.gcp\"\n" +
		"}\n"
	out := redactSensitiveContentByPath(in, "svc/my-service-account.json")

	// Structure and keys stay; the numeric field stays (it is not a value string).
	require.Contains(t, out, "\"type\":")
	require.Contains(t, out, "\"private_key_id\": 1234567890")
	// Every string value is dropped.
	require.NotContains(t, out, "service_account")
	require.NotContains(t, out, "my-proj")
	require.NotContains(t, out, "MIIFAKE")
	require.NotContains(t, out, "svc@proj.iam.gcp")
	require.Contains(t, out, valueSentinel)
}

func TestRedactSensitiveContentByPath_JSONColonInKeyIsSafe(t *testing.T) {
	usePolicy(t, true, nil)

	// A colon inside the key token must not be mistaken for the key/value split.
	in := "{ \"time:zone\": \"utc\", \"token\": \"sekret123\" }"
	out := redactSensitiveContentByPath(in, "secrets.json")
	require.NotContains(t, out, "sekret123")
	// The key that itself contains a colon is preserved verbatim.
	require.Contains(t, out, "\"time:zone\"")
}

func TestRedactSensitiveContentByPath_Opaque(t *testing.T) {
	usePolicy(t, true, nil)

	in := "-----BEGIN RSA PRIVATE KEY-----\nMIIEvGtleBase64Blob==\n-----END RSA PRIVATE KEY-----\n"
	out := redactSensitiveContentByPath(in, "~/.ssh/id_rsa")
	require.Equal(t, opaqueSentinel+"\n", out)
	require.NotContains(t, out, "MIIEvGtleBase64Blob")
}

func TestRedactSensitive_MixedAllowlist(t *testing.T) {
	usePolicy(t, true, nil)

	in := "" +
		"use flake\n" +
		"export PATH=/run/current\n" +
		"export NODE_ENV=development\n" +
		"export LOG_LEVEL=debug\n" +
		"export GITHUB_TOKEN=ghp_secret_value_1234567890\n"
	out := redactSensitiveContentByPath(in, ".envrc")

	// Non-secret shell setup stays visible for the agent.
	require.Contains(t, out, "use flake")
	require.Contains(t, out, "export PATH=/run/current")
	require.Contains(t, out, "export NODE_ENV=development")
	require.Contains(t, out, "export LOG_LEVEL=debug")
	// A token-shaped value is still scrubbed.
	require.NotContains(t, out, "ghp_secret_value_1234567890")
	require.Contains(t, out, "export GITHUB_TOKEN="+valueSentinel)
}

func TestRedactSensitive_Template_ExemptFromA_BStillRuns(t *testing.T) {
	usePolicy(t, true, nil)

	// A committed template keeps its informative placeholders (exempt from the
	// blunt whole-value rule A) but a real key dropped into it is scrubbed by the
	// detector (B). This is what makes "example files are the exception" safe.
	in := "" +
		"DATABASE_URL=postgres://localhost/app\n" +
		"LOG_FORMAT=pretty\n" +
		"YOUR_API_KEY=your_key_here\n" +
		"AWS_ACCESS_KEY_ID=" + awsKeyForTest + "\n"
	out := redactReadContent(in, "docs/.env.example", "view")

	// (A) is skipped: the placeholders/informative values are still there.
	require.Contains(t, out, "LOG_FORMAT=pretty")
	require.Contains(t, out, "YOUR_API_KEY=your_key_here")
	require.Contains(t, out, "DATABASE_URL=postgres://localhost/app")
	// (B) still fires: the real AWS key id never reaches the transcript.
	require.NotContains(t, out, awsKeyForTest)
	require.Contains(t, out, "<redacted:gitleaks:")
}

func TestRedactSensitive_DisabledIsPassthrough(t *testing.T) {
	usePolicy(t, false, nil)

	in := "SECRET_VALUE=supersecret123456\n"
	out := redactSensitiveContentByPath(in, ".env")
	require.Equal(t, in, out)
}

func TestSensitiveFile_ExtraPatternsExtend(t *testing.T) {
	usePolicy(t, true, []string{"*.tfvars", "config/*.vault"})

	// Operator-added dotenv files become sensitive with the dotenv shape.
	out := redactSensitiveContentByPath("VAULT_TOKEN=s.1234567890abc\n", "config/prod.vault")
	require.Contains(t, out, "VAULT_TOKEN="+valueSentinel)
	require.NotContains(t, out, "s.1234567890abc")
}

func TestSensitiveFile_PathGlobMatchesWholePath(t *testing.T) {
	// .tfvars is not in the default set, so only the operator's slash-bearing
	// glob can make these sensitive, and only when the whole path lines up.
	usePolicy(t, true, []string{"deploy/prod/*.tfvars"})

	require.Contains(t, redactSensitiveContentByPath("A=1\n", "deploy/prod/x.tfvars"), "A="+valueSentinel)
	require.Equal(t, "A=1\n", redactSensitiveContentByPath("A=1\n", "deploy/dev/x.tfvars"))
}

func TestRedactSensitiveOutputForCommand(t *testing.T) {
	usePolicy(t, true, nil)

	// `cat .env` exposes path-less bytes, but the argv reveals the sensitive
	// reference, so the whole-value rule is applied to the output.
	out := redactSensitiveOutputForCommand("DB_URL=postgres://u:p@h/db\nPUBLIC=1\n", "cat .env")
	require.Contains(t, out, "DB_URL="+valueSentinel)
	require.NotContains(t, out, "postgres://u:p@h/db")

	// A command that does not reference a sensitive path is left alone, even when
	// its output happens to contain KEY=value-shaped lines (this is the whole
	// point of gating (A) on the path rather than the content).
	benign := "SECRET_HACK=notactuallysecret\n"
	require.Equal(t, benign, redactSensitiveOutputForCommand(benign, "cat README.md"))

	// An opaque reference redacts the whole body.
	require.Contains(t, redactSensitiveOutputForCommand("-----BEGIN PRIVATE KEY-----\nMIIFAKE\n", "cat server.pem"), opaqueSentinel)

	// Flag-shaped tokens are ignored, real path tokens still count.
	require.Contains(t, redactSensitiveOutputForCommand("TOK=abc\n", "grep -R --include=* . .env"), "TOK="+valueSentinel)
}

func TestContentScanExcluded_DotenvTemplatesStillScanned(t *testing.T) {
	// The .env family is never allow-listed, so a template's real key is scrubbed.
	require.False(t, contentScanExcluded("docs/.env.example"))
	require.False(t, contentScanExcluded("config/.env.sample"))
	// ... while ordinary doc/test material is still skipped.
	require.True(t, contentScanExcluded("docs/RUNBOOK.md"))
	require.True(t, contentScanExcluded("pkg/x/testdata/fixture.go"))

	out := redactSecretsAt("AWS_ACCESS_KEY_ID="+awsKeyForTest, "config/.env.example", "view")
	require.NotContains(t, out, awsKeyForTest)
}
