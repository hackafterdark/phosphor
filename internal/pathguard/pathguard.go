// Package pathguard centralises the workspace path-confinement policy used by
// the bash tool. It exposes two complementary layers that share one source of
// truth:
//
//  1. A static, pre-expansion pass over the raw command string
//     (CorrectCommandPaths / ValidateCommandPaths). This runs before the shell
//     interpreter sees the command and relocates leading-slash arguments that
//     were meant to be workspace-relative, and rejects obvious escapes.
//  2. A dynamic, post-expansion pass over the fully-resolved argv
//     (Confinement.Blocked). This runs at exec time once the interpreter has
//     substituted $VARS, $(…), backticks, globs and brace expansion, and
//     rejects any argument that addresses a location outside the trusted
//     roots. It closes the bypass where an escape is only materialised at
//     execution time and therefore invisible to the static pass.
//
// The policy lives here, rather than in the bash tool or the shell package, so
// both consumers enforce the exact same classification and cannot drift.
package pathguard

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/hackafterdark/phosphor/internal/filepathext"
)

// driveRegexp matches a token that begins with a Windows drive specifier
// (e.g. "C:/", "D:\"). Used to decide whether a token should be treated as an
// absolute path for both correction and validation.
var driveRegexp = regexp.MustCompile(`^[A-Za-z]:`)

// dotDotSegmentRegexp matches a standalone ".." path segment, which is the
// only form of relative traversal that can escape the workspace. A literal
// ".." must be bounded by a separator or a string edge so that names such as
// "foo..bar" or "..cache" are not mistaken for traversal.
var dotDotSegmentRegexp = regexp.MustCompile(`(^|[/\\])\.\.($|[/\\])`)

// remoteURLRegexp matches tokens that are unambiguously remote URLs (a scheme
// followed by "://"). Such tokens are never local filesystem paths and are
// therefore skipped by both path correction and validation. The scheme list is
// an explicit allowlist: notably "file:" is excluded because a file URL can
// still carry ".." traversal that must be validated.
var remoteURLRegexp = regexp.MustCompile(`(?i)^(https?|ws|wss|ftp|ftps|git|gopher|ssh|sftp|svn|ipfs|ipns|blob|data|mailto|magnet|oci|docker|registry|npm|yarn|cargo|gem|pip|pypi|atom|feed)://`)

// isRemoteURL reports whether a token is a safe remote URL. It deliberately
// returns false for any token that contains a ".." path segment so that a
// scheme-prefixed value such as "file://../../etc/passwd" or
// "http://x/../../etc/passwd" is still treated as a path and validated.
func isRemoteURL(token string) bool {
	if dotDotSegmentRegexp.MatchString(token) {
		return false
	}
	return remoteURLRegexp.MatchString(token)
}

// isAbsoluteLike reports whether a token is written as an absolute path: a
// leading slash, a Windows-style drive specifier, or a backslash prefix. Such
// tokens are the ones CorrectCommandPaths attempts to relocate inside the
// workspace (they are frequently workspace-relative paths the model wrote with
// an erroneous leading separator).
func isAbsoluteLike(token string) bool {
	return filepathext.SmartIsAbs(token) || driveRegexp.MatchString(token)
}

// isEscapablePathToken reports whether a token is capable of referring to a
// location outside the workspace. Only such tokens are worth resolving against
// the workspace bounds; ordinary words, flags, package names, Go import paths
// (github.com/x/y) and remote URLs are ignored so they neither produce false
// positives nor get rewritten. A token is escapable when it uses a leading
// "~" home expansion or, carrying a real named path segment, is absolute-like,
// UNC-prefixed, or contains a ".." traversal segment. Degenerate values such
// as "\", "/", ".", ".." or a line-continuation backslash are not escapable.
func isEscapablePathToken(token string) bool {
	if token == "" {
		return false
	}
	s := filepath.ToSlash(token)
	// Home expansion always targets the user profile, which is outside the
	// workspace regardless of the sub-path that follows.
	if s == "~" || strings.HasPrefix(s, "~/") {
		return true
	}
	// Must contain a separator to be able to address anything else.
	if !strings.ContainsAny(token, "/\\") {
		return false
	}
	// Strip separators, dots and the home shortcut; if nothing meaningful
	// remains the token is just punctuation and cannot name an outside path.
	if strings.Trim(token, "./\\~") == "" {
		return false
	}
	if dotDotSegmentRegexp.MatchString(token) {
		return true
	}
	if isAbsoluteLike(token) {
		return true
	}
	// UNC shares ("\\host\share" or "//host/share") can reach network or
	// device paths and never live inside a normal workspace tree.
	return strings.HasPrefix(s, "//")
}

// commandToken is a whitespace/shell-operator-delimited token together with
// its byte span in the original command and the quote character (if any) that
// wrapped it.
type commandToken struct {
	text  string
	start int
	end   int
	quote byte
}

// isShellDelimiter reports whether c separates tokens when unquoted.
func isShellDelimiter(c byte) bool {
	switch c {
	case ' ', '\t', '\n', '\r', ';', '&', '|', '<', '>':
		return true
	}
	return false
}

// scanCommandTokens performs a best-effort shell tokenization of a command.
// It is intentionally conservative and only used for path inspection, never
// for execution (the real parser lives in pkg/shell). It splits on unquoted
// whitespace and shell metacharacters and strips surrounding single/double
// quotes, recording each token's span so callers can rewrite tokens in place
// while leaving everything else byte-for-byte intact.
//
// Here-document bodies are masked out before tokenizing: the body is data
// streamed to the command's stdin, not a path operand, so path-like text
// inside it (a drive root in a commit message, a ".." in a patch) must
// neither trigger a violation nor be rewritten in place. The mask also
// protects quote tracking: embedded quotes in heredoc data would otherwise
// fragment an enclosing quoted argument and expose the body's words as bare
// tokens. Masking preserves byte offsets and length, so token spans remain
// valid against the original command.
func scanCommandTokens(command string) []commandToken {
	var tokens []commandToken
	masked := maskHeredocBodies(command)
	i := 0
	for i < len(masked) {
		if isShellDelimiter(masked[i]) {
			i++
			continue
		}
		start := i
		var text []byte
		var quote byte
		var openQuote byte
		for i < len(masked) {
			c := masked[i]
			if quote != 0 {
				if c == quote {
					quote = 0
					i++
					continue
				}
				text = append(text, c)
				i++
				continue
			}
			if c == '\'' || c == '"' {
				if openQuote == 0 {
					openQuote = c
				}
				quote = c
				i++
				continue
			}
			if isShellDelimiter(c) {
				break
			}
			text = append(text, c)
			i++
		}
		tokens = append(tokens, commandToken{text: string(text), start: start, end: i, quote: openQuote})
	}
	return tokens
}

// maskHeredocBodies returns a copy of command with every here-document body
// blanked to spaces (newlines preserved, byte offsets unchanged). The parser
// then cannot see any path-like text or quotes inside heredoc data.
//
// The pre-pass is deliberately quote-agnostic: any "<<" sequence is treated
// as a heredoc introducer. A false positive here (e.g. "echo \"a << b\""
// swallowing a later line) only suppresses the static text scan for those
// bytes; the runtime exec/open handlers remain the authoritative gate, so
// the bias toward masking errs on the side of availability, not evasion.
func maskHeredocBodies(command string) string {
	if !strings.Contains(command, "<<") {
		return command
	}
	masked := []byte(command)
	blank := func(from, to int) {
		for j := from; j < to; j++ {
			if masked[j] != '\n' {
				masked[j] = ' '
			}
		}
	}
	var pending []pendingHeredoc
	i := 0
	for i < len(masked) {
		// Herestring "<<<word": the operand is stdin data too, so mask it.
		if masked[i] == '<' && i+2 < len(masked) && masked[i+1] == '<' && masked[i+2] == '<' {
			j := i + 3
			for j < len(masked) && masked[j] == ' ' {
				j++
			}
			if j < len(masked) && (masked[j] == '\'' || masked[j] == '"') {
				q := masked[j]
				j++
				for j < len(masked) && masked[j] != q {
					j++
				}
				if j < len(masked) {
					j++
				}
			} else {
				for j < len(masked) && !isShellDelimiter(masked[j]) {
					j++
				}
			}
			blank(i, j)
			i = j
			continue
		}
		if masked[i] == '<' && i+1 < len(masked) && masked[i+1] == '<' {
			if here, next, ok := parseHeredocIntro(command, i); ok {
				if here != nil {
					pending = append(pending, *here)
				}
				i = next
				continue
			}
		}
		if masked[i] == '\n' && len(pending) > 0 {
			// Stacked introducers ("cat <<A <<B") stream their bodies in
			// source order, each starting on the line after the previous
			// terminator.
			start := i + 1
			pos := i
			for _, here := range pending {
				pos = skipHeredocBody(command, pos, here)
			}
			blank(start, pos)
			pending = pending[:0]
			i = pos
			continue
		}
		i++
	}
	return string(masked)
}

// pendingHeredoc describes a here-document introducer whose body has not yet
// been consumed by the scanner.
type pendingHeredoc struct {
	delim string
	dash  bool
}

// parseHeredocIntro parses a here-document introducer at index i ("<<" or
// "<<-"). It returns the pending heredoc (nil for an expanded or empty
// delimiter, which cannot be matched reliably) and the index to resume
// scanning at. ok is false for constructs that carry no body — the "<<<"
// herestring and unparseable intros — in which case scanning proceeds
// token-by-token as before.
func parseHeredocIntro(command string, i int) (*pendingHeredoc, int, bool) {
	j := i + 2
	if j < len(command) && command[j] == '<' {
		return nil, i, false // "<<<" herestring: no body
	}
	here := &pendingHeredoc{}
	if j < len(command) && command[j] == '-' {
		here.dash = true
		j++
	}
	// Delimiter word: quoted or bare, ending at whitespace or a metachar.
	var delim strings.Builder
	closed := false
	for j < len(command) {
		c := command[j]
		if c == '\'' || c == '"' {
			if closed {
				break // trailing text after a closed quote: bare word
			}
			q := c
			j++
			for j < len(command) && command[j] != q {
				delim.WriteByte(command[j])
				j++
			}
			if j >= len(command) {
				return nil, i, false
			}
			j++
			closed = true
			continue
		}
		if delim.Len() > 0 && closed && !isShellDelimiter(c) {
			// Concatenation like <<'E'"OF": treat as unmatchable.
			return nil, i, false
		}
		if delim.Len() > 0 && (isShellDelimiter(c) || c == '<' || c == '>') {
			break
		}
		if delim.Len() == 0 && (isShellDelimiter(c) || c == '<' || c == '>') {
			// "<<" with no delimiter word (e.g. "cat <<;"): nothing to match.
			return nil, j, true
		}
		if c == '$' || c == '`' || c == '(' || c == '{' {
			// Expanded delimiter: its runtime value is unknown, so the body
			// cannot be delimited reliably.
			return nil, i, false
		}
		delim.WriteByte(c)
		j++
	}
	word := delim.String()
	if word == "" {
		return nil, j, true
	}
	here.delim = word
	return here, j, true
}

// skipHeredocBody consumes the here-document body that starts at the newline
// index i and returns the index just past the terminator line. A body with no
// terminator runs to the end of the command.
func skipHeredocBody(command string, i int, here pendingHeredoc) int {
	pos := i + 1
	for pos < len(command) {
		lineEnd := strings.IndexByte(command[pos:], '\n')
		var line string
		var next int
		if lineEnd < 0 {
			line = command[pos:]
			next = len(command)
		} else {
			line = command[pos : pos+lineEnd]
			next = pos + lineEnd + 1
		}
		trimmed := strings.TrimSuffix(line, "\r")
		if here.dash {
			trimmed = strings.TrimLeft(trimmed, "\t")
		}
		if trimmed == here.delim {
			return next
		}
		pos = next
	}
	return len(command)
}

// CorrectCommandPaths relocates absolute-looking arguments that were meant to
// be workspace-relative. It rewrites, in place, only tokens that are written
// as an absolute path (a leading separator or a drive specifier) so that they
// resolve inside the workspace using HeuristicClean. Remote URLs (https://…),
// remote hosts, Go import paths (github.com/x/y), relative operands (./…) and
// ordinary words are deliberately left untouched, which prevents the previous
// regex-based pass from corrupting such arguments. Quoting is preserved.
func CorrectCommandPaths(command string, absWorkingDir string) string {
	tokens := scanCommandTokens(command)
	if len(tokens) == 0 {
		return command
	}

	var sb strings.Builder
	lastIdx := 0
	for _, tok := range tokens {
		// Copy the gap between the previous token and this one verbatim.
		sb.WriteString(command[lastIdx:tok.start])

		switch {
		case isRemoteURL(tok.text):
			sb.WriteString(command[tok.start:tok.end])
		case isAbsoluteLike(tok.text):
			corrected := filepathext.HeuristicClean(absWorkingDir, tok.text)
			corrected = filepath.ToSlash(corrected)
			if tok.quote != 0 {
				sb.WriteByte(tok.quote)
				sb.WriteString(corrected)
				sb.WriteByte(tok.quote)
			} else {
				sb.WriteString(corrected)
			}
		default:
			sb.WriteString(command[tok.start:tok.end])
		}
		lastIdx = tok.end
	}
	sb.WriteString(command[lastIdx:])
	return sb.String()
}

// tempEnvVars are environment-variable names whose expansion points at the OS
// temporary directory. They are trusted when TrustTempRoots is on and rejected
// otherwise, because their value is unknown until execution time.
var tempEnvVars = map[string]struct{}{
	"tmp": {}, "temp": {}, "tmpdir": {}, "tempdir": {},
}

// outsideEnvVars are environment-variable names whose expansion points at a
// location outside the workspace (the user profile, temp, or system roots).
var outsideEnvVars = map[string]struct{}{
	"home": {}, "homedrive": {}, "homedir": {}, "userprofile": {},
	"tmp": {}, "temp": {}, "tmpdir": {}, "tempdir": {},
	"appdata": {}, "localappdata": {}, "public": {}, "desktopdirectory": {},
	"programfiles": {}, "allusersprofile": {}, "systemdrive": {}, "windir": {},
}

// envVarRegexp captures the ${NAME}, $NAME and %NAME% shell expansion forms.
var envVarRegexp = regexp.MustCompile(`\$(\{?)([A-Za-z_][0-9A-Za-z_]*)\}?|%([A-Za-z_][0-9A-Za-z_]*)%`)

// referencesOutsideEnvVar reports whether a path-like token embeds an
// environment variable whose value lives outside the workspace. When trustTemp
// is set, a token that only references temporary-directory variables is treated
// as safe, because those roots are part of the trusted set in that mode.
func referencesOutsideEnvVar(token string, trustTemp bool) bool {
	if !strings.ContainsAny(token, "/\\") {
		return false
	}
	for _, m := range envVarRegexp.FindAllStringSubmatch(token, -1) {
		name := m[2] // $NAME or ${NAME}
		if name == "" {
			name = m[3] // %NAME%
		}
		lower := strings.ToLower(name)
		if _, ok := outsideEnvVars[lower]; !ok {
			continue
		}
		if trustTemp {
			if _, temp := tempEnvVars[lower]; temp {
				continue // trusted root, not a violation
			}
		}
		return true
	}
	return false
}

// resolveTokenBase maps a path token to an absolute path for the bounds check:
// a leading "~" is expanded to the user home when known, otherwise the token
// is treated as absolute (SmartIsAbs) or relative to base.
func resolveTokenBase(token, base, home string) string {
	s := filepath.ToSlash(token)
	if home != "" && (s == "~" || strings.HasPrefix(s, "~/")) {
		return filepath.Clean(filepath.Join(home, strings.TrimLeft(s[1:], "/")))
	}
	if filepathext.SmartIsAbs(token) {
		return filepath.Clean(token)
	}
	return filepath.Clean(filepath.Join(base, s))
}

// ValidateCommandPaths checks whether any path in the raw (pre-expansion)
// command string escapes the workspace. Unlike the previous implementation it is
// not gated on the presence of a known I/O command name: every token that could
// refer to a location outside the workspace is resolved and bounds-checked.
// This closes the bypass where write/read vectors that are not in the I/O
// keyword list (cp, mv, tee, dd, shell redirection targets, …) skipped
// validation entirely. Tokens that cannot escape — remote URLs, import paths,
// package names and in-tree relative operands — are ignored.
//
// The pass is expansion-aware. A token such as "$dir/etc/passwd" or the result
// of an inline assignment like "dir=/etc; cat $dir/passwd" only becomes an
// absolute, out-of-workspace path once $dir is substituted, which the
// pre-expansion classifier alone cannot see and which the dynamic, argv-only
// Confinement.Blocked cannot reach for shell redirection operands (the
// interpreter opens "<"/">" files without ever routing the path through the exec
// handler) or for library-native builtins such as "source"/".". We therefore
// substitute inline assignments and, for tokens that already look like paths,
// the process environment, before deciding. This is intentionally narrow: unset
// names are left literal so we never fabricate a leading separator, and
// temporary-directory names are left literal so trusted staging stays usable.
// Command substitutions $(…) and backticks are deliberately NOT expanded here
// (running them would duplicate side effects); they remain the documented
// residual and are handled by the post-expansion walk discussed in the design.
func ValidateCommandPaths(command string, absWorkingDir string) error {
	home := ""
	if h, err := os.UserHomeDir(); err == nil {
		home = h
	}

	env, caseInsensitive := buildEnvLookup()
	assign := collectAssignments(command, env, caseInsensitive)

	// ValidateCommandPaths bounds ordinary path tokens to the workspace only,
	// matching the historical policy. Temporary-directory access is granted via
	// the trusted temp-variable handling in referencesOutsideEnvVar and the
	// temp-var-literal rule in expandVarsForValidation, not by widening the
	// filesystem root set (the OS temp directory is large and a workspace is
	// commonly nested inside it, so trusting it as a root would weaken
	// traversal detection).
	trusted := []string{filepath.Clean(absWorkingDir)}

	for _, tok := range scanCommandTokens(command) {
		raw := tok.text
		if raw == "" || isRemoteURL(raw) || isDeviceFile(raw) {
			continue
		}

		// A token that references a known-outside variable (directly, or through
		// an inline assignment that resolves to one) is refused regardless of the
		// separator heuristics below, because its value is unknown until exec.
		assignExpanded := expandVarsForValidation(raw, assign, nil, caseInsensitive)
		if referencesOutsideEnvVar(raw, true) || referencesOutsideEnvVar(assignExpanded, true) {
			return fmt.Errorf("Security violation: path %s is outside workspace", raw)
		}

		// Only consult the real environment for tokens that already read as a
		// path reference (contain a separator). This keeps the blast radius small
		// so unrelated arguments such as grep '$HOME' or unset "$FOO/bar" are not
		// rewritten into fabricated absolute paths.
		expanded := assignExpanded
		if strings.ContainsAny(raw, "/\\") {
			expanded = expandVarsForValidation(raw, assign, env, caseInsensitive)
		}

		if (!isEscapablePathToken(raw) && !isEscapablePathToken(expanded)) || isDeviceFile(expanded) {
			continue
		}

		absPath := resolveTokenBase(expanded, absWorkingDir, home)
		if !insideAny(absPath, trusted) {
			return fmt.Errorf("Security violation: path %s is outside workspace", absPath)
		}
	}

	return nil
}

// isIdentName reports whether s is a valid shell variable name: a leading
// letter or underscore followed by letters, digits, or underscores.
func isIdentName(s string) bool {
	if s == "" {
		return false
	}
	const identStart = "ABCDEFGHIJKLMNOPQRSTUVWXYZ_abcdefghijklmnopqrstuvwxyz"
	const identChars = identStart + "0123456789"
	if strings.IndexByte(identStart, s[0]) < 0 {
		return false
	}
	for i := 1; i < len(s); i++ {
		if strings.IndexByte(identChars, s[i]) < 0 {
			return false
		}
	}
	return true
}

// buildEnvLookup indexes the process environment for variable substitution used
// only during validation. Keys are stored both verbatim and lower-cased; on
// Windows the lookup is additionally case-insensitive to match the shell.
func buildEnvLookup() (map[string]string, bool) {
	caseInsensitive := runtime.GOOS == "windows"
	env := map[string]string{}
	for _, kv := range os.Environ() {
		key, value, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		env[key] = value
		env[strings.ToLower(key)] = value
	}
	return env, caseInsensitive
}

// collectAssignments harvests inline "name=value" words from the command so a
// later "$name/…" reference can be resolved during validation. Assignment values
// are themselves expanded against the environment and any earlier assignment, so
// "root=$SystemRoot; jq . $root/System32/x" is caught.
func collectAssignments(command string, env map[string]string, caseInsensitive bool) map[string]string {
	assign := map[string]string{}
	for _, tok := range scanCommandTokens(command) {
		name, value, ok := strings.Cut(tok.text, "=")
		if !ok || !isIdentName(name) {
			continue
		}
		assign[strings.ToLower(name)] = expandVarsForValidation(value, assign, env, caseInsensitive)
	}
	return assign
}

// expandVarsForValidation substitutes $NAME, ${NAME} and %NAME% references in a
// token using the inline-assignment map and, when env is non-nil, the process
// environment. Unset names and temporary-directory names are returned verbatim
// (preserving the existing "unknown variable is not an outside target" policy
// and keeping trusted temp staging usable). Command substitutions and backticks
// do not match the variable regexp and so pass through untouched.
func expandVarsForValidation(s string, assign map[string]string, env map[string]string, caseInsensitive bool) string {
	return envVarRegexp.ReplaceAllStringFunc(s, func(match string) string {
		name := strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(match, "$"), "{"), "}")
		lower := strings.ToLower(name)
		if _, temp := tempEnvVars[lower]; temp {
			return match
		}
		if v, ok := lookupExpanded(name, assign, env, caseInsensitive); ok {
			return v
		}
		return match
	})
}

// lookupExpanded resolves a variable name against the assignment map and, when
// env is non-nil, the environment, honouring case-insensitivity where requested.
func lookupExpanded(name string, assign map[string]string, env map[string]string, caseInsensitive bool) (string, bool) {
	if v, ok := assign[name]; ok {
		return v, true
	}
	if v, ok := assign[strings.ToLower(name)]; ok {
		return v, true
	}
	if env == nil {
		return "", false
	}
	if v, ok := env[name]; ok {
		return v, true
	}
	if caseInsensitive {
		if v, ok := env[strings.ToLower(name)]; ok {
			return v, true
		}
	}
	return "", false
}

// Confinement configures the dynamic, post-expansion argv policy. The zero
// value (empty WorkspaceRoot) disables it entirely, which is what the trusted
// hook runner uses so user-authored aliases are not over-constrained.
type Confinement struct {
	// WorkspaceRoot is the absolute directory argv is confined to. When empty
	// the policy is a no-op.
	WorkspaceRoot string
	// TrustTempRoots permits arguments that resolve inside the OS temporary
	// directory (os.TempDir and the TMP*/TEMP* variables). It is on by default
	// because a very large amount of ordinary tooling stages files there.
	TrustTempRoots bool
	// ExtraRoots are additional absolute directories the caller explicitly
	// trusts (the tools.bash.trusted_extra_roots config knob).
	ExtraRoots []string
}

// deviceFiles are well-known, world-writable device nodes that are safe to name
// as an argument (tee /dev/null, redirections to /dev/zero, …). They are never
// confined, mirroring the fact that they cannot leak workspace data.
var deviceFiles = map[string]struct{}{
	"/dev/null":    {},
	"/dev/stdin":   {},
	"/dev/stdout":  {},
	"/dev/stderr":  {},
	"/dev/zero":    {},
	"/dev/full":    {},
	"/dev/random":  {},
	"/dev/urandom": {},
	"/dev/tty":     {},
	"nul":          {},
}

// Blocked reports the first argv element (skipping argv[0], the program itself)
// that resolves to a location outside the trusted roots, or nil when every
// argument is permitted. cwd is the directory the command runs in (the
// interpreter's post-"cd" location) and is the base against which relative
// arguments are resolved.
func (c Confinement) Blocked(argv []string, cwd string) error {
	if c.WorkspaceRoot == "" {
		return nil // confinement disabled (e.g. trusted hook runner)
	}
	if len(argv) < 2 {
		return nil // nothing beyond the program name to inspect
	}

	home := ""
	if h, err := os.UserHomeDir(); err == nil {
		home = h
	}
	if cwd == "" {
		cwd = c.WorkspaceRoot
	}

	roots := c.roots()
	// Relative operands are only provably safe while cwd itself sits inside a
	// trusted root; see the exemption below.
	cwdInside := insideAny(filepath.Clean(cwd), roots)

	for _, arg := range argv[1:] {
		if arg == "" || isRemoteURL(arg) {
			continue
		}
		if isDeviceFile(arg) {
			continue
		}
		// Home expansions and filesystem roots are always refused regardless of
		// the resolved base: "~" targets the user profile and "/" (or a drive
		// root) spans the entire filesystem, neither can be proven inside a
		// trusted root. This closes vectors such as "cat ~/.ssh/id", "find /"
		// and "rm -rf /" which the escapable-classifier alone would otherwise
		// wave through because they look degenerate.
		if isHomePath(arg) || isFilesystemRoot(arg) {
			return fmt.Errorf("Security violation: path %s is outside workspace", resolveTokenBase(arg, cwd, home))
		}
		// An argument that embeds an unresolvable outside environment variable
		// (the interpreter kept it literal, e.g. inside single quotes) cannot be
		// bounds-checked, so reject it. Temporary variables are tolerated when
		// temp roots are trusted.
		if referencesOutsideEnvVar(arg, c.TrustTempRoots) {
			return fmt.Errorf("Security violation: path %s is outside workspace", arg)
		}
		// Relative operands are only exempt while the current directory is
		// itself inside a trusted root: a mid-command "cd" can move cwd
		// outside the workspace, and then a harmless-looking relative
		// operand resolves to an outside location. Once cwd has escaped,
		// every path-like operand must be resolved and bounds-checked.
		if !isEscapablePathToken(arg) && cwdInside {
			continue
		}
		absPath := resolveTokenBase(arg, cwd, home)
		if insideAny(absPath, roots) {
			continue
		}
		return fmt.Errorf("Security violation: path %s is outside workspace", absPath)
	}
	return nil
}

// DirBlocked reports whether the interpreter's current working directory
// itself lies outside the trusted roots. The cd builtin is dispatched
// inside the interpreter and never reaches the exec-handler chain, so a
// mid-command "cd /outside" escapes argv-based checks entirely; commands
// that operate on the cwd without naming a path ("ls", "du", "rm -rf .")
// must still be refused while cwd is outside the workspace.
func (c Confinement) DirBlocked(dir string) error {
	if c.WorkspaceRoot == "" || dir == "" {
		return nil
	}
	absDir := filepath.Clean(dir)
	if insideAny(absDir, c.roots()) {
		return nil
	}
	return fmt.Errorf("Security violation: working directory %s is outside workspace", absDir)
}

// isHomePath reports whether a token begins with a home-directory expansion.
func isHomePath(token string) bool {
	s := filepath.ToSlash(token)
	return s == "~" || strings.HasPrefix(s, "~/")
}

// driveRootRegexp matches a bare drive root such as "C:/" or "C:".
var driveRootRegexp = regexp.MustCompile(`^[A-Za-z]:[/\\]?$`)

// isFilesystemRoot reports whether a token names an entire filesystem root: a
// lone "/", a lone "\", or a Windows drive root ("C:/", "C:").
func isFilesystemRoot(token string) bool {
	s := filepath.ToSlash(token)
	if s == "/" {
		return true
	}
	return driveRootRegexp.MatchString(token)
}

// roots returns the set of absolute directories an argument may resolve inside:
// the workspace root, any caller-added roots and, when trusted, the OS
// temporary directory.
func (c Confinement) roots() []string {
	roots := make([]string, 0, len(c.ExtraRoots)+2)
	roots = append(roots, filepath.Clean(c.WorkspaceRoot))
	roots = append(roots, c.ExtraRoots...)
	if c.TrustTempRoots {
		roots = append(roots, filepath.Clean(os.TempDir()))
	}
	return roots
}

// isDeviceFile reports whether an argument names a well-known device node.
// It is consulted by both the dynamic argv check (Blocked) and the raw
// command-string check (ValidateCommandPaths) so redirection targets such as
// "2>/dev/null" are not falsely rejected, on any platform: filepath.Clean
// normalizes the Windows spelling (\dev\null) back to the allow-listed form.
func isDeviceFile(arg string) bool {
	s := strings.ToLower(filepath.ToSlash(filepath.Clean(arg)))
	if s == "" {
		return false
	}
	_, ok := deviceFiles[s]
	return ok
}

// insideAny reports whether absPath resolves inside any of the given roots.
// filepathext.IsInside resolves symlinks on both sides, so a path whose final
// target escapes a trusted root is still rejected at check time.
func insideAny(absPath string, roots []string) bool {
	for _, root := range roots {
		if filepathext.IsInside(absPath, root) {
			return true
		}
	}
	return false
}
