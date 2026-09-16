package tools

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDetectorWithSecretRulesLoadsCustomRule(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secret-rules.toml")
	require.NoError(t, os.WriteFile(path, []byte(`
[[rules]]
id = "phosphor-test-token"
description = "Test token"
regex = '''PHOSPHOR_TEST_TOKEN[=\s]+["']?([A-Z0-9]{16})["']?'''
secretGroup = 1
keywords = ["phosphor_test_token"]
`), 0o644))

	detector, err := detectorWithSecretRules(path)
	require.NoError(t, err)

	findings := detector.DetectString("PHOSPHOR_TEST_TOKEN=ABCDEFGHIJKLMNOP")
	require.NotEmpty(t, findings)

	var found bool
	for _, finding := range findings {
		if finding.RuleID == "phosphor-test-token" {
			found = true
			break
		}
	}
	require.True(t, found, "expected custom rule to match")
}

func TestDetectorWithSecretRulesRejectsUnknownRequiredRule(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secret-rules.toml")
	require.NoError(t, os.WriteFile(path, []byte(`
[[rules]]
id = "phosphor-required-host"
regex = '''missing.example'''
keywords = ["missing"]

[[rules]]
id = "phosphor-required-token"
regex = '''TOKEN=ABCDEFGHIJKLMNOP'''
keywords = ["token"]

[[rules.required]]
id = "phosphor-does-not-exist"
withinLines = 1
`), 0o644))

	_, err := detectorWithSecretRules(path)
	require.Error(t, err)
	require.Contains(t, err.Error(), "phosphor-does-not-exist")
}

func TestLoadCustomSecretRulesIgnoresMissingFile(t *testing.T) {
	rules, allowlists, err := loadCustomSecretRules(filepath.Join(t.TempDir(), "missing.toml"))
	require.NoError(t, err)
	require.Empty(t, rules)
	require.Empty(t, allowlists)
}
func TestDetectorWithSecretRulesAppliesCustomAllowlist(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secret-rules.toml")
	require.NoError(t, os.WriteFile(path, []byte(`
[[rules]]
id = "phosphor-allowlisted-token"
description = "Token"
regex = '''PHOSPHOR_ALLOWLISTED=[A-Z0-9]{16}'''
keywords = ["phosphor_allowlisted="]

[[allowlists]]
description = "Ignore fixture token"
regexTarget = "match"
regexes = ['''PHOSPHOR_ALLOWLISTED=[A-Z0-9]{16}''']
`), 0o644))

	detector, err := detectorWithSecretRules(path)
	require.NoError(t, err)

	findings := detector.DetectString("PHOSPHOR_ALLOWLISTED=ABCDEFGHIJKLMNOP")
	for _, finding := range findings {
		require.NotEqual(t, "phosphor-allowlisted-token", finding.RuleID)
	}
}
