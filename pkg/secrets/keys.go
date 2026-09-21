package secrets

import "strings"

// IsCredentialKey reports whether an environment-variable name, header name, or
// JSON key is itself a credential marker: either an exact always-credential name
// or a name carrying one of the credential suffix fragments. It is the shared
// name-shape predicate used to decide which config/MCP env+header values are
// worth registering in the registry and which subprocess env keys are unsafe to
// pass through, so the notion of "this key names a secret" lives in exactly one
// place.
//
// Matching is on the trimmed, lower-cased key. The check is intentionally a
// heuristic name classifier (not a value detector): a value is only registered
// when its *key* says it is a credential, which keeps the registry from
// scrubbing benign config such as a sort/cache/primary "key".
func IsCredentialKey(key string) bool {
	k := strings.ToLower(strings.TrimSpace(key))
	if k == "" {
		return false
	}
	if _, ok := credentialExactKeys[k]; ok {
		return true
	}
	for _, suffix := range credentialKeySuffixes {
		if strings.HasSuffix(k, suffix) {
			return true
		}
	}
	return false
}

// credentialExactKeys are standalone names that are a credential by convention.
var credentialExactKeys = map[string]struct{}{
	"authorization":         {},
	"proxy_authorization":   {},
	"password":              {},
	"passwd":                {},
	"pwd":                   {},
	"token":                 {},
	"secret":                {},
	"api_key":               {},
	"apikey":                {},
	"private_key":           {},
	"credentials":           {},
	"client_secret":         {},
	"aws_secret_access_key": {},
	"aws_session_token":     {},
	"aws_access_key_id":     {},
	"azure_client_secret":   {},
	"docker_password":       {},
	"gh_token":              {},
	"github_token":          {},
	"gitlab_token":          {},
}

// credentialKeySuffixes are the trailing name fragments that mark a key as a
// credential. They require no leading separator so the bare vendor spellings
// (APITOKEN, CLIENTSECRET) are caught alongside the conventional ones.
var credentialKeySuffixes = []string{
	"_api_key", "api_key", "_apikey", "apikey",
	"_secret", "secret",
	"_token", "token",
	"_password", "password", "_passwd", "passwd",
	"_private_key", "private_key",
	"_credentials", "credentials", "_credential", "credential",
	"_access_key", "access_key",
	"authorization",
}
