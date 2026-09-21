package config

import (
	"strings"

	"github.com/hackafterdark/phosphor/pkg/secrets"
)

// registerConfiguredSecrets seeds the known-value redaction registry with the
// credential material this process loaded from configuration: provider API keys,
// OAuth access tokens, and the credential-named MCP env/header values. Those
// exact strings are then scrubbed from every read-path surface (bash stdout,
// file reads, MCP results) and from the provider wire, so a key that Phosphor
// itself holds can never leak into the conversation transcript through a `view`
// of phosphor.json or a `cat` of a keyring file, even though the child env is
// already allowlisted against it.
//
// It runs once at config load over the persisted configuration. Live token
// rotation and per-provider build register their values at their own sites (see
// ConfigStore.SetProviderAPIKey/applyToken/RefreshOAuthToken and the agent
// provider builder); this sweep covers what was on disk at startup, including
// providers that are configured but not selected for the current session.
//
// Values that still carry a shell template ($VAR / $(cmd)) are skipped: they are
// not the real secret yet, and the resolved value is registered at expansion or
// provider-build time. Registering the template form would only scrub the
// placeholder, not the credential.
func (c *Config) registerConfiguredSecrets() {
	if c == nil {
		return
	}
	for p := range c.Providers.Seq() {
		if !isTemplate(p.APIKey) {
			secrets.Register(p.APIKey)
		}
		if p.OAuthToken != nil {
			secrets.Register(p.OAuthToken.AccessToken)
		}
		for hk, hv := range p.ExtraHeaders {
			if secrets.IsCredentialKey(hk) && !isTemplate(hv) {
				secrets.Register(hv)
			}
		}
	}
	for _, m := range c.MCP.Sorted() {
		for ek, ev := range m.MCP.Env {
			if secrets.IsCredentialKey(ek) && !isTemplate(ev) {
				secrets.Register(ev)
			}
		}
		for hk, hv := range m.MCP.Headers {
			if secrets.IsCredentialKey(hk) && !isTemplate(hv) {
				secrets.Register(hv)
			}
		}
	}
}

// RegisterSecret adds an already-resolved secret string to the known-value
// registry. It is exported so the agent's provider builder and the config
// store's token paths can record a credential at the moment it materialises. The
// value is skipped when it is empty or still an unexpanded template.
func RegisterSecret(value string) {
	if isTemplate(value) {
		return
	}
	secrets.Register(value)
}

// registerCredentialValue records value in the registry only when its key names
// it a credential. This gates the MCP env/header sweep so benign values (PATH, a
// base URL, a non-secret toggle) are never registered and therefore never
// scrubbed from ordinary tool output — only values whose own key says they are
// secrets are.
func registerCredentialValue(key, value string) {
	if !secrets.IsCredentialKey(key) {
		return
	}
	RegisterSecret(value)
}

// RegisterCredentialValue is the exported form of registerCredentialValue for
// callers outside this package (the agent's provider builder) that resolve a
// header or env value keyed by name and want to record it only when the name
// marks it as a credential.
func RegisterCredentialValue(key, value string) { registerCredentialValue(key, value) }

// isTemplate reports whether a config value still contains a shell-expansion
// template and therefore has not yet been turned into a real secret.
func isTemplate(v string) bool {
	return strings.Contains(v, "$")
}
