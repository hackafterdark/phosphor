package shell

import (
	"context"
	"log/slog"
	"os"
	"strings"

	"mvdan.cc/sh/v3/interp"

	"github.com/hackafterdark/phosphor/internal/pathguard"
)

// pathConfinementHandler returns exec-handler middleware that enforces the
// workspace path-confinement policy on the *fully expanded* argv the
// interpreter is about to exec. Because it runs at exec time it observes the
// result of $VAR, $(…), backtick, glob and brace expansion — the class of
// escape that the static, pre-expansion path in the bash tool is blind to. It
// deliberately does not inspect argv[0] (the program name): LookPath-based
// resolution of the binary is a separate concern owned by the exec layer.
//
// The current working directory is taken from the interpreter handler context
// so that a mid-command "cd" is honoured when resolving relative operands.
//
// A nil confinement or one without a workspace root disables the check, which
// is how the trusted hook runner keeps running user-authored commands.
//
// A command whose argv matches any of the provided blockFuncs is passed
// through unchecked so that the downstream blockHandler rejects it with the
// authoritative "not allowed for security reasons" verdict. The deny list is
// the stronger policy (the program may not run at all regardless of its
// operands), so it must win over the path-bounds verdict when both apply —
// otherwise a deny-listed command that merely happens to carry an
// out-of-workspace operand (e.g. `sudo rm -rf /`) would surface a misleading
// path error instead of the ban.
func pathConfinementHandler(conf *pathguard.Confinement, blockFuncs []BlockFunc) func(next interp.ExecHandlerFunc) interp.ExecHandlerFunc {
	return func(next interp.ExecHandlerFunc) interp.ExecHandlerFunc {
		return func(ctx context.Context, args []string) error {
			if conf != nil && conf.WorkspaceRoot != "" && !isBlocked(args, blockFuncs) {
				hc := interp.HandlerCtx(ctx)
				// The cd builtin is dispatched inside the interpreter and
				// never reaches this chain, so cwd itself may already sit
				// outside the workspace after a mid-command "cd". Commands
				// that act on the cwd without naming a path ("ls", "du")
				// must be refused in that state.
				if err := conf.DirBlocked(hc.Dir); err != nil {
					slog.InfoContext(ctx, "Command blocked by workspace path confinement",
						"command", args[0],
						"args", args,
						"workspace", conf.WorkspaceRoot,
						"reason", err.Error(),
					)
					return err
				}
				if len(args) > 1 {
					if err := conf.Blocked(args, hc.Dir); err != nil {
						slog.InfoContext(ctx, "Command blocked by workspace path confinement",
							"command", args[0],
							"args", args,
							"workspace", conf.WorkspaceRoot,
							"reason", err.Error(),
						)
						return err
					}
				}
			}
			return next(ctx, args)
		}
	}
}

// isBlocked reports whether any blockFunc rejects the given argv. It mirrors
// the decision blockHandler makes so pathConfinementHandler can defer to the
// deny list rather than pre-empting it with a path-bounds verdict.
func isBlocked(args []string, blockFuncs []BlockFunc) bool {
	for _, blockFunc := range blockFuncs {
		if blockFunc(args) {
			return true
		}
	}
	return false
}

// confinementCallHandler returns a [interp.CallHandlerFunc] that gates the
// interpreter's in-process builtins.
//
// The exec and open gates only see exec'd programs and interpreter-opened
// files; the cd/pwd builtins are dispatched inside the interpreter and never
// appear there. Without this gate, a `cd` whose target the static pass cannot
// prove (relative traversal like `cd ..`, `$VAR`, or `$(...)`) can move the
// cwd outside the workspace mid-command, and the very next `pwd` prints the
// outside path into the transcript.
//
// The handler enforces two checks: the cwd itself must sit inside a trusted
// root before *any* simple command runs, and cd's post-expansion target must
// land inside a trusted root. A nil confinement or one without a workspace
// root disables the gate, matching the exec/open handlers.
func confinementCallHandler(conf *pathguard.Confinement) interp.CallHandlerFunc {
	return func(ctx context.Context, args []string) ([]string, error) {
		if conf == nil || conf.WorkspaceRoot == "" || len(args) == 0 {
			return args, nil
		}
		hc := interp.HandlerCtx(ctx)
		if err := conf.DirBlocked(hc.Dir); err != nil {
			slog.InfoContext(ctx, "Command blocked by workspace path confinement",
				"command", args[0],
				"workspace", conf.WorkspaceRoot,
				"reason", err.Error(),
			)
			return args, err
		}
		if args[0] == "cd" {
			if len(args) == 1 {
				// cd with no target lands in HOME, which is never a
				// trusted root; bounds-check it explicitly.
				if home, err := os.UserHomeDir(); err == nil {
					if err := conf.Blocked([]string{"cd", home}, hc.Dir); err != nil {
						return args, err
					}
				}
				return args, nil
			}
			for _, arg := range args[1:] {
				if strings.HasPrefix(arg, "-") {
					// Flag or `cd -` (OLDPWD). OLDPWD can only name a
					// directory a previous (gated) cd accepted, so it is
					// always inside a trusted root.
					continue
				}
				if err := conf.Blocked([]string{"cd", arg}, hc.Dir); err != nil {
					slog.InfoContext(ctx, "cd target blocked by workspace path confinement",
						"target", arg,
						"workspace", conf.WorkspaceRoot,
						"reason", err.Error(),
					)
					return args, err
				}
			}
		}
		return args, nil
	}
}
