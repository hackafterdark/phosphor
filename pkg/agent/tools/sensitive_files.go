package tools

import (
	"log/slog"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"

	"github.com/hackafterdark/phosphor/pkg/config"
)

// Path-based whole-value redaction (mechanism "A" in the secret-protection
// plan). Where the gitleaks pass (mechanism "B") only scrubs spans that match a
// detector, this layer treats an entire *sensitive file* as secret by shape:
// when a read path is recognised as sensitive, the assignment values are dropped
// unconditionally while the keys are kept, so the model can still tell that
// DATABASE_URL exists without ever holding its value. Because we decided the
// file is sensitive up front, the false-positive cost is bounded and recall over
// assignment values is total, unlike a generic/entropy detector that silently
// misses opaque custom keys.
//
// It never blocks. By default it emits the same non-reusable sentinel as the
// gitleaks read path. When reversible tokenization is enabled (see tokenstore.go
// and the security.tokenize_secrets option) dotenv values become <secret:kind:id>
// tokens that the agent can round-trip: editing or copying a .env restores the
// original value only on a write to a trusted sensitive target.

// valueSentinel replaces every redacted assignment value. It is deliberately not
// a syntactically valid credential, so a masked line echoed back through an edit
// can never resurrect the secret (the write-back-corruption class of bug).
const valueSentinel = "<redacted:sensitive-file:value>"

// opaqueSentinel stands in for files whose whole body is the secret (private
// keys, certificates): there is no key/value shape to preserve.
const opaqueSentinel = "<redacted:sensitive-file:private-key>"

// sensitiveFilePolicy is the process-wide configuration for this layer. It is set
// once by the coordinator from the loaded config and read on every sensitive
// file read, so the hot path never touches the config store.
type sensitiveFilePolicy struct {
	enabled  bool
	patterns []string
}

var sensitivePolicy = func() *atomic.Pointer[sensitiveFilePolicy] {
	p := &atomic.Pointer[sensitiveFilePolicy]{}
	p.Store(&sensitiveFilePolicy{enabled: true, patterns: config.DefaultSensitiveFilePatterns})
	return p
}()

// SetSensitiveFilePolicy installs the effective sensitive-file policy. enabled is
// the master switch; extraPatterns are merged on top of the built-in default set
// (they can extend, never subtract, so the .env family stays sensitive). Safe to
// call repeatedly and from concurrent agents.
func SetSensitiveFilePolicy(enabled bool, extraPatterns []string) {
	patterns := config.DefaultSensitiveFilePatterns
	if len(extraPatterns) > 0 {
		merged := make([]string, 0, len(patterns)+len(extraPatterns))
		seen := make(map[string]bool, len(patterns)+len(extraPatterns))
		for _, p := range append(append([]string{}, patterns...), extraPatterns...) {
			if seen[p] {
				continue
			}
			seen[p] = true
			merged = append(merged, p)
		}
		patterns = merged
	}
	sensitivePolicy.Store(&sensitiveFilePolicy{enabled: enabled, patterns: patterns})
}

// sensitiveTier classifies a file for the redaction decision.
type sensitiveTier int

const (
	tierNone sensitiveTier = iota
	// tierSensitive: whole-value redaction (A) + detection (B) + registry.
	tierSensitive
	// tierTemplate: committed shape files (.env.example) — exempt from the blunt
	// whole-value rule (A) only, detection (B) still runs.
	tierTemplate
	// tierMixed: files that usually hold benign setup but can carry tokens
	// (.envrc) — whole-value redaction with a known-harmless key allowlist.
	tierMixed
)

// sensitiveShape selects the structural parser for a sensitive file.
type sensitiveShape int

const (
	shapeNone sensitiveShape = iota
	shapeDotenv
	shapeJSON
	shapeOpaque
)

// templateBasenames are the committed "shape" files that are ok to read in full
// (they define the file format with placeholders). They are exempt from (A) but
// NEVER from (B): a real key dropped into a .env.example is still scrubbed by the
// gitleaks pass. Matched on the exact base name, case-insensitive.
var templateBasenames = map[string]bool{
	".env.example":   true,
	".env.examples":  true,
	".env.sample":    true,
	".env.samples":   true,
	".env.template":  true,
	".env.templates": true,
	".env.dist":      true,
	".env.tpl":       true,
	".env.defaults":  true,
}

// mixedBasenames are files that are usually harmless setup yet can stash a
// token. They get whole-value redaction with the harmless-key allowlist so
// non-secret config the agent legitimately needs stays visible.
var mixedBasenames = map[string]bool{
	".envrc": true,
}

// harmlessEnvKeys are configuration keys that are, by convention, not secrets.
// Only consulted for the MIXED tier; on a SENSITIVE file every value is redacted.
var harmlessEnvKeys = map[string]bool{
	"PATH": true, "SHELL": true, "EDITOR": true, "VISUAL": true, "PAGER": true,
	"TERM": true, "LANG": true, "LC_ALL": true, "TZ": true, "HOME": true,
	"PWD": true, "TMPDIR": true, "TMP": true, "DEBUG": true, "LOG_LEVEL": true,
	"LOGLEVEL": true, "VERBOSE": true, "NODE_ENV": true, "RAILS_ENV": true,
	"DJANGO_SETTINGS_MODULE": true, "FLASK_ENV": true, "GO_ENV": true,
	"GOFLAGS": true, "GOROOT": true, "GOPATH": true, "PYTHONDONTWRITEBYTECODE": true,
	"PYTHONUNBUFFERED": true, "RUBYOPT": true, "RUBYLIB": true, "RUBYGEMPATH": true,
	"NPM_CONFIG_PREFIX": true, "NPM_CONFIG_YARN": true, "YARN_CACHE_DIR": true,
	"SPARKLOCAL_TAGS": true, "COMSPEC": true, "DISPLAY": true, "NO_COLOR": true,
	"FORCE_COLOR": true, "CI": true, "NODE_OPTIONS": true,
}

var (
	// dotenvLineRe matches a dotenv assignment line, capturing leading indent +
	// optional "export", the key, the "=[ \t]*" run, and the value.
	dotenvLineRe = regexp.MustCompile(`(?m)^([ \t]*(?:export[ \t]+)?)([A-Za-z_][A-Za-z0-9_]*)([ \t]*=[ \t]*)(.*)$`)
	// jsonValueRe matches a pretty-printed JSON "key": "value" pair with a
	// string value (numbers, booleans, and structure are left intact so the model
	// still sees which keys and non-secret fields the credential file has).
	jsonValueRe = regexp.MustCompile(`("(?:[^"\\\n]|\\.)*"\s*:\s*)"((?:[^"\\\n]|\\.)*)"`)
)

// classifySensitiveFile decides the tier and shape for a file path, consult the
// effective policy pattern set for the SENSITIVE tier.
func classifySensitiveFile(path string) (sensitiveTier, sensitiveShape) {
	if path == "" {
		return tierNone, shapeNone
	}
	full := strings.ToLower(filepath.ToSlash(filepath.Clean(filepath.FromSlash(path))))
	base := full
	if i := strings.LastIndexByte(full, '/'); i >= 0 {
		base = full[i+1:]
	}
	if templateBasenames[base] {
		return tierTemplate, shapeNone
	}
	if mixedBasenames[base] {
		return tierMixed, shapeDotenv
	}
	pol := sensitivePolicy.Load()
	if matchesSensitiveAny(pol.patterns, base, full) {
		return tierSensitive, inferShape(base)
	}
	return tierNone, shapeNone
}

// inferShape maps a sensitive base name onto the structural parser to run.
func inferShape(base string) sensitiveShape {
	switch {
	case strings.HasSuffix(base, ".pem"), strings.HasSuffix(base, ".p12"), strings.HasSuffix(base, ".pfx"),
		base == "id_rsa", base == "id_dsa", base == "id_ecdsa", base == "id_ed25519":
		return shapeOpaque
	case strings.HasSuffix(base, ".json") &&
		(strings.Contains(base, "credential") || strings.Contains(base, "service-account")),
		strings.HasPrefix(base, "secrets."):
		return shapeJSON
	default:
		return shapeDotenv
	}
}

// matchesSensitiveAny reports whether any glob matches the base name (globs with
// no slash) or the full slash path (globs that contain a slash).
func matchesSensitiveAny(patterns []string, base, full string) bool {
	for _, p := range patterns {
		lp := strings.ToLower(p)
		target := base
		if strings.Contains(lp, "/") {
			target = full
		}
		if ok, _ := filepath.Match(lp, target); ok {
			return true
		}
	}
	return false
}

// redactSensitiveContentByPath applies the whole-value redaction for a file read,
// keyed off its path. Content is returned unchanged when the file is not
// sensitive or the feature is off, so it composes safely with the gitleaks pass.
func redactSensitiveContentByPath(content, path string) string {
	if content == "" {
		return content
	}
	if !sensitivePolicy.Load().enabled {
		return content
	}
	tier, shape := classifySensitiveFile(path)
	return applySensitiveShape(content, tier, shape)
}

// redactReadContent is the single read-path chokepoint: structural whole-value
// redaction (A) when the path is sensitive, then the content detector (B) so
// opaque keys the structural parser did not cover, and any real secret living in
// a template file, are still scrubbed. The content pass honours the read-path
// secrets toggle (a startup snapshot).
func redactReadContent(text, filePath, source string) string {
	text = redactSensitiveContentByPath(text, filePath)
	return redactSecretsForTool(text, filePath, source)
}

// redactSensitiveOutputForCommand handles the bash asymmetry: `cat .env` gives us
// only path-less stdout, but the command argv does reveal that a sensitive path
// was referenced. When it is, the same whole-value redaction is applied to the
// command output. When detection is inconclusive we do nothing here and fall back
// to the gitleaks pass that runs over all bash output.
func redactSensitiveOutputForCommand(output, command string) string {
	if output == "" || !sensitivePolicy.Load().enabled {
		return output
	}
	shape, ok := shapeForCommandReference(command)
	if !ok {
		return output
	}
	return applySensitiveShape(output, tierSensitive, shape)
}

// applySensitiveShape runs the structural parser selected by shape. The TEMPLATE
// and NONE tiers (and shapeNone) are a no-op here: TEMPLATE is exempt from (A),
// and (B) still runs separately in redactReadContent.
func applySensitiveShape(content string, tier sensitiveTier, shape sensitiveShape) string {
	if tier != tierSensitive && tier != tierMixed {
		return content
	}
	switch shape {
	case shapeOpaque:
		return opaqueSentinel + "\n"
	case shapeJSON:
		return jsonValueRe.ReplaceAllStringFunc(content, redactJSONValue)
	case shapeDotenv:
		return redactDotenvValues(content, tier == tierMixed)
	default:
		return content
	}
}

// redactDotenvValues rewrites every KEY=value line to KEY=<placeholder>, keeping
// the indent, the optional export, and the key. When keepHarmless is set (MIXED
// tier) conventionally-harmless keys are left intact. When reversible
// tokenization is enabled the placeholder is a <secret:kind:id> token (so the
// value can be restored on a trusted write) instead of the static sentinel; with
// tokenization off the shipped static sentinel is used unchanged.
func redactDotenvValues(content string, keepHarmless bool) string {
	tokenize := currentRedactionPolicy().tokenizationEnabled
	return dotenvLineRe.ReplaceAllStringFunc(content, func(line string) string {
		i := strings.IndexByte(line, '=')
		if i < 0 {
			return line
		}
		lhs := line[:i]
		value := strings.TrimLeft(line[i+1:], " \t")
		if value == "" || strings.Contains(value, valueSentinel) || tokenPatternRe.MatchString(value) {
			return line
		}
		if keepHarmless && harmlessEnvKeys[assignmentKey(lhs)] {
			return line
		}
		return lhs + "=" + dotenvPlaceholder(value, tokenize)
	})
}

// dotenvPlaceholder returns the token (tokenization on) or the static sentinel
// (tokenization off) that replaces a dotenv value on read.
func dotenvPlaceholder(value string, tokenize bool) string {
	if tokenize {
		if tok := secretTokens.Issue("dotenv", value); tok != "" {
			return tok
		}
	}
	return valueSentinel
}

// assignmentKey pulls the variable name out of the left-hand side of a dotenv
// assignment, dropping leading indentation and an optional "export " prefix.
func assignmentKey(lhs string) string {
	s := strings.TrimLeft(lhs, " \t")
	if len(s) >= 6 && strings.EqualFold(s[:6], "export") {
		s = strings.TrimLeft(s[6:], " \t")
	}
	return s
}

// redactJSONValue keeps "key": visible and replaces the string value with the
// sentinel. Returns the match unchanged when it cannot be split on a key/value
// colon outside the key token, so odd inputs are never corrupted.
func redactJSONValue(match string) string {
	// The match opens with the key token; walk to its closing quote, then to the
	// separating colon, so a colon inside the key or value cannot fool us.
	if match == "" || match[0] != '"' {
		return match
	}
	i := 1
	for i < len(match) {
		if match[i] == '\\' {
			i += 2
			continue
		}
		if match[i] == '"' {
			break
		}
		i++
	}
	i++ // step past the closing quote of the key
	for i < len(match) && (match[i] == ' ' || match[i] == '\t' || match[i] == '\n' || match[i] == '\r') {
		i++
	}
	if i >= len(match) || match[i] != ':' {
		return match
	}
	return match[:i+1] + " \"" + valueSentinel + "\""
}

// redactSensitiveLine is the single-line variant of the whole-value redaction,
// used where a tool has one line out of a file's context (a grep hit). It runs
// the same shape parsers on the lone line.
func redactSensitiveLine(line, path string) string {
	if line == "" || !sensitivePolicy.Load().enabled {
		return line
	}
	tier, shape := classifySensitiveFile(path)
	if tier != tierSensitive && tier != tierMixed {
		return line
	}
	switch shape {
	case shapeOpaque:
		return opaqueSentinel
	case shapeJSON:
		return jsonValueRe.ReplaceAllStringFunc(line, redactJSONValue)
	case shapeDotenv:
		return redactDotenvValues(line, tier == tierMixed)
	default:
		return line
	}
}

// shapeForCommandReference reports the sensitive shape implied by a command that
// references a sensitive path (argv-detect). It is a heuristic over argv tokens;
// a miss simply falls through to the gitleaks pass, so it errs toward not
// redacting only when no referenced token looks like a sensitive file.
func shapeForCommandReference(command string) (sensitiveShape, bool) {
	pol := sensitivePolicy.Load()
	for _, tok := range strings.Fields(command) {
		tok = strings.Trim(tok, "\"'")
		if tok == "" || strings.HasPrefix(tok, "-") {
			continue
		}
		norm := strings.ToLower(filepath.ToSlash(filepath.Base(filepath.FromSlash(tok))))
		if norm == "" || norm == "." {
			continue
		}
		full := strings.ToLower(filepath.ToSlash(filepath.FromSlash(tok)))
		if matchesSensitiveAny(pol.patterns, norm, full) {
			return inferShape(norm), true
		}
	}
	return shapeNone, false
}

// Trusted-write restore (secret-protection plan, Phase 6).
//
// A credential the agent saw redacted on read is written back as an inert
// <secret:kind:id> token. For a legitimate round-trip (the agent edits or copies
// an .env, moving a value it never actually held) we resolve those tokens back to
// their real values, but only when the destination is a file we already treat as
// sensitive (an .env family member, a credential JSON, a private key). For any
// other destination the token is left in place: it is not a valid credential, the
// model never saw the plaintext, so a token that leaks into a source file is
// inert. Restoring is therefore always a no-op unless tokenization is on and the
// target is a trusted sensitive file.

// trustedWriteTarget reports whether a write to path is a destination allowed to
// receive restored plaintext: the SENSITIVE or MIXED sensitive-file tiers, i.e.
// the same set the read path redacts. Template and ordinary files are not.
func trustedWriteTarget(path string) bool {
	if !currentRedactionPolicy().tokenizationEnabled {
		return false
	}
	tier, _ := classifySensitiveFile(path)
	return tier == tierSensitive || tier == tierMixed
}

// restoreSecretTokensForWrite resolves reversible tokens back to their values on
// the way to disk, but only for a trusted sensitive target and only when
// tokenization is enabled. It is a no-op otherwise, so shipped writes are
// unchanged. Called at every content-write site in the edit/write/append tools.
func restoreSecretTokensForWrite(content, targetPath string) string {
	if content == "" || !trustedWriteTarget(targetPath) {
		return content
	}
	out, n := secretTokens.Restore(content)
	if n > 0 {
		slog.Debug("Restored secret tokens on trusted write",
			"path", filepath.Base(targetPath),
			"count", n,
		)
	}
	return out
}
