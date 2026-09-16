package tools

import (
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"sync"

	"github.com/hackafterdark/phosphor/pkg/secrets"
	"github.com/zricethezav/gitleaks/v8/report"
)

// secretDetector is the process-wide gitleaks detector. It is built once and
// reused for every write/edit: constructing it parses the embedded ruleset and
// compiles 700+ RE2 regexes plus an Aho-Corasick prefilter, which is far too
// expensive to redo per call. Detector.DetectString only reads immutable
// post-construction state, so concurrent calls from parallel tools are safe.
var secretDetector = sync.OnceValues(newSecretDetector)

// contentScanExcluded reports whether filePath is test/doc material that
// should skip content scanning. gitleaks' inline "// gitleaks:allow" opt-out
// is honored independently by the detector itself.
func contentScanExcluded(filePath string) bool {
	if filePath == "" {
		return false
	}
	p := strings.ToLower(strings.ReplaceAll(filePath, "\\", "/"))
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	// The .env family is never allow-listed, even for its committed template
	// printings (.env.example and friends). Those are exempt from the blunt
	// whole-value rule only, never from content detection: that is exactly what
	// makes "example files are the exception" safe rather than an evasion hole,
	// since a real key dropped into a template is still scrubbed here.
	if base := p[strings.LastIndexByte(p, '/')+1:]; strings.HasPrefix(base, ".env") {
		return false
	}
	for _, dir := range []string{"/testdata/", "/fixtures/", "/__snapshots__/", "/vendor/", "/node_modules/"} {
		if strings.Contains(p, dir) {
			return true
		}
	}
	for _, suffix := range []string{"_test.go", ".example", ".sample", ".template", ".dist", ".md"} {
		if strings.HasSuffix(p, suffix) {
			return true
		}
	}
	return false
}

// checkSecrets scans content that is about to be written to disk for leaked
// credentials, backed by gitleaks' ruleset. It is a hard gate: any finding
// blocks the write and returns an error to the model. The matched secret is
// never included in the message, only its rule identity and location, so a
// blocked edit cannot echo the credential back into the conversation.
//
// checkSecrets is retained for call sites without a target path; it delegates
// to checkSecretsAt with an empty path (full scan, no path allowlisting).
func checkSecrets(content string) error {
	return checkSecretsAt(content, "")
}

// checkSecretsAt is checkSecrets with path-based false-positive suppression:
// test and doc material is skipped so fixture credentials do not block
// legitimate edits. Pass filePath as the write/edit target when known.
func checkSecretsAt(content, filePath string) error {
	if content == "" || contentScanExcluded(filePath) {
		return nil
	}

	d, err := secretDetector()
	if err != nil {
		// The detector parses a tested embedded TOML and only errors if that
		// parse fails, which is effectively unreachable. Fail open to the legacy
		// three-rule scanner rather than block every edit over an init failure.
		return checkSecretsLegacy(content)
	}

	// A reversible token we issued earlier is inert text, not a credential, so it
	// must never trip the write gate (that would make a legitimate round-trip of
	// an .env value un-writable). Neutralise our own tokens before detecting; a
	// genuine credential the model typed verbatim still has no token and is still
	// caught.
	scan := stripTokens(content)
	findings := d.DetectString(scan)
	if len(findings) == 0 {
		return nil
	}

	// Report the highest-signal finding only; never the secret itself.
	f := findings[0]
	preview := strings.TrimSpace(f.Description)
	if preview == "" {
		preview = f.RuleID
	}
	msg := fmt.Sprintf("Security violation: potential secret detected (%s: %s", f.RuleID, preview)
	if f.StartLine > 0 {
		msg += fmt.Sprintf(" near line %d", f.StartLine)
	}
	if len(findings) > 1 {
		msg += fmt.Sprintf(", %d findings total", len(findings))
	}
	msg += "). Your edit was rejected to keep the credential out of the transcript."
	return fmt.Errorf("%s", msg)
}

// Legacy detectors kept solely as the fail-open path if gitleaks ever fails to
// initialize. Retained from the pre-gitleaks checkSecrets.
var (
	awsSecretRegex  = regexp.MustCompile(`(?i)aws_(?:secret_)?access_key\s*[:=]\s*['"][A-Za-z0-9/\+=]{40}['"]`)
	privateKeyRegex = regexp.MustCompile(`-----BEGIN [A-Z ]+ PRIVATE KEY-----`)
	genericApiKeyRe = regexp.MustCompile(`(?i)api_key\s*[:=]\s*['"][A-Za-z0-9_\-]{20,}['"]`)
)

func checkSecretsLegacy(content string) error {
	if awsSecretRegex.MatchString(content) {
		return fmt.Errorf("Security violation: potential AWS secret access key leak detected")
	}
	if privateKeyRegex.MatchString(content) {
		return fmt.Errorf("Security violation: potential private key leak detected")
	}
	if genericApiKeyRe.MatchString(content) {
		return fmt.Errorf("Security violation: potential API key leak detected")
	}
	return nil
}

// redactSecrets replaces detected secrets in read-path tool output with a
// non-reusable sentinel (or, when tokenization is enabled, a reversible
// <secret:kind:id> token) so credentials never enter the conversation, while the
// model still sees that a secret of a given type is present. Unlike the write
// path it never blocks: a false positive only alters displayed text, and a
// detector failure fails open rather than blanking real output.
//
// It is the unconditional entrypoint used at the provider-boundary last-resort
// mask (see RedactSecretsForWire), where redaction must happen regardless of the
// read-path toggle. Tool read surfaces go through redactSecretsForTool, which
// honours that toggle.
func redactSecrets(text string) string {
	return redactSecretsAt(text, "", "tool")
}

// redactSecretsForTool is the read-path entrypoint the view/grep/bash/mcp/job
// surfaces call. It is gated by the read-path secrets toggle (a snapshot frozen
// at startup) so an operator can turn read-side scanning off; it then defers to
// redactSecretsAt, which derives the scan mode from the read context.
func redactSecretsForTool(text, filePath, source string) string {
	if text == "" {
		return text
	}
	// Exact known provider/OAuth/MCP credentials are erased regardless of the
	// gitleaks read-path toggle: the registry only removes values we explicitly
	// loaded this session, so it is high-precision and cheap enough to always run.
	// It also fires on the test/doc paths the gitleaks pass allow-lists, since a
	// real provider key should never reach context even inside a fixture.
	text = secrets.Scrub(text)
	if !SecretsEnabled() {
		return text
	}
	return redactSecretsAt(text, filePath, source)
}

// redactSecretsAt is redactSecrets with path-based false-positive suppression
// for known test/doc material and context-aware mode selection: a read of a
// source-code file runs ScanCodeFile (generic family suppressed), anything else
// runs ScanFull. pass the source file path when it is known.
func redactSecretsAt(text, filePath, source string) string {
	if text == "" || contentScanExcluded(filePath) {
		return text
	}

	d, err := secretDetector()
	if err != nil {
		return text
	}

	mode := scanModeForPath(filePath, source)
	findings := filterFindings(d.DetectString(text), mode)
	if len(findings) == 0 {
		return text
	}

	tokenize := currentRedactionPolicy().tokenizationEnabled
	learned := LearnedSecretMemoryEnabled()
	pairs := make([]string, 0, len(findings)*2)
	seen := make(map[string]struct{}, len(findings))
	rules := make([]string, 0, len(findings))
	for _, f := range findings {
		needle := f.Match
		if needle == "" {
			needle = f.Secret
		}
		if len(needle) < 4 {
			continue
		}
		if _, ok := seen[needle]; ok {
			continue
		}
		seen[needle] = struct{}{}
		// Remember this value as judged-sensitive (keyed hash only, no plaintext)
		// so a later detection of the same bytes is trusted even where a
		// per-context false-positive rule would otherwise drop it.
		if learned {
			secrets.Learn(needle)
		}
		pairs = append(pairs, needle, tokenOrSentinel(f.RuleID, needle, tokenize))
		rules = append(rules, f.RuleID)
	}
	if len(pairs) == 0 {
		return text
	}

	// Log a fingerprint only: rule identities and a count, never the value.
	// Debug level: this fires on every tool output, so it must not spam Info.
	slog.Debug("Redacted secrets in tool output",
		"source", source,
		"mode", modeString(mode),
		"count", len(seen),
		"rules", strings.Join(uniqueStrings(rules), ","),
	)
	return strings.NewReplacer(pairs...).Replace(text)
}

// filterFindings drops the low-precision generic KEY=value / "apiKey":"value"
// rule family when the scan is known to be reading source code, keeping every
// high-precision vendor-prefix, PEM, JWT, auth-header and connection-string hit.
// This is the code_file FP mode of the redaction plan; it only ever removes
// detections, so it cannot introduce a false block, and it never runs on the
// write path (which always uses ScanFull).
func filterFindings(findings []report.Finding, mode ScanMode) []report.Finding {
	if mode != ScanCodeFile || !codeFileFPEnabled() {
		return findings
	}
	pol := currentRedactionPolicy()
	learned := LearnedSecretMemoryEnabled()
	out := findings[:0]
	for _, f := range findings {
		if pol.isGenericRule(f.RuleID) {
			// The generic family is FP noise on source code and is normally
			// dropped here. But if this exact value was already judged a real
			// secret elsewhere this session (its keyed hash is in the learned
			// set), it is not a false positive any more, so keep the finding and
			// let the scrub pass erase it.
			if !learned || !secrets.Known(findingValue(f)) {
				continue
			}
		}
		out = append(out, f)
	}
	return out
}

// findingValue returns the secret bytes a finding points at, preferring the
// full match and falling back to the extracted secret, mirroring how the scrub
// pass picks its needle.
func findingValue(f report.Finding) string {
	if f.Match != "" {
		return f.Match
	}
	return f.Secret
}

// tokenOrSentinel returns the replacement text for a redacted span: a reversible
// token when tokenization is on (and the value was non-empty), otherwise the
// static non-reusable sentinel. The static path keeps shipped output identical to
// before tokenization existed.
func tokenOrSentinel(ruleID, value string, tokenize bool) string {
	if tokenize {
		if tok := secretTokens.Issue(ruleID, value); tok != "" {
			return tok
		}
	}
	return redactionSentinel(ruleID)
}

// stripTokens removes the reversible tokens we issued from content so that our
// own inert markers are never mistaken for credentials by the detector. A
// credential the model typed verbatim has no token form and is still detected.
func stripTokens(content string) string {
	if content == "" || !tokenPatternRe.MatchString(content) {
		return content
	}
	return tokenPatternRe.ReplaceAllString(content, "«tok»")
}

func modeString(mode ScanMode) string {
	if mode == ScanCodeFile {
		return "code_file"
	}
	return "file_read"
}

// redactionSentinel builds a non-reusable marker naming the detector rule that
// fired, so the model keeps semantic context (which credential is here) without
// the credential itself.
func redactionSentinel(ruleID string) string {
	if ruleID == "" {
		ruleID = "unknown"
	}
	return "<redacted:gitleaks:" + ruleID + ">"
}

// uniqueStrings returns the input with duplicates removed, preserving order.
func uniqueStrings(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

// RedactSecretsForWire is the exported entrypoint other packages (notably the
// agent's outgoing-message last-resort pass) use to redact detected secrets
// from arbitrary text destined for the provider. It first erases any exact
// known-value credential we loaded (the registry, which is a hard, high-precision
// boundary independent of the gitleaks read-path toggle) and then runs the
// gitleaks pattern pass over what remains.
func RedactSecretsForWire(text string) string {
	text = secrets.Scrub(text)
	return redactSecrets(text)
}
