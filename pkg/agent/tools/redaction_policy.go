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
}

// RedactionPolicyOptions is the operator-facing knob set for the read-path
// redaction snapshot. A nil field means "use the secure default".
type RedactionPolicyOptions struct {
	SecretsEnabled      *bool
	CodeFileFPEnabled   *bool
	TokenizationEnabled *bool
	ExtraGenericRules   []string
	TokenTTL            time.Duration
	TokenMaxEntries     int
}

var redactionPolicyPtr = func() *atomic.Pointer[redactionPolicy] {
	p := &atomic.Pointer[redactionPolicy]{}
	p.Store(defaultRedactionPolicy())
	return p
}()

var redactionPolicyMu sync.Mutex

func defaultRedactionPolicy() *redactionPolicy {
	return &redactionPolicy{
		secretsEnabled:      true,
		codeFileFPEnabled:   true,
		tokenizationEnabled: false,
		genericPrefixes:     []string{"generic-"},
		genericRuleIDs:      defaultGenericRuleIDs(),
		tokenTTL:            defaultTokenTTL,
		tokenMaxEntries:     defaultTokenMaxEntries,
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
	for _, id := range opts.ExtraGenericRules {
		if id != "" {
			base.genericRuleIDs[id] = true
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
