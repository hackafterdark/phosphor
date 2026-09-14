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
func pathConfinementHandler(conf *pathguard.Confinement) func(next interp.ExecHandlerFunc) interp.ExecHandlerFunc {
	return func(next interp.ExecHandlerFunc) interp.ExecHandlerFunc {
		return func(ctx context.Context, args []string) error {
			if conf != nil && conf.WorkspaceRoot != "" && len(args) > 1 {
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
