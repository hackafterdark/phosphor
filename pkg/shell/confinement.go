package shell

import (
	"context"
	"log/slog"

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
			if conf != nil && conf.WorkspaceRoot != "" && len(args) > 1 && !isBlocked(args, blockFuncs) {
				dir := interp.HandlerCtx(ctx).Dir
				if err := conf.Blocked(args, dir); err != nil {
					slog.InfoContext(ctx, "Command blocked by workspace path confinement",
						"command", args[0],
						"args", args,
						"workspace", conf.WorkspaceRoot,
						"reason", err.Error(),
					)
					return err
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
