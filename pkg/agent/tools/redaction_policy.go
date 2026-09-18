package tools

import (
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// The secret-protection read/redaction pipeline reads its runtime switches from
// an immutable snapshot installed once at agent construction, never from mutable
// process state on the hot path. That is the "env snapshot" discipline: a
// prompt-injected tool that mutates the environment or re-reads config
// mid-session cannot silently flip these guards off, because nothing consults
// live state per call. The sensitive-file toggle lives in its own snapshot
// (see sensitive_files.go) and is installed alongside this one by the
// coordinator; both are frozen at the same init boundary.
type redactionPolicy struct {
	// secretsEnabled gates the gitleaks pass over read-path tool output (view,
	// grep, bash, mcp, job_output). The secure default is on.
	secretsEnabled bool
	// codeFileFPEnabled turns on the context-aware false-positive suppression:
	// when a scan is known to be reading source code, the low-precision
	// generic KEY=value / "apiKey":"value" rule family is skipped while the
	// high-precision vendor-prefix, PEM, JWT, auth-header and connection-string
	// checks still run. On by default.
	codeFileFPEnabled bool
	// tokenizationEnabled turns reversible-by-id tokenization on. Off by
	// default: the shipped read path emits static sentinels, which are already
	// non-reusable. When on, full-mode findings and sensitive-file values are
	// issued as <secret:kind:id> tokens resolvable only for trusted writes.
	tokenizationEnabled bool
	// genericPrefixes and genericRuleIDs identify the low-precision rule family
	// suppressed in code-file mode. Operators can extend the set; it always
	// contains the built-in "generic-" prefix.
	genericPrefixes []string
	genericRuleIDs  map[string]bool
	// tokenTTL and tokenMaxEntries bound the in-process token store.
	tokenTTL        time.Duration
	tokenMaxEntries int
	// jsonKeyRedactionEnabled turns the structured-JSON key-drop layer on. On by
	// default: it is a cheap, low-false-positive complement to value scanning for
	// JSON-producing tools (MCP/CLI results), because it drops a whole field by
	// its key name rather than guessing at the value. See json_key_redact.go.
	jsonKeyRedactionEnabled bool
	// jsonSecretKeys are the exact (lower-cased) JSON object keys whose values are
	// dropped when a JSON document is scanned. Operators can extend the set; the
	// built-in set is always present and cannot be subtracted from.
	jsonSecretKeys map[string]bool
	// jsonKeySuffixes are the trailing patterns a JSON object key is matched
	// against (also lower-cased), so *_token / *_secret style names are dropped
	// without enumerating every vendor spelling.
	jsonKeySuffixes []string
	// learnedSecretMemoryEnabled turns the hashed learned-secret memory on. On by
	// default. When on, every value the detector judges sensitive has its keyed
	// hash (HMAC) remembered, and a later detection of that same value is treated
	// as sensitive even where a per-context false-positive rule would otherwise
	// drop it (e.g. a generic KEY=value hit on a source file). Only digests are
	// kept, never the plaintext. See pkg/secrets/learned.go.
	learnedSecretMemoryEnabled bool
	// sealSentinels turns the opt-in architectural egress-isolation tier's read
	// path on. When on, a gitleaks-detected credential is replaced by a sealed
	// [egress] token (AES-256-GCM under a process key) rather than the plain
	// non-reusable <redacted:...> sentinel, so the agent can still round-trip it
	// through the broker while the transcript carries only an inert handle. Off
	// by default: it is armed only when the operator enables egress isolation AND
	// sealing, so shipped read output is byte-for-byte unchanged otherwise.
	sealSentinels bool
}

// RedactionPolicyOptions is the operator-facing knob set for the read-path
// redaction snapshot. A nil field means "use the secure default".
type RedactionPolicyOptions struct {
	SecretsEnabled          *bool
	CodeFileFPEnabled       *bool
	TokenizationEnabled     *bool
	ExtraGenericRules       []string
	TokenTTL                time.Duration
	TokenMaxEntries         int
	JSONKeyRedactionEnabled *bool
	ExtraJSONSecretKeys     []string
	// LearnedSecretMemoryEnabled gates the hashed learned-secret memory. A nil
	// field means the secure default (on).
	LearnedSecretMemoryEnabled *bool
	// SealSentinelsEnabled arms the egress-isolation read path: a detected
	// credential becomes a sealed, broker-resolvable token rather than the plain
	// non-reusable sentinel. A nil field means the secure default (off), so the
	// shipped read output is unchanged unless an operator turns the tier on.
	SealSentinelsEnabled *bool
}

var redactionPolicyPtr = func() *atomic.Pointer[redactionPolicy] {
	p := &atomic.Pointer[redactionPolicy]{}
	p.Store(defaultRedactionPolicy())
	return p
}()

var redactionPolicyMu sync.Mutex

func defaultRedactionPolicy() *redactionPolicy {
	return &redactionPolicy{
		secretsEnabled:             true,
		codeFileFPEnabled:          true,
		tokenizationEnabled:        false,
		genericPrefixes:            []string{"generic-"},
		genericRuleIDs:             defaultGenericRuleIDs(),
		tokenTTL:                   defaultTokenTTL,
		tokenMaxEntries:            defaultTokenMaxEntries,
		jsonKeyRedactionEnabled:    true,
		jsonSecretKeys:             defaultJSONSecretKeys(),
		jsonKeySuffixes:            defaultJSONKeySuffixes(),
		learnedSecretMemoryEnabled: true,
	}
}

// defaultGenericRuleIDs are the shipped rules whose detection is keyword- or
// entropy-anchored enough to be a false-positive magnet on source code (the
// generic KEY=value and "apiKey":"value" catch-alls). They are suppressed in
// code-file mode only; the high-precision vendor-prefix family is never here.
func defaultGenericRuleIDs() map[string]bool {
	ids := []string{
		"generic-api-key",
		"generic-api-token",
		"generic-secret",
		"generic-password",
		"private-key-pkcs8", // PEM has its own precise detector; the generic one is noisy
	}
	m := make(map[string]bool, len(ids))
	for _, id := range ids {
		m[id] = true
	}
	return m
}

// SetRedactionPolicy installs the effective read-path redaction snapshot. Call
// it once at startup. Safe to call repeatedly and from concurrent agents; the
// write is guarded so it composes with SetSensitiveFilePolicy. Passing the zero
// options yields the secure defaults.
func SetRedactionPolicy(opts RedactionPolicyOptions) {
	redactionPolicyMu.Lock()
	defer redactionPolicyMu.Unlock()
	base := defaultRedactionPolicy()
	if p := redactionPolicyPtr.Load(); p != nil {
		// Carry the generic-rule customizations forward across repeated sets so
		// a later call that only changes, say, tokenization does not reset them.
		base.genericPrefixes = append([]string{}, p.genericPrefixes...)
		base.genericRuleIDs = make(map[string]bool, len(p.genericRuleIDs))
		for k := range p.genericRuleIDs {
			base.genericRuleIDs[k] = true
		}
		// Same for the JSON key set: the built-in names are always present, and any
		// operator additions survive a later policy change.
		base.jsonSecretKeys = make(map[string]bool, len(p.jsonSecretKeys))
		for k := range p.jsonSecretKeys {
			base.jsonSecretKeys[k] = true
		}
		base.jsonKeySuffixes = append([]string{}, p.jsonKeySuffixes...)
	}
	if opts.SecretsEnabled != nil {
		base.secretsEnabled = *opts.SecretsEnabled
	}
	if opts.CodeFileFPEnabled != nil {
		base.codeFileFPEnabled = *opts.CodeFileFPEnabled
	}
	if opts.TokenizationEnabled != nil {
		base.tokenizationEnabled = *opts.TokenizationEnabled
	}
	if opts.JSONKeyRedactionEnabled != nil {
		base.jsonKeyRedactionEnabled = *opts.JSONKeyRedactionEnabled
	}
	if opts.LearnedSecretMemoryEnabled != nil {
		base.learnedSecretMemoryEnabled = *opts.LearnedSecretMemoryEnabled
	}
	if opts.SealSentinelsEnabled != nil {
		base.sealSentinels = *opts.SealSentinelsEnabled
	}
	for _, id := range opts.ExtraGenericRules {
		if id != "" {
			base.genericRuleIDs[id] = true
		}
	}
	for _, k := range opts.ExtraJSONSecretKeys {
		if k != "" {
			base.jsonSecretKeys[strings.ToLower(k)] = true
		}
	}
	if opts.TokenTTL > 0 {
		base.tokenTTL = opts.TokenTTL
	}
	if opts.TokenMaxEntries > 0 {
		base.tokenMaxEntries = opts.TokenMaxEntries
	}
	redactionPolicyPtr.Store(base)
	secretTokens.mu.Lock()
	secretTokens.ttl = base.tokenTTL
	secretTokens.max = base.tokenMaxEntries
	secretTokens.mu.Unlock()
}

// ResetRedactionPolicyForTest restores the built-in defaults and clears the
// token store. Intended for tests only.
func ResetRedactionPolicyForTest() {
	redactionPolicyMu.Lock()
	defer redactionPolicyMu.Unlock()
	redactionPolicyPtr.Store(defaultRedactionPolicy())
	secretTokens.Reset()
}

func currentRedactionPolicy() *redactionPolicy {
	if p := redactionPolicyPtr.Load(); p != nil {
		return p
	}
	return defaultRedactionPolicy()
}

// TokenizationEnabled reports whether the reversible token layer is active.
func TokenizationEnabled() bool { return currentRedactionPolicy().tokenizationEnabled }

// SecretsEnabled reports whether the read-path gitleaks pass is active.
func SecretsEnabled() bool { return currentRedactionPolicy().secretsEnabled }

func codeFileFPEnabled() bool { return currentRedactionPolicy().codeFileFPEnabled }

// JSONKeyRedactionEnabled reports whether the structured-JSON key-drop layer is
// active. The secure default is on; it is cheap and low-noise because it only
// fires on a whole-document JSON payload and only drops fields by key name.
func JSONKeyRedactionEnabled() bool {
	return currentRedactionPolicy().jsonKeyRedactionEnabled
}

// LearnedSecretMemoryEnabled reports whether the hashed learned-secret memory is
// active. The secure default is on: it only ever upgrades a detection toward
// scrubbing a value already judged sensitive, so it cannot introduce a false
// block and stores no plaintext.
func LearnedSecretMemoryEnabled() bool {
	return currentRedactionPolicy().learnedSecretMemoryEnabled
}

// SealSentinelsEnabled reports whether the egress-isolation read path is armed:
// when true a detected credential is sealed into a broker-resolvable token
// rather than a plain sentinel. The default is off, so the shipped read output
// is unchanged unless an operator turned the tier on at startup.
func SealSentinelsEnabled() bool {
	return currentRedactionPolicy().sealSentinels
}

// isSecretJSONKey reports whether a JSON object key names a field whose value
// should be dropped: an exact match against the (lower-cased) secret-key set or a
// match against one of the configured trailing suffixes. Empty keys are ignored.
func (p *redactionPolicy) isSecretJSONKey(key string) bool {
	if key == "" {
		return false
	}
	k := strings.ToLower(key)
	if p.jsonSecretKeys[k] {
		return true
	}
	for _, suffix := range p.jsonKeySuffixes {
		if suffix != "" && strings.HasSuffix(k, suffix) {
			return true
		}
	}
	return false
}

// isGenericRule reports whether a rule belongs to the low-precision family that
// code-file mode suppresses: an explicitly listed id or a configured prefix.
func (p *redactionPolicy) isGenericRule(ruleID string) bool {
	if ruleID == "" {
		return false
	}
	if p.genericRuleIDs[ruleID] {
		return true
	}
	for _, pre := range p.genericPrefixes {
		if pre != "" && strings.HasPrefix(ruleID, pre) {
			return true
		}
	}
	return false
}
