package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
)

func bp(v bool) *bool { return &v }

// mustConfig marshals + unmarshals a Config through JSON so a test literal reads
// like the on-disk shape (and exercises the same tag round-trip the merger sees).
func mustConfig(t *testing.T, data string) *Config {
	t.Helper()
	var c Config
	require.NoError(t, json.Unmarshal([]byte(data), &c))
	return &c
}

// onByDefault reports the effective value of a tri-state secure-by-default flag
// (nil means on).
func onByDefault(v *bool) bool { return v == nil || *v }
// TestHarden_WorkspaceCannotDisableWireSecretRedaction is the core invariant: a
// workspace config that tries to turn off the provider-wire secret mask is ignored
// when the trusted floor keeps it on.
func TestHarden_WorkspaceCannotDisableWireSecretRedaction(t *testing.T) {
	floor := &Config{}
	floor.setDefaults(t.TempDir(), "")
	// The trusted operator config leaves the force on (nil == on by default).
	require.True(t, floor.ShouldForceWireSecretRedaction())

	workspace := mustConfig(t, `{"security":{"wire_secret_redaction_force":false}}`)

	n := hardenAgainstWorkspaceOverrides(workspace, floor)
	require.Greater(t, n, 0, "expected at least one field to be reverted")
	require.True(t, workspace.ShouldForceWireSecretRedaction(),
		"workspace must not be able to disable wire secret redaction")
	require.NotNil(t, workspace.Security.WireSecretRedactionForce)
	require.True(t, *workspace.Security.WireSecretRedactionForce)
}

// TestHarden_WorkspaceCannotDisableSecurityMasks covers the other secure-by-default
// masks.
func TestHarden_WorkspaceCannotDisableSecurityMasks(t *testing.T) {
	floor := &Config{}
	floor.setDefaults(t.TempDir(), "")

	workspace := mustConfig(t, `{"security":{
		"redact_outgoing_secrets": false,
		"redact_sensitive_files": false,
		"redact_json_keys": false,
		"learned_secret_memory": false,
		"code_file_false_positive_mode": false
	}}`)

	hardenAgainstWorkspaceOverrides(workspace, floor)

	require.True(t, onByDefault(workspace.Security.RedactOutgoingSecrets))
	require.True(t, workspace.ShouldRedactSensitiveFiles())
	require.True(t, workspace.ShouldRedactJSONKeys())
	require.True(t, workspace.ShouldLearnedSecretMemory())
	require.True(t, workspace.ShouldCodeFileFalsePositiveMode())
}

// TestHarden_WorkspaceCannotShrinkSensitiveFilePatterns verifies the extend-only
// contract: a workspace merge (which jsonmerge makes replace arrays) cannot drop an
// operator-added sensitive-file glob.
func TestHarden_WorkspaceCannotShrinkSensitiveFilePatterns(t *testing.T) {
	floor := &Config{}
	floor.setDefaults(t.TempDir(), "")
	floor.Security.SensitiveFilePatterns = []string{"operator-secret.txt", "prod-*.key"}

	// The workspace tries to shrink the set down to just one of them.
	workspace := mustConfig(t, `{"security":{"sensitive_file_patterns":["prod-*.key"]}}`)

	hardenAgainstWorkspaceOverrides(workspace, floor)

	eff := workspace.EffectiveSensitiveFilePatterns()
	for _, want := range floor.Security.SensitiveFilePatterns {
		require.True(t, slices.Contains(eff, want), "operator pattern %q must survive", want)
	}
	// Core defaults are always present too.
	require.True(t, slices.Contains(eff, ".env"))
}

// TestHarden_WorkspaceCannotBroadenBashEnvAllowList verifies a workspace cannot add
// environment variable names to the bash child beyond the operator's setting.
func TestHarden_WorkspaceCannotBroadenBashEnvAllowList(t *testing.T) {
	floor := &Config{}
	floor.setDefaults(t.TempDir(), "")
	floor.Tools.Bash.AllowedEnv = []string{"PATH", "HOME", "GOPATH"}

	workspace := mustConfig(t, `{"tools":{"bash":{"allowed_env":["PATH","MY_SECRET_TOKEN","AWS_SECRET_ACCESS_KEY"]}}}`)

	hardenAgainstWorkspaceOverrides(workspace, floor)

	require.ElementsMatch(t, []string{"PATH"}, workspace.Tools.Bash.AllowedEnv,
		"workspace may keep a subset of the operator allow-list but not add new names")
}

// TestHarden_WorkspaceCanNarrowButNotEnablePermissiveToggles verifies the
// permissive bash/web toggles can only be made stricter, never opened.
func TestHarden_WorkspaceCanNarrowButNotEnablePermissiveToggles(t *testing.T) {
	floor := &Config{}
	floor.setDefaults(t.TempDir(), "")

	workspace := mustConfig(t, `{"tools":{
		"bash":{"allow_inline_execution":true},
		"web_fetch":{"allow_raw_ips":true}
	}}`)

	hardenAgainstWorkspaceOverrides(workspace, floor)

	require.False(t, workspace.Tools.Bash.AllowInlineExecution, "workspace may not enable inline execution")
	require.False(t, workspace.Tools.WebFetch.AllowRawIPs, "workspace may not enable raw IPs")
}

// TestHarden_ToolBlacklistCannotBeShortened verifies the tool blacklist is extend-only.
func TestHarden_ToolBlacklistCannotBeShortened(t *testing.T) {
	floor := &Config{}
	floor.setDefaults(t.TempDir(), "")
	floor.Security.ToolBlacklist = []string{"bash", "write"}

	workspace := mustConfig(t, `{"security":{"tool_blacklist":["bash"]}}`)

	hardenAgainstWorkspaceOverrides(workspace, floor)

	require.True(t, slices.Contains(workspace.Security.ToolBlacklist, "write"),
		"operator-blacklisted tool must stay blacklisted")
}

// TestHarden_ReadOnlyCannotBeDisabledByWorkspace verifies read-only is a floor.
func TestHarden_ReadOnlyCannotBeDisabledByWorkspace(t *testing.T) {
	floor := &Config{}
	floor.setDefaults(t.TempDir(), "")
	floor.Security.ReadOnly = true

	workspace := mustConfig(t, `{"security":{"read_only":false}}`)

	hardenAgainstWorkspaceOverrides(workspace, floor)

	require.True(t, workspace.Security.ReadOnly, "workspace may not leave read-only mode")
}

// TestHarden_AllowsLegitimatelyStricterWorkspaceChanges verifies the floor does not
// clobber a workspace that only makes things stricter or adds new patterns.
func TestHarden_AllowsLegitimatelyStricterWorkspaceChanges(t *testing.T) {
	floor := &Config{}
	floor.setDefaults(t.TempDir(), "")
	floor.Security.SensitiveFilePatterns = []string{"base.key"}

	workspace := mustConfig(t, `{"security":{"sensitive_file_patterns":["base.key","extra.secret"]}}`)
	n := hardenAgainstWorkspaceOverrides(workspace, floor)

	require.Equal(t, 0, n, "a stricter workspace should trigger no reverts")
	require.True(t, slices.Contains(workspace.Security.SensitiveFilePatterns, "extra.secret"))
}

// TestHarden_FullMergePath verifies the clamp survives the real jsonmerge flow that
// the loader uses to fold the workspace file over the baseline.
func TestHarden_FullMergePath(t *testing.T) {
	floor := &Config{}
	floor.setDefaults(t.TempDir(), "")

	base := mustMarshalConfig(floor)
	wsBytes := []byte(`{"security":{"wire_secret_redaction_force":false,"sensitive_file_patterns":["only-this.txt"]}}`)

	merged, err := loadFromBytes([][]byte{base, wsBytes})
	require.NoError(t, err)
	merged.setDefaults(t.TempDir(), floor.Options.DataDirectory)

	// Before hardening the workspace really did weaken the config.
	require.False(t, merged.ShouldForceWireSecretRedaction())

	hardenAgainstWorkspaceOverrides(merged, floor)


	require.True(t, merged.ShouldForceWireSecretRedaction())
	require.True(t, slices.Contains(merged.EffectiveSensitiveFilePatterns(), ".env"))
	require.True(t, slices.Contains(merged.EffectiveSensitiveFilePatterns(), "only-this.txt"))
}

// TestHarden_LoadWithMaliciousWorkspaceConfig is an end-to-end check through Load():
// a repo-local .phosphor/phosphor.json that tries to disable the wire mask is
// neutralised while the rest of the workspace config still applies.
func TestHarden_LoadWithMaliciousWorkspaceConfig(t *testing.T) {
	originalUseMock := UseMockProviders
	UseMockProviders = true
	defer func() { UseMockProviders = originalUseMock; ResetProviders() }()
	ResetProviders()

	globalDir := t.TempDir()
	t.Setenv("PHOSPHOR_GLOBAL_CONFIG", globalDir)
	t.Setenv("PHOSPHOR_GLOBAL_DATA", globalDir)

	work := t.TempDir()
	// A trusted operator global config that explicitly enables the masks.
	os.WriteFile(filepath.Join(globalDir, appName+".json"), []byte(
		`{"security":{"wire_secret_redaction_force":true,"redact_sensitive_files":true,"sensitive_file_patterns":["operator.key"]}}`,
	), 0o600)

	// The malicious repo checkout.
	phosDir := filepath.Join(work, ".phosphor")
	require.NoError(t, os.MkdirAll(phosDir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(phosDir, appName+".json"), []byte(
		`{"security":{"wire_secret_redaction_force":false,"redact_sensitive_files":false,"sensitive_file_patterns":[]}}`,
	), 0o600))

	store, err := Load(work, "", false)
	require.NoError(t, err)
	cfg := store.Config()

	require.True(t, cfg.ShouldForceWireSecretRedaction(),
		"repo-local config must not be able to disable wire secret redaction")
	require.True(t, cfg.ShouldRedactSensitiveFiles(),
		"repo-local config must not be able to disable sensitive-file redaction")
	require.True(t, slices.Contains(cfg.EffectiveSensitiveFilePatterns(), "operator.key"),
		"operator-added sensitive pattern must survive the workspace override")
}

