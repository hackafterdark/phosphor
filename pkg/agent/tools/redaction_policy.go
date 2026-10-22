package tools

import (
	"strings"
	"time"

	"github.com/hackafterdark/phosphor/pkg/config"
)

// The secret-protection read/redaction pipeline reads its runtime switches from
// an immutable snapshot built once per agent and carried on the tool instances,
// never from mutable process state on the hot path. That is the "env snapshot"
// discipline: a prompt-injected tool that mutates the environment or re-reads
// config mid-session cannot silently flip these guards off, because nothing
// consults live state per call. The snapshot is per-agent (serve mode hosts
// several workspaces concurrently, and a process-wide copy would let the
// last-built agent clobber every other workspace's policy), and it carries the
// sensitive-file policy, the custom rules path, and the token store alongside
// the read-path toggles so all of them freeze at the same boundary.
type redactionPolicy struct {
	// secretsEnabled gates the gitleaks pass over read-path tool output (view,
	// grep, bash, mcp, job_output). The secure default is on.
	secretsEnabled bool
	// codeFileFalsePositiveEnabled turns on the context-aware false-positive
	// suppression: when a scan is known to be reading source code, the
	// low-precision generic KEY=value / "apiKey":"value" rule family is skipped
	// while the high-precision vendor-prefix, PEM, JWT, auth-header and
	// connection-string checks still run. On by default.
	codeFileFalsePositiveEnabled bool
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
	// tokenTTL and tokenMaxEntries bound the per-policy token store.
	tokenTTL        time.Duration
	tokenMaxEntries int
	// tokens is the reversible-token store owned by this policy. Keeping it
	// per-policy means a token minted in one workspace cannot be resolved by
	// another workspace's tools: cross-policy restores fail closed and the
	// token stays an inert sentinel.
	tokens *tokenStore
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
	// sensitiveEnabled is the master switch for path-based whole-value
	// redaction of sensitive files. The secure default is on.
	sensitiveEnabled bool
	// sensitivePatterns is the effective sensitive-file pattern set: the
	// built-in default set merged with any operator additions (they can
	// extend, never subtract, so the .env family stays sensitive).
	sensitivePatterns []string
	// secretRulesPath is the workspace's custom gitleaks rules file. The
	// detector for a path is cached process-wide (see detectorFor).
	secretRulesPath string
}

// Policy is the immutable per-agent snapshot of the secret-protection switches.
// Build one with NewPolicy at agent construction and pass it to the tool
// constructors; helpers that receive a nil Policy fall back to the built-in
// secure defaults.
type Policy = redactionPolicy

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
	// SensitiveFilesEnabled gates path-based whole-value redaction. A nil field
	// means the secure default (on).
	SensitiveFilesEnabled *bool
	// ExtraSensitiveFilePatterns extend the built-in sensitive-file pattern set.
	ExtraSensitiveFilePatterns []string
	// SecretRulesPath points at the workspace's custom gitleaks rules file. An
	// empty value uses the built-in default path.
	SecretRulesPath string
}

func defaultRedactionPolicy() *redactionPolicy {
	return &redactionPolicy{
		secretsEnabled:               true,
		codeFileFalsePositiveEnabled: true,
		tokenizationEnabled:          false,
		genericPrefixes:              []string{"generic-"},
		genericRuleIDs:               defaultGenericRuleIDs(),
		tokenTTL:                     defaultTokenTTL,
		tokenMaxEntries:              defaultTokenMaxEntries,
		jsonKeyRedactionEnabled:      true,
		jsonSecretKeys:               defaultJSONSecretKeys(),
		jsonKeySuffixes:              defaultJSONKeySuffixes(),
		learnedSecretMemoryEnabled:   true,
		sensitiveEnabled:             true,
		sensitivePatterns:            config.DefaultSensitiveFilePatterns,
		secretRulesPath:              defaultSecretRulesPath(),
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

// NewPolicy builds the effective redaction snapshot for one agent. The result
// is immutable: it must be constructed once at agent construction and carried
// on the tool instances, so no later agent build can alter this agent's
// guards. Passing the zero options yields the secure defaults.
func NewPolicy(opts RedactionPolicyOptions) *Policy {
	base := defaultRedactionPolicy()
	if opts.SecretsEnabled != nil {
		base.secretsEnabled = *opts.SecretsEnabled
	}
	if opts.CodeFileFPEnabled != nil {
		base.codeFileFalsePositiveEnabled = *opts.CodeFileFPEnabled
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
	if opts.SensitiveFilesEnabled != nil {
		base.sensitiveEnabled = *opts.SensitiveFilesEnabled
	}
	if len(opts.ExtraSensitiveFilePatterns) > 0 {
		base.sensitivePatterns = mergeSensitivePatterns(opts.ExtraSensitiveFilePatterns)
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
	if p := strings.TrimSpace(opts.SecretRulesPath); p != "" {
		base.secretRulesPath = p
	}
	base.tokens = newTokenStore(base.tokenTTL, base.tokenMaxEntries)
	return base
}

// defaultPolicy returns the built-in secure defaults for callers that have no
// agent-bound policy (tests, tools constructed outside a coordinator). It is
// fresh per call so its token store is never shared across agents.
func defaultPolicy() *Policy {
	return defaultRedactionPolicy()
}

// orDefault resolves a nil policy to the built-in secure defaults.
func (p *Policy) orDefault() *Policy {
	if p == nil {
		return defaultPolicy()
	}
	return p
}

// TokenizationEnabled reports whether the reversible token layer is active.
func (p *Policy) TokenizationEnabled() bool { return p.orDefault().tokenizationEnabled }

// SecretsEnabled reports whether the read-path gitleaks pass is active.
func (p *Policy) SecretsEnabled() bool { return p.orDefault().secretsEnabled }

func (p *Policy) codeFileFPEnabled() bool { return p.orDefault().codeFileFalsePositiveEnabled }

// JSONKeyRedactionEnabled reports whether the structured-JSON key-drop layer is
// active. The secure default is on; it is cheap and low-noise because it only
// fires on a whole-document JSON payload and only drops fields by key name.
func (p *Policy) JSONKeyRedactionEnabled() bool {
	return p.orDefault().jsonKeyRedactionEnabled
}

// LearnedSecretMemoryEnabled reports whether the hashed learned-secret memory is
// active. The secure default is on: it only ever upgrades a detection toward
// scrubbing a value already judged sensitive, so it cannot introduce a false
// block and stores no plaintext.
func (p *Policy) LearnedSecretMemoryEnabled() bool {
	return p.orDefault().learnedSecretMemoryEnabled
}

// SealSentinelsEnabled reports whether the egress-isolation read path is armed:
// when true a detected credential is sealed into a broker-resolvable token
// rather than a plain sentinel. The default is off, so the shipped read output
// is unchanged unless an operator turned the tier on at startup.
func (p *Policy) SealSentinelsEnabled() bool {
	return p.orDefault().sealSentinels
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
