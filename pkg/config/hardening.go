package config

import (
	"fmt"
	"log/slog"
	"slices"

	"github.com/hackafterdark/phosphor/pkg/shell"
)

// Config-evaluation hardening (malicious-repository defense).
//
// Phosphor merges configuration from several sources: trusted user/system files
// under the global config directory, then project- and workspace-local files that
// live inside the repository being opened (phosphor.json at the project root and
// .phosphor/phosphor.json). The last merge wins, so an untrusted repository that
// is checked out and opened could otherwise silently loosen a security boundary
// the operator set globally — turning off the provider-wire secret mask, shrinking
// the sensitive-file set, or broadening the environment the bash child may see.
//
// enforceSecurityFloors closes that hole. It is run against the fully-merged
// config with the *trusted* (global-only + built-in defaults) config as a floor:
// every security-relevant field may stay at or move more secure than the trusted
// baseline, but never less. When a workspace/project value tries to go below the
// floor it is reverted and a warning is logged naming the field, so the operator
// learns a repository attempted a downgrade rather than it happening silently.

// trustedSecurityFloor builds the floor config from only the trusted sources: the
// user/system global config files plus the built-in defaults. Project- and
// workspace-local files are deliberately excluded — those are the untrusted input
// this hardening clamps.
func trustedSecurityFloor(workingDir, dataDir string) *Config {
	floor := &Config{}
	if cfg, _, err := loadFromConfigPaths([]string{GlobalConfig(), GlobalConfigData()}); err == nil {
		floor = cfg
	}
	floor.setDefaults(workingDir, dataDir)
	return floor
}

// hardenAgainstWorkspaceOverrides clamps cfg's security-relevant fields to the
// trusted floor. cfg is the fully-merged config; floor is the global-only baseline.
// It reports how many fields had to be reverted (used by callers/tests).
func hardenAgainstWorkspaceOverrides(cfg, floor *Config) int {
	if cfg == nil || floor == nil {
		return 0
	}
	// Security is always non-nil after setDefaults, but be defensive on partial
	// configs built directly in tests.
	if cfg.Security == nil {
		cfg.Security = &SecurityConfig{}
	}
	if floor.Security == nil {
		floor.Security = &SecurityConfig{}
	}

	reverts := 0
	revert := func(field string, reason string) {
		reverts++
		slog.Warn(
			"Ignored workspace/project override that would weaken a security setting",
			"field", field,
			"reason", reason,
		)
	}

	// Secure-by-default tri-state masks: the floor keeps them on; a repo may not
	// switch one off. (nil == on.)
	floorTriState(&cfg.Security.WireSecretRedactionForce, floor.Security.WireSecretRedactionForce, "security.wire_secret_redaction_force", revert)
	floorTriState(&cfg.Security.RedactOutgoingSecrets, floor.Security.RedactOutgoingSecrets, "security.redact_outgoing_secrets", revert)
	floorTriState(&cfg.Security.RedactSensitiveFiles, floor.Security.RedactSensitiveFiles, "security.redact_sensitive_files", revert)
	floorTriState(&cfg.Security.RedactJSONKeys, floor.Security.RedactJSONKeys, "security.redact_json_keys", revert)
	floorTriState(&cfg.Security.LearnedSecretMemory, floor.Security.LearnedSecretMemory, "security.learned_secret_memory", revert)
	floorTriState(&cfg.Security.CodeFileFalsePositiveMode, floor.Security.CodeFileFalsePositiveMode, "security.code_file_false_positive_mode", revert)

	// Extend-only deny lists: the floor entries can never be dropped, only added.
	cfg.Security.SensitiveFilePatterns = requiredSuperset(
		cfg.Security.SensitiveFilePatterns, floor.Security.SensitiveFilePatterns,
		"security.sensitive_file_patterns", revert,
	)
	cfg.Security.ToolBlacklist = requiredSuperset(
		cfg.Security.ToolBlacklist, floor.Security.ToolBlacklist,
		"security.tool_blacklist", revert,
	)

	// Plain bools whose true value is the secure direction: a repo may turn them
	// on (more secure) but never off once the operator has them on.
	if floor.Security.ReadOnly {
		if !cfg.Security.ReadOnly {
			cfg.Security.ReadOnly = true
			revert("security.read_only", "workspace attempted to disable read-only mode")
		}
	}

	// Opt-in egress isolation is operator-only. Any workspace change to the whole
	// block (enabling it, editing the brokered host allowlist, or flipping its
	// sub-guards) is discarded in favour of the trusted floor.
	if !egressEqual(cfg.Security.EgressIsolation, floor.Security.EgressIsolation) {
		cfg.Security.EgressIsolation = cloneEgressIsolation(floor.Security.EgressIsolation)
		revert("security.egress_isolation", "workspace attempted to modify operator-only egress isolation")
	}

	// Platform egress toggles: a repo may not enable a channel the operator left
	// off (each is permissive when true).
	cfg.Security.AllowedEgress.HTTP = cfg.Security.AllowedEgress.HTTP && floor.Security.AllowedEgress.HTTP
	cfg.Security.AllowedEgress.Discord = cfg.Security.AllowedEgress.Discord && floor.Security.AllowedEgress.Discord
	cfg.Security.AllowedEgress.Slack = cfg.Security.AllowedEgress.Slack && floor.Security.AllowedEgress.Slack
	cfg.Security.AllowedEgress.Telegram = cfg.Security.AllowedEgress.Telegram && floor.Security.AllowedEgress.Telegram

	// Bash child-process controls.
	if !floor.Tools.Bash.AllowInlineExecution && cfg.Tools.Bash.AllowInlineExecution {
		cfg.Tools.Bash.AllowInlineExecution = false
		revert("tools.bash.allow_inline_execution", "workspace attempted to allow inline interpreter execution")
	}
	cfg.Tools.Bash.TrustedExtraRoots = noBroadenAllowList(
		cfg.Tools.Bash.TrustedExtraRoots, floor.Tools.Bash.TrustedExtraRoots,
		"tools.bash.trusted_extra_roots", nil, revert,
	)
	cfg.Tools.Bash.AllowedEnv = noBroadenAllowList(
		cfg.Tools.Bash.AllowedEnv, floor.Tools.Bash.AllowedEnv,
		"tools.bash.allowed_env", shell.SafeDefaultEnv(), revert,
	)

	// Web tools.
	if !floor.Tools.WebFetch.AllowRawIPs && cfg.Tools.WebFetch.AllowRawIPs {
		cfg.Tools.WebFetch.AllowRawIPs = false
		revert("tools.web_fetch.allow_raw_ips", "workspace attempted to allow raw IP destinations")
	}
	cfg.Tools.WebFetch.IPAllowList = noBroadenAllowList(
		cfg.Tools.WebFetch.IPAllowList, floor.Tools.WebFetch.IPAllowList,
		"tools.web_fetch.ip_allow_list", nil, revert,
	)

	return reverts
}

// floorTriState clamps a tri-state secure-by-default bool (nil == on) so it can
// never be turned off below a floor that keeps it on.
func floorTriState(field **bool, floorVal *bool, name string, revert func(string, string)) {
	floorOn := floorVal == nil || *floorVal
	currentOn := *field == nil || **field
	if floorOn && !currentOn {
		on := true
		*field = &on
		revert(name, "workspace attempted to disable a security mask")
	}
}

// requiredSuperset returns the union of current and floor so a workspace merge can
// add entries to a deny-style list but never remove a floor entry. It warns when an
// entry had to be restored.
func requiredSuperset(current, floor []string, name string, revert func(string, string)) []string {
	if len(floor) == 0 {
		return current
	}
	out := slices.Clone(current)
	var restored []string
	for _, want := range floor {
		if !slices.Contains(out, want) {
			out = append(out, want)
			restored = append(restored, want)
		}
	}
	if len(restored) > 0 {
		revert(name, fmt.Sprintf("workspace attempted to remove %d protected entr%v", len(restored), plural(restored)))
	}
	return out
}

// noBroadenAllowList keeps only the entries of current that are present in the
// trusted allow-set. An allow-list may be narrowed by a workspace but never
// broadened beyond the operator's setting. defaultFloor supplies the built-in
// trusted set to compare against when the floor list itself is empty (e.g. the
// bash child environment's safe default).
func noBroadenAllowList(current, floor []string, name string, defaultFloor []string, revert func(string, string)) []string {
	allowed := make(map[string]struct{}, len(floor)+len(defaultFloor))
	for _, a := range floor {
		allowed[a] = struct{}{}
	}
	for _, a := range defaultFloor {
		allowed[a] = struct{}{}
	}
	var dropped []string
	out := make([]string, 0, len(current))
	for _, c := range current {
		if _, ok := allowed[c]; ok {
			out = append(out, c)
		} else {
			dropped = append(dropped, c)
		}
	}
	if len(dropped) > 0 {
		revert(name, fmt.Sprintf("workspace attempted to broaden an allow-list past the operator setting (dropped %d entries)", len(dropped)))
		return out
	}
	return current
}

func plural(items []string) string {
	if len(items) == 1 {
		return "y"
	}
	return "ies"
}

func egressEqual(a, b *EgressIsolationConfig) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Enabled == b.Enabled &&
		triEqual(a.HTTPSOnly, b.HTTPSOnly) &&
		triEqual(a.SealDetectedSecrets, b.SealDetectedSecrets) &&
		triEqual(a.DenyPrivateIPs, b.DenyPrivateIPs) &&
		triEqual(a.RouteSubprocesses, b.RouteSubprocesses) &&
		a.MaxBodyBytes == b.MaxBodyBytes &&
		slices.Equal(a.AllowedHosts, b.AllowedHosts)
}

func triEqual(a, b *bool) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func cloneEgressIsolation(e *EgressIsolationConfig) *EgressIsolationConfig {
	if e == nil {
		return nil
	}
	c := *e
	c.AllowedHosts = slices.Clone(e.AllowedHosts)
	return &c
}
