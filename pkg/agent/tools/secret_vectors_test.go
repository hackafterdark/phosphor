package tools

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// secretVec is one adversarial write-path case for the gitleaks-backed gate.
// wantBlock means the content must be rejected; the raw secret must never leak
// into the returned message.
type secretVec struct {
	name      string
	content   string
	path      string
	wantBlock bool
	secret    string
}

// realSecrets are credential-shaped values gitleaks' default ruleset catches.
func TestCheckSecrets_Vectors(t *testing.T) {
	t.Parallel()

	vectors := []secretVec{
		{
			name:      "aws_access_key_id",
			content:   "AWS_ACCESS_KEY_ID := \"AKIAIOSVPK253PIP5TGP\"",
			path:      "config/settings.go",
			wantBlock: true,
			secret:    "AKIAIOSVPK253PIP5TGP",
		},
		{
			name: "pem_private_key",
			content: "-----BEGIN RSA PRIVATE KEY-----\n" +
				"MIIEvgIBAAKCAQEA0y2Q3851260BAAQCAQAga252Q3851260BAAQCAQAga252\n" +
				"Q3851260BAAQCAQAga252Q3851260BAAQCAQAga252Q3851260BAAQCAQAg\n" +
				"-----END RSA PRIVATE KEY-----",
			path:      "deploy/key.pem",
			wantBlock: true,
			secret:    "MIIEvgIBAAKCAQEA0y2Q3851260BAAQCAQAga252",
		},
		{
			name:      "github_pat",
			content:   "GH_TOKEN := \"ghp_125JKPSG25JQ253PIP5TG25P25KPSG\"",
			path:      ".github/workflows/ci.yml",
			wantBlock: true,
			secret:    "ghp_125JKPSG25JQ253PIP5TG25P25KPSG",
		},
		{
			name:      "stripe_secret_live",
			content:   "stripe_key := \"sk_live_125JKPSG25JQ253PIP5TG25P\"",
			path:      "payments/stripe.go",
			wantBlock: true,
			secret:    "sk_live_125JKPSG25JQ253PIP5TG25P",
		},
		{
			name:      "generic_high_entropy_api_key",
			content:   "api_key := \"AIzaSyD125JKPSG25JQ253PIP5TG25P25K\"",
			path:      "services/client.go",
			wantBlock: true,
			secret:    "AIzaSyD125JKPSG25JQ253PIP5TG25P25K",
		},
		{
			name:      "slack_bot_token",
			content:   "SLACK_BOT_TOKEN := \"xoxb-4125JKPSG25JQ253PIP-25Tg\"",
			path:      "integrations/slack.go",
			wantBlock: true,
			secret:    "xoxb-4125JKPSG25JQ253PIP-25Tg",
		},
	}

	for _, v := range vectors {
		t.Run(v.name, func(t *testing.T) {
			t.Parallel()

			err := checkSecretsAt(v.content, v.path)
			require.Error(t, err, "expected %q to be blocked", v.name)
			require.Contains(t, err.Error(), "Security violation")
			// The matched credential must never be echoed back to the model.
			require.NotContains(t, err.Error(), v.secret, "message leaked the secret")
		})
	}
}

// TestCheckSecrets_Allowlists proves content scanning is skipped for test/doc
// material and empty input, and that gitleaks' inline opt-out comment works.
func TestCheckSecrets_Allowlists(t *testing.T) {
	t.Parallel()

	const secret = "AKIAIOSVPK253PIP5TGP"
	const withSecret = "AWS_ACCESS_KEY_ID := \"" + secret + "\""

	cases := []struct {
		name string
		path string
	}{
		{"testdata dir", "internal/foo/testdata/fixture.go"},
		{"fixtures dir", "test/fixtures/config.go"},
		{"snapshots dir", "src/__snapshots__/out.md"},
		{"vendor dir", "vendor/example/pkg/lib.go"},
		{"node_modules", "node_modules/left-pad/index.js"},
		{"go test file", "pkg/agent/tools/secrets_test.go"},
		{"example file", "config/prod.go.example"},
		{"sample file", "config/prod.sample"},
		{"template file", "config/prod.template"},
		{"markdown doc", "docs/RUNBOOK.md"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			require.NoError(t, checkSecretsAt(withSecret, c.path))
		})
	}

	// Empty content never trips the gate.
	require.NoError(t, checkSecretsAt("", "app.go"))

	// The inline gitleaks opt-out comment suppresses a finding in scannable code.
	optOut := "// gitleaks:allow generic-api-key\napi_key := \"AIzaSyD125JKPSG25JQ253PIP5TG25P25K\""
	require.NoError(t, checkSecretsAt(optOut, "services/client.go"))
}

// TestCheckSecrets_Negatives proves ordinary source code is not falsely blocked.
func TestCheckSecrets_Negatives(t *testing.T) {
	t.Parallel()

	negatives := []string{
		`const myVar = "hello world"`,
		`func main() { fmt.Println("hi") }`,
		`api_key := "your_key_here"`, // low-entropy placeholder, not a real secret
		`// sha256 = 2c02f3695f23487c68b3695f23487c68b3695f23487c68b3695f23487c68b36`,
		`password := ""`,
	}
	for _, content := range negatives {
		t.Run(strings.Trim(content, "\""), func(t *testing.T) {
			t.Parallel()
			require.NoError(t, checkSecretsAt(content, "src/app.go"))
		})
	}
}

// TestCheckSecrets_DelegatesToPath checks that the no-path entrypoint behaves
// like checkSecretsAt with an empty path (full scan, no path allowlisting).
func TestCheckSecrets_DelegatesToPath(t *testing.T) {
	t.Parallel()

	err := checkSecrets("AWS_ACCESS_KEY_ID := \"AKIAIOSVPK253PIP5TGP\"")
	require.Error(t, err)
	require.NotContains(t, err.Error(), "AKIAIOSVPK253PIP5TGP")

	// A fixture path that would be skipped via checkSecretsAt is still scanned
	// here because there is no path to allowlist against.
	require.Error(t, checkSecrets("AWS_ACCESS_KEY_ID := \"AKIAIOSVPK253PIP5TGP\""))
}
