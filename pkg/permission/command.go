package permission

import (
	"runtime"
	"slices"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// CommandCarrier is implemented by tool permission params that carry a raw
// shell command (bash). The permission service uses it to consult the
// command allowlist without importing the tool package.
type CommandCarrier interface {
	CommandText() string
}

// normalizeCmdName reduces a program word to a lowercase base name. The
// .exe suffix is stripped on Windows only: elsewhere a bare foo.exe is a
// legitimate ELF binary name (e.g. Go cross-compile helpers) and stripping
// it would wrongly alias go.exe onto the go allowlist entry.
func normalizeCmdName(word string) string {
	base := strings.ToLower(word)
	if i := strings.LastIndexAny(base, `/\`); i >= 0 {
		base = base[i+1:]
	}
	if runtime.GOOS == "windows" {
		base = strings.TrimSuffix(base, ".exe")
	}
	return base
}

// parseAllowEntries splits each allowlist entry into lowercase tokens and
// groups them by program. A bare entry ("git") contributes {git}; a prefix
// entry ("go build") contributes {go, build}.
func parseAllowEntries(allow []string) map[string][][]string {
	entries := map[string][][]string{}
	for _, raw := range allow {
		tokens := strings.Fields(raw)
		if len(tokens) == 0 {
			continue
		}
		normalized := make([]string, len(tokens))
		for i, tok := range tokens {
			normalized[i] = normalizeCmdName(tok)
		}
		program := normalized[0]
		entries[program] = append(entries[program], normalized)
	}
	return entries
}

// argvAllowed reports whether argv (already normalized, argv[0] is the
// program) starts with any allowlist entry for its program.
func argvAllowed(argv []string, entries map[string][][]string) bool {
	if len(argv) == 0 {
		return false
	}
	for _, entry := range entries[argv[0]] {
		if len(entry) > len(argv) {
			continue
		}
		match := true
		for i, tok := range entry {
			if argv[i] != tok {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// wordIsLiteral reports whether the word is a single literal that cannot
// change which program executes. Quoted literals (single or double) whose
// sole part is literal text also qualify.
func wordIsLiteral(word *syntax.Word) bool {
	if word == nil || len(word.Parts) != 1 {
		return false
	}
	switch part := word.Parts[0].(type) {
	case *syntax.Lit:
		return part.Value != ""
	case *syntax.SglQuoted:
		return true
	case *syntax.DblQuoted:
		return len(part.Parts) == 1 && litValue(part.Parts[0]) != ""
	}
	return false
}

// litValue returns the literal text of a word part, or "" if the part is
// not literal.
func litValue(part syntax.WordPart) string {
	if lit, ok := part.(*syntax.Lit); ok {
		return lit.Value
	}
	return ""
}

// wordLiteral returns the literal text of a word that wordIsLiteral
// accepts, unwrapping quotes.
func wordLiteral(word *syntax.Word) string {
	if word == nil || len(word.Parts) != 1 {
		return ""
	}
	switch part := word.Parts[0].(type) {
	case *syntax.Lit:
		return part.Value
	case *syntax.SglQuoted:
		return part.Value
	case *syntax.DblQuoted:
		if len(part.Parts) == 1 {
			return litValue(part.Parts[0])
		}
	}
	return ""
}

// wordHasSubstitution reports whether the word embeds a command or process
// substitution, which would execute code the allowlist never vets.
func wordHasSubstitution(word *syntax.Word) bool {
	if word == nil {
		return false
	}
	found := false
	syntax.Walk(word, func(n syntax.Node) bool {
		if n == nil || found {
			return false
		}
		switch n.(type) {
		case *syntax.CmdSubst, *syntax.ProcSubst:
			found = true
			return false
		}
		return true
	})
	return found
}

// nodeHasHiddenExecutables reports whether the subtree can spawn a program
// the statement walk would not see: subshells, command/process substitution,
// or function definitions. Those run code outside the allowlist's view, so
// the whole command must be prompted for.
func nodeHasHiddenExecutables(node syntax.Node) bool {
	found := false
	syntax.Walk(node, func(n syntax.Node) bool {
		if n == nil || found {
			return false
		}
		switch n.(type) {
		case *syntax.Subshell, *syntax.CmdSubst, *syntax.ProcSubst,
			*syntax.FuncDecl:
			found = true
			return false
		}
		return true
	})
	return found
}

// commandMatchesAllowlist reports whether every program executed by the
// shell program is covered by the pre-parsed allowlist entries. It is the
// auto-approve test for permissions.allow_commands: deny-by-default — any
// parse failure, substitution, subshell, or non-allowlisted program returns
// false so the normal permission prompt runs.
//
// Compound programs are evaluated piece by piece: `git status && go build`
// auto-approves only when BOTH sides match; `git status && rm -rf /` does
// not. Argument expansion ($VAR, globs) is tolerated — it cannot swap which
// program runs — but any word containing $(...) or <(...) is refused, and
// program words themselves must be literal.
func commandMatchesAllowlist(command string, entries map[string][][]string) bool {
	if len(entries) == 0 || strings.TrimSpace(command) == "" {
		return false
	}

	file, err := syntax.NewParser().Parse(strings.NewReader(command), "")
	if err != nil || len(file.Stmts) == 0 {
		return false
	}

	for _, stmt := range file.Stmts {
		if !stmtApproved(stmt, entries) {
			return false
		}
	}
	return true
}

// stmtApproved walks one statement, requiring every program it runs to
// match an allowlist entry.
func stmtApproved(stmt *syntax.Stmt, entries map[string][][]string) bool {
	if nodeHasHiddenExecutables(stmt.Cmd) {
		return false
	}
	switch cmd := stmt.Cmd.(type) {
	case *syntax.CallExpr:
		return callApproved(cmd, entries)
	case *syntax.BinaryCmd:
		// &&, ||, and | chain two statements; both sides must match.
		return stmtApproved(cmd.X, entries) && stmtApproved(cmd.Y, entries)
	case *syntax.Block:
		// A block { ...; } runs the enclosed statements in the current
		// shell; the walk covers them.
		for _, inner := range cmd.Stmts {
			if !stmtApproved(inner, entries) {
				return false
			}
		}
		return true
	case *syntax.IfClause, *syntax.WhileClause, *syntax.ForClause,
		*syntax.CaseClause, *syntax.DeclClause:
		// Control flow and declarations run inside the interpreter and
		// cannot execute a program the walk above would miss; any simple
		// command nested in them is reachable only through Subshell or
		// CmdSubst nodes, which nodeHasHiddenExecutables already rejects.
		return true
	default:
		// Unknown syntax: stay deny-by-default.
		return false
	}
}

// callApproved vets one simple command: literal program word, no
// substitutions anywhere, and argv matching an allowlist entry.
func callApproved(call *syntax.CallExpr, entries map[string][][]string) bool {
	if len(call.Args) == 0 {
		// A pure assignment (FOO=bar) or bare redirect runs no program;
		// refusing keeps the approved surface small and explicit.
		return false
	}
	if slices.ContainsFunc(call.Args, wordHasSubstitution) {
		return false
	}
	for _, assign := range call.Assigns {
		if assign == nil || assign.Value == nil {
			continue
		}
		if wordHasSubstitution(assign.Value) {
			return false
		}
	}
	if !wordIsLiteral(call.Args[0]) {
		// The program name itself is dynamic ($CMD, globs); we cannot
		// prove which binary runs.
		return false
	}
	argv := make([]string, 0, len(call.Args))
	provablyLiteral := true
	for _, word := range call.Args {
		if !wordIsLiteral(word) {
			provablyLiteral = false
			argv = append(argv, "")
			continue
		}
		argv = append(argv, normalizeCmdName(wordLiteral(word)))
	}
	if !argvAllowed(argv, entries) {
		return false
	}
	if !provablyLiteral {
		// An argument word expanded at runtime; a program-only entry
		// still covers the command, but an entry that depends on the
		// expanded argument cannot be proven.
		for _, entry := range entries[argv[0]] {
			if len(entry) == 1 {
				return true
			}
		}
		return false
	}
	return true
}
