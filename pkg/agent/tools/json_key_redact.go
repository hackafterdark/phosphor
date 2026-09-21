package tools

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
)

// Structured-JSON key-drop (secret-protection plan, Phase 7).
//
// Some tools — most notably MCP servers and JSON-emitting CLIs — hand back a
// whole JSON document as their result. For those payloads a cheap, precise
// complement to the value-scanning gitleaks pass is available: drop an entire
// field by its *key* rather than guessing at its value. A credential-manager MCP
// that returns {"SecretString":"...","SessionToken":"..."} does not need a
// regex to recognise the secret; the key name already says what it is. Key-based
// dropping is strictly cheaper (no regex, no entropy scan) and lower on false
// positives than value scanning, because we only ever act on a document we have
// already confirmed is JSON and only on keys that are themselves secret-shaped.
//
// It is a complement, not a replacement: the gitleaks value pass still runs
// afterwards over the same bytes, so an opaque secret hidden under a benign key
// (or a real key dropped into a document that is not itself sensitive) is still
// caught. This layer just removes the obvious, self-labelled ones before that.
//
// It never blocks and it never reorders or reserialises content that is not a
// whole JSON document: text that fails to parse as a single JSON object/array is
// returned verbatim, so ordinary command output and logs pass straight through.

// jsonKeySentinel replaces the value of any field whose key is recognised as
// secret-shaped. Like the other read-path sentinels it is deliberately not a
// syntactically valid credential, so echoing it back through an edit can never
// resurrect the original value.
const jsonKeySentinel = "<redacted:json:key>"

// jsonKeyScanLimit caps how large a payload we are willing to parse. A tool
// result larger than this is almost never a tidy credential document, and
// skipping the parse keeps the hot path allocation-free on the common case.
const jsonKeyScanLimit = 2 << 20 // 2 MiB

// defaultJSONSecretKeys is the built-in set of exact JSON object keys (stored
// lower-cased) whose value is dropped. It covers the common credential, token,
// and secret field names emitted by cloud SDKs and secret-manager MCP servers.
// Operators may only add to it via security.json_secret_keys, never remove.
func defaultJSONSecretKeys() map[string]bool {
	keys := []string{
		// Generic secret/credential material.
		"secret", "secrets", "secretstring", "secretvalue", "secretkey",
		"secret_key", "secret_value", "credentials", "credential",
		"clientsecret", "client_secret", "privatekey", "private_key",
		// Tokens.
		"token", "tokens", "accesstoken", "access_token", "access_token_key",
		"refreshtoken", "refresh_token", "idtoken", "id_token", "sessiontoken",
		"session_token", "authtoken", "auth_token", "bearertoken", "bearer_token",
		"securitytoken", "security_token", "authticket", "auth_ticket",
		// API keys.
		"apikey", "api_key", "api_secret", "apisecret", "x_api_key", "api_token",
		// Passwords.
		"password", "passwd", "pwd", "pass",
		// Cloud / vendor specific high-signal names.
		"aws_secret_access_key", "aws_secret_access_key_id",
		"authorization", "proxy_password", "connectionstring", "connection_string",
	}
	m := make(map[string]bool, len(keys))
	for _, k := range keys {
		m[k] = true
	}
	return m
}

// defaultJSONKeySuffixes are the trailing patterns a JSON key is matched against
// (also lower-cased), so vendor-spelled families such as "db_password",
// "github_token" or "hmac_secret" are dropped without enumerating every name.
// The set is kept deliberately narrow: "key" alone is not present because it
// false-positives on benign structure (sort key, primary key, cache key), while
// the qualified forms still cover the credential families.
func defaultJSONKeySuffixes() []string {
	return []string{
		"_secret", "secret", "_token", "token", "_password", "password",
		"_passwd", "passwd", "_apikey", "apikey", "_api_key", "_credential",
		"credential", "_private_key", "private_key", "_signing_key",
		"_access_key", "authorization",
	}
}

// redactSecretJSONFields reports whether out differs from text after dropping
// secret-named fields. It returns (text, false) untouched when the payload is
// not a single whole JSON object/array, is over the size cap, the layer is
// disabled, or no secret-named key was present. It never errors: a parse failure
// is simply "not JSON here", so callers can treat it as a no-op.
func redactSecretJSONFields(text string) (string, bool) {
	if !JSONKeyRedactionEnabled() {
		return text, false
	}
	trimmed := strings.TrimSpace(text)
	if trimmed == "" || len(trimmed) > jsonKeyScanLimit {
		return text, false
	}
	// Cheap shape gate: only documents that open like a JSON object or array are
	// worth handing to the parser. This keeps ordinary command output, which is
	// the overwhelming majority of tool results, allocation-free.
	if trimmed[0] != '{' && trimmed[0] != '[' {
		return text, false
	}

	dec := json.NewDecoder(strings.NewReader(trimmed))
	dec.UseNumber()
	// Reject anything that is not exactly one JSON value: a leading "{" that is
	// really prose, or JSON glued to log lines, must not be reshaped. RejectTrailingCommas
	// is deliberately not set so malformed JSON simply fails to decode.
	var val any
	if err := dec.Decode(&val); err != nil {
		return text, false
	}
	if dec.More() {
		// Trailing non-whitespace content: this was not a bare JSON document.
		return text, false
	}

	pol := currentRedactionPolicy()
	changed := dropSecretKeys(val, pol)
	if !changed {
		return text, false
	}

	// Re-serialise with HTML escaping off so the <redacted:json:key> marker stays
	// literal (encoding/json would otherwise turn the angle brackets into
	// \u003c… escapes, which is unreadable to the model and inconsistent with the
	// other read-path sentinels that appear verbatim in tool output).
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(val); err != nil {
		// We only ever splice a plain string into a value position, so this is
		// effectively unreachable; fail open by leaving the original intact and let
		// the value-scanning pass handle it.
		slog.Debug("JSON key-drop re-marshal failed; leaving payload intact")
		return text, false
	}
	return strings.TrimRight(buf.String(), "\n"), true
}

// dropSecretKeys walks a decoded JSON value in place, replacing the value of
// every secret-named object field with the sentinel and recursing into nested
// objects and arrays. It reports whether it changed anything, so a document with
// no secret keys is left byte-for-byte intact.
func dropSecretKeys(v any, pol *redactionPolicy) bool {
	switch node := v.(type) {
	case map[string]any:
		var changed bool
		for k := range node {
			if pol.isSecretJSONKey(k) {
				if !isSentinel(node[k]) {
					node[k] = jsonKeySentinel
					changed = true
				}
				continue
			}
			if dropSecretKeys(node[k], pol) {
				changed = true
			}
		}
		return changed
	case []any:
		var changed bool
		for _, elem := range node {
			if dropSecretKeys(elem, pol) {
				changed = true
			}
		}
		return changed
	default:
		return false
	}
}

// isSentinel reports whether a value is already the key-drop sentinel, so a
// document that already carries a dropped field is not counted as a new change.
func isSentinel(v any) bool {
	s, ok := v.(string)
	return ok && s == jsonKeySentinel
}

// RedactJSONForTool is the read-path entrypoint the MCP result and command-output
// surfaces call. It drops secret-named fields from a whole-JSON-document result,
// then hands the (possibly rewritten) text to the gitleaks value pass so the two
// layers compose. The source label is only used for logging.
func RedactJSONForTool(text, source string) string {
	if text == "" {
		return text
	}
	redacted, changed := redactSecretJSONFields(text)
	if changed {
		slog.Debug("Dropped secret fields from JSON tool result by key",
			"source", source,
		)
	}
	return redacted
}
