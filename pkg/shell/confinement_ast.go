package shell

import (
	"fmt"
	"io"
	"strings"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"

	"github.com/hackafterdark/phosphor/internal/pathguard"
)

// sourcingCommands are the shell builtins that read and evaluate a file whose
// name is passed as a plain word argument. They are dispatched *inside* the
// interpreter as special builtins, so their file operand never reaches the
// exec-handler middleware chain (and thus not [pathConfinementHandler]); nor
// are they [syntax.Redirect] nodes. The AST walk must check them explicitly or
// an out-of-workspace file is opened and read with no confinement at all.
var sourcingCommands = map[string]bool{
	".":       true,
	"source":  true,
	"builtin": true,
}

// inputRedirectOps are the redirection operators whose operand names a file
// the interpreter opens for reading. These never surface in exec argv, so
// neither the static [pathguard.ValidateCommandPaths] heuristic nor the
// argv-bounded [pathConfinementHandler] can see the path they ultimately
// reference once it is produced by $VAR, $(...), or other post-parse
// expansion. They are the residual the AST walk below is responsible for.
var inputRedirectOps = map[syntax.RedirOperator]bool{
	syntax.RdrIn:    true,
	syntax.RdrInOut: true,
}

// outputRedirectOps are the redirection operators whose operand names a file
// the interpreter opens for writing. A fully-expandable operand is bounds
// checked the same way as an input operand; an unprovable operand (it
// depends on an unexecuted command substitution) is allowed so build tooling
// such as "make > $(echo out.txt)" is not broken, since writing through it is
// both lower-risk and already partially covered by the block list.
var outputRedirectOps = map[syntax.RedirOperator]bool{
	syntax.RdrOut:     true,
	syntax.AppOut:     true,
	syntax.RdrClob:    true,
	syntax.AppClob:    true,
	syntax.RdrAll:     true,
	syntax.RdrAllClob: true,
	syntax.AppAll:     true,
	syntax.AppAllClob: true,
}

// checkProgram bounds-checks every file operand the interpreter opens itself
// rather than handing to an exec'd program's argv: redirection operands and
// sourcing-builtin ("." / "source") operands. It must run after the command
// is parsed but before [interp.Runner.Run] so the offending file is rejected
// without the side effect of ever being opened.
//
// It is a no-op when confinement is disabled (nil conf or empty workspace), so
// trusted surfaces such as hooks are unaffected.
func checkProgram(conf *pathguard.Confinement, cwd string, env []string, file *syntax.File) error {
	if conf == nil || conf.WorkspaceRoot == "" || file == nil {
		return nil
	}

	// violation carries the first confinement refusal discovered during the
	// walk; once set, traversal short-circuits.
	var violation error

	// checkOperand expands a single operand word and applies the confinement
	// policy. blockIfUnprovable selects the read-vs-write stance: a target that
	// depends on an unexecuted substitution is blocked for reads (the
	// substitution is itself the leak vector) but tolerated for writes.
	checkOperand := func(word *syntax.Word, blockIfUnprovable bool) bool {
		operand, unprovable, err := expandRedirectOperand(env, word)
		if err != nil {
			if blockIfUnprovable {
				violation = unprovableOperandError(word, err)
				return false
			}
			return true
		}
		if unprovable {
			if blockIfUnprovable {
				violation = unprovableOperandError(word, nil)
				return false
			}
			return true
		}
		if operand == "" {
			return true
		}
		if e := conf.Blocked([]string{"operand", operand}, cwd); e != nil {
			violation = e
			return false
		}
		return true
	}

	visit := func(node syntax.Node) bool {
		// Walk invokes f(nil) after descending into a node's children; the
		// post-visit carries no work. Stop once a violation is recorded.
		if node == nil || violation != nil {
			return false
		}
		switch n := node.(type) {
		case *syntax.Redirect:
			if n == nil || n.Word == nil {
				return true
			}
			switch {
			case inputRedirectOps[n.Op]:
				return checkOperand(n.Word, true)
			case outputRedirectOps[n.Op]:
				return checkOperand(n.Word, false)
			default:
				// Here-doc bodies and fd-duplication operands (<& , >&) are not
				// file paths; still descend to catch nested redirections.
				return true
			}
		case *syntax.CallExpr:
			if n == nil || len(n.Args) < 2 || !isSourcingCommand(n.Args[0], env) {
				return true
			}
			// Args[0] is "." / "source"; the remaining words are files to read.
			for _, word := range n.Args[1:] {
				if word == nil {
					continue
				}
				if strings.HasPrefix(strings.TrimSpace(word.Lit()), "-") {
					continue // option word, not a path
				}
				if !checkOperand(word, true) {
					return false
				}
			}
			return true
		}
		return true
	}

	for _, stmt := range file.Stmts {
		syntax.Walk(stmt, visit)
		if violation != nil {
			return violation
		}
	}
	return nil
}

// isSourcingCommand reports whether a command-word names a sourcing builtin.
// It handles both a literal (".", "source") and a name produced only through
// expansion (e.g. $SRC set to "source"), which would otherwise slip past the
// argv-bounded pathConfinementHandler because the resolved program is still a
// special builtin executed inside the interpreter.
func isSourcingCommand(word *syntax.Word, env []string) bool {
	if word == nil {
		return false
	}
	if name := word.Lit(); name != "" {
		return sourcingCommands[name]
	}
	if expanded, _, err := expandRedirectOperand(env, word); err == nil {
		return sourcingCommands[expanded]
	}
	return false
}

// expandRedirectOperand expands an operand word using the interpreter's own
// expansion rules but without globbing and without executing command or
// process substitutions. It returns the expanded path, whether the word
// referenced an unexecuted substitution (making the path "unprovable"), and
// any expansion error.
func expandRedirectOperand(env []string, word *syntax.Word) (string, bool, error) {
	unprovable := false
	cfg := &expand.Config{
		Env: expand.ListEnviron(withNonInteractiveEnv(env)...),
		CmdSubst: func(_ io.Writer, _ *syntax.CmdSubst) error {
			unprovable = true
			return nil
		},
		ProcSubst: func(_ *syntax.ProcSubst) (string, error) {
			unprovable = true
			return "", nil
		},
	}
	// Document() expands $VAR/$(...) in a single word but deliberately skips
	// brace, tilde, and pathname (glob) expansion, matching what the runtime
	// opens for these operands and avoiding false matches against real files.
	expanded, err := expand.Document(cfg, word)
	return expanded, unprovable, err
}

// unprovableOperandError builds a security-violation error for a read operand
// whose concrete path cannot be verified to lie inside the workspace.
func unprovableOperandError(word *syntax.Word, cause error) error {
	text := word.Lit()
	if text == "" {
		text = "<substitution>"
	}
	if cause != nil {
		return fmt.Errorf("Security violation: operand %q is outside workspace or cannot be verified post-expansion: %w", text, cause)
	}
	return fmt.Errorf("Security violation: operand %q is outside workspace or cannot be verified post-expansion", text)
}
