package shell

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"mvdan.cc/sh/moreinterp/coreutils"
	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/syntax"

	"github.com/hackafterdark/phosphor/internal/pathguard"
)

// RunOptions configures a single stateless shell execution via [Run].
//
// The zero value is not useful; at minimum Command must be set. Stdin,
// Stdout, and Stderr may be nil (nil readers/writers are treated as
// empty/discard). BlockFuncs may be nil to disable block-list enforcement —
// hooks use this to run user-authored commands with the same trust level as
// a shell alias.
type RunOptions struct {
	// Command is the shell source to parse and execute.
	Command string
	// Cwd is the working directory for the execution. Required: callers
	// must supply a non-empty value. Run does not silently fall back to
	// the Phosphor process cwd — hooks and the bash tool have different
	// notions of "default" and each owns that decision.
	Cwd string
	// Env is the full environment visible to the command. The caller is
	// responsible for inheriting from os.Environ() if that's desired.
	Env []string
	// Stdin is the command's standard input. nil is equivalent to an empty
	// input stream.
	Stdin io.Reader
	// Stdout receives the command's standard output. nil discards output.
	Stdout io.Writer
	// Stderr receives the command's standard error. nil discards output.
	Stderr io.Writer
	// BlockFuncs is an optional list of deny-list matchers applied before
	// each command reaches the exec layer. nil disables blocking entirely.
	BlockFuncs []BlockFunc
	// Workspace is the absolute workspace root used to confine fully expanded
	// argv. Empty disables post-expansion path confinement.
	Workspace string
	// ExtraTrustedRoots are additional absolute directories that argv may
	// reference alongside Workspace.
	ExtraTrustedRoots []string
	// DisableTempRoot opts out of trusting the OS temporary directory.
	DisableTempRoot bool
	// TermWidth is the terminal width in columns for PTY execution.
	// Zero uses a default of 200.
	TermWidth int
}

// Run parses and executes a shell command using the same mvdan.cc/sh
// interpreter stack that the stateful [Shell] type uses (builtins,
// optional block list, optional Go coreutils). It is safe to call
// concurrently from multiple goroutines: each call builds its own
// [interp.Runner] and shares no state with other callers or with any
// [Shell] instance.
//
// Errors returned from the command itself (non-zero exit, context
// cancellation, parse failures) follow the same conventions as
// [Shell.Exec]: inspect with [IsInterrupt] and [ExitCode].
func Run(ctx context.Context, opts RunOptions) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("command execution panic: %v", r)
		}
	}()

	if opts.Cwd == "" {
		return fmt.Errorf("shell.Run: Cwd is required")
	}

	stdout := opts.Stdout
	if stdout == nil {
		stdout = io.Discard
	}
	stderr := opts.Stderr
	if stderr == nil {
		stderr = io.Discard
	}

	line, err := syntax.NewParser().Parse(strings.NewReader(opts.Command), "")
	if err != nil {
		return fmt.Errorf("could not parse command: %w", err)
	}

	conf := newConfinement(opts.Workspace, opts.ExtraTrustedRoots, opts.DisableTempRoot)
	if err := checkProgram(conf, opts.Cwd, opts.Env, line); err != nil {
		return err
	}

	runner, err := newRunner(opts.Cwd, opts.Env, opts.Stdin, stdout, stderr, opts.BlockFuncs, conf)
	if err != nil {
		return fmt.Errorf("could not run command: %w", err)
	}

	return runner.Run(ctx, line)
}

// CaptureResult holds the combined output and exit code from a
// captured shell execution.
type CaptureResult struct {
	Output   string
	ExitCode int
}

// PersistFunc is a callback that persists a shell command result.
// Used by RunAndPersist to decouple execution from storage.
type PersistFunc func(command, output string, exitCode int) error

// RunAndPersist executes a shell command via PTY and optionally
// persists the result through the provided callback. This unifies
// the run-and-save pattern used by both AppWorkspace and Backend.
func RunAndPersist(ctx context.Context, opts RunOptions, persist PersistFunc) (CaptureResult, error) {
	result, err := RunAndCapturePTY(ctx, opts)
	if err != nil {
		return CaptureResult{}, err
	}

	if persist != nil {
		if persistErr := persist(opts.Command, result.Output, result.ExitCode); persistErr != nil {
			slog.Error("Failed to persist shell command output", "error", persistErr, "command", opts.Command)
		}
	}

	return result, nil
}

// RunAndCapture executes a shell command and returns its combined
// stdout/stderr output along with the exit code. It inherits the
// current process environment when opts.Env is nil.
func RunAndCapture(ctx context.Context, opts RunOptions) (CaptureResult, error) {
	if opts.Env == nil {
		opts.Env = os.Environ()
	}

	var stdout, stderr bytes.Buffer
	opts.Stdout = &stdout
	opts.Stderr = &stderr

	runErr := Run(ctx, opts)

	exitCode := 0
	if runErr != nil {
		exitCode = ExitCode(runErr)
	}

	output := stdout.String()
	if stderr.Len() > 0 {
		if output != "" {
			output += "\n"
		}
		output += stderr.String()
	}

	return CaptureResult{
		Output:   output,
		ExitCode: exitCode,
	}, nil
}

// ptyColorEnvVars force color output for programs running inside a
// PTY. These are only applied in RunAndCapturePTY, not in the plain
// Run/RunAndCapture paths where ANSI codes would be noise.
var ptyColorEnvVars = []string{
	"COLORTERM=truecolor",
	"CLICOLOR_FORCE=1",
	"FORCE_COLOR=1",
}

// RunAndCapturePTY executes a shell command through the mvdan.cc/sh
// interpreter with color-forcing environment variables set. Programs
// that respect FORCE_COLOR or CLICOLOR_FORCE (git, cargo, npm, eza,
// bat, ripgrep, etc.) will emit ANSI color sequences even without a
// real PTY. This approach is fully cross-platform — no /bin/sh or
// unix PTY required.
//
// The name is preserved for API compatibility; the PTY path has been
// replaced by the portable interpreter + env-var approach.
func RunAndCapturePTY(ctx context.Context, opts RunOptions) (CaptureResult, error) {
	if opts.Env == nil {
		opts.Env = os.Environ()
	}
	opts.Env = append(opts.Env, ptyColorEnvVars...)
	return RunAndCapture(ctx, opts)
}

// newRunner constructs an [interp.Runner] configured with the standard
// Phosphor handler stack. Shared by the stateless [Run] entrypoint and the
// stateful [Shell] so the two surfaces cannot drift.
func newRunner(cwd string, env []string, stdin io.Reader, stdout, stderr io.Writer, blockFuncs []BlockFunc, conf *pathguard.Confinement) (*interp.Runner, error) {
	env = withNonInteractiveEnv(env)
	return interp.New(
		interp.StdIO(stdin, stdout, stderr),
		interp.Interactive(false),
		interp.Env(expand.ListEnviron(env...)),
		interp.Dir(cwd),
		interp.OpenHandler(pathOpenHandler(conf)),
		execHandlerOption(blockFuncs, conf),
	)
}

// pathOpenHandler returns an [interp.OpenHandlerFunc] that bounds-checks every
// file the interpreter opens *itself* — redirection operands, sourced script
// bodies, here-documents — against the confinement before handing the open to
// mvdan's default handler.
//
// This is the authoritative runtime gate for interpreter-opened paths: unlike
// the static validator (command text only) or [pathConfinementHandler] (exec
// argv only), the OpenHandler sees the operand *after* the interpreter has fully
// expanded it, so $VAR, inline assignments, and $(...) results that never reach
// argv are all resolved to a concrete path here. Confinement is skipped when
// disabled so trusted surfaces (hooks, ExpandValue's nil-conf sub-runner) keep
// their unrestricted open behaviour.
func pathOpenHandler(conf *pathguard.Confinement) interp.OpenHandlerFunc {
	base := interp.DefaultOpenHandler()
	if conf == nil || conf.WorkspaceRoot == "" {
		return base
	}
	return func(ctx context.Context, path string, flag int, perm os.FileMode) (io.ReadWriteCloser, error) {
		cwd := interp.HandlerCtx(ctx).Dir
		absPath := path
		if !filepath.IsAbs(path) {
			absPath = filepath.Join(cwd, path)
		}
		if err := conf.Blocked([]string{"open", absPath}, cwd); err != nil {
			return nil, err
		}
		return base(ctx, path, flag, perm)
	}
}

// newConfinement builds the shared post-expansion policy. A missing workspace
// root intentionally disables confinement so trusted, caller-owned execution
// surfaces (notably hooks) remain usable.
func newConfinement(workspace string, extraRoots []string, disableTempRoot bool) *pathguard.Confinement {
	if workspace == "" {
		return nil
	}
	return &pathguard.Confinement{
		WorkspaceRoot:  workspace,
		TrustTempRoots: !disableTempRoot,
		ExtraRoots:     extraRoots,
	}
}

// execHandlerOption returns an interp.RunnerOption that installs the
// standard Phosphor middleware chain (builtins, script dispatch, block list)
// on top of a process-group-isolated base exec handler.
//
// We use interp.ExecHandler (singular) with a manually-built chain rather
// than interp.ExecHandlers because the latter always appends
// interp.DefaultExecHandler as the final handler, which lacks process group
// isolation. Without isolation, shells like zsh that set up job control
// when sourcing framework files can send SIGINT/SIGTERM to Phosphor's process
// group and crash the parent.
func execHandlerOption(blockFuncs []BlockFunc, conf *pathguard.Confinement) interp.RunnerOption {
	base := processGroupExecHandler(defaultKillTimeout)
	handler := base
	for _, mw := range slices.Backward(standardHandlers(blockFuncs, conf)) {
		handler = mw(handler)
	}
	return interp.ExecHandler(handler) //nolint:staticcheck // ExecHandlers always appends DefaultExecHandler which lacks process isolation.
}

// nonInteractiveEnvVars are forced on every shell execution to prevent
// commands from hanging on a nonexistent TTY. These are always applied
// regardless of the caller's environment because Phosphor shells are never
// interactive — preserving user preferences like EDITOR=nvim only causes
// hangs, not useful behavior.
var nonInteractiveEnvVars = []string{
	"TERM=xterm-256color",
	"GIT_EDITOR=false",
	"EDITOR=false",
	"VISUAL=false",
	"JJ_EDITOR=false",
	"JJ_PAGER=cat",
	"GIT_PAGER=cat",
	"PAGER=cat",
}

// withNonInteractiveEnv returns env with nonInteractiveEnvVars forced in,
// replacing any existing values for those keys. The returned slice is a
// new allocation safe to use concurrently with the input.
func withNonInteractiveEnv(env []string) []string {
	// Build a set of override keys for fast lookup.
	overrideKeys := make(map[string]bool, len(nonInteractiveEnvVars))
	for _, kv := range nonInteractiveEnvVars {
		if key, _, ok := strings.Cut(kv, "="); ok {
			overrideKeys[key] = true
		}
	}

	// Copy env, filtering out any keys we will override.
	result := make([]string, 0, len(env)+len(nonInteractiveEnvVars))
	for _, e := range env {
		if key, _, ok := strings.Cut(e, "="); ok && overrideKeys[key] {
			continue
		}
		result = append(result, e)
	}

	return append(result, nonInteractiveEnvVars...)
}

// standardHandlers returns the exec-handler middleware chain used by both
// [Run] and [Shell]. Order matters:
//  1. post-expansion path confinement — first, so it bounds-checks the fully
//     expanded argv of *every* command, including the Phosphor builtins
//     dispatched below (notably jq, whose positional file operands are opened
//     in-process); placing it after the builtin dispatch would let a builtin
//     short-circuit the chain and reach conf.Blocked never;
//  2. builtins (so Phosphor's in-process jq still wins over any PATH binary);
//  3. script dispatch (shebang / binary / shell-source for path-prefixed
//     argv[0], no-op for bare commands) — runs before the block list so
//     that deny rules see the already-resolved argv of anything the
//     script exec's rather than the outer path-prefixed wrapper;
//  4. block list;
//  5. optional Go coreutils (only when useGoCoreUtils is on).
func standardHandlers(blockFuncs []BlockFunc, conf *pathguard.Confinement) []func(next interp.ExecHandlerFunc) interp.ExecHandlerFunc {
	handlers := []func(next interp.ExecHandlerFunc) interp.ExecHandlerFunc{
		pathConfinementHandler(conf, blockFuncs),
		builtinHandler(conf),
		scriptDispatchHandler(blockFuncs, conf),
		blockHandler(blockFuncs),
	}
	if useGoCoreUtils {
		handlers = append(handlers, coreutils.ExecHandler)
	}
	return handlers
}

// builtinHandler returns middleware that dispatches recognized Phosphor
// builtins to their in-process Go implementations. Currently: jq. The active
// confinement is threaded through so the builtin can bound-check the files it
// opens itself, independently of its position in the middleware chain.
func builtinHandler(conf *pathguard.Confinement) func(next interp.ExecHandlerFunc) interp.ExecHandlerFunc {
	return func(next interp.ExecHandlerFunc) interp.ExecHandlerFunc {
		return func(ctx context.Context, args []string) error {
			if len(args) == 0 {
				return next(ctx, args)
			}
			switch args[0] {
			case "jq":
				hc := interp.HandlerCtx(ctx)
				return handleJQ(ctx, conf, hc.Dir, args, hc.Stdin, hc.Stdout, hc.Stderr)
			default:
				return next(ctx, args)
			}
		}
	}
}

// blockHandler returns middleware that rejects commands matched by any of
// the provided [BlockFunc]s before they reach the underlying exec path.
// A nil or empty blockFuncs slice is a no-op. When a command is blocked,
// an info log entry is emitted with the full argv so operators can debug
// accidentally-overbroad deny rules.
func blockHandler(blockFuncs []BlockFunc) func(next interp.ExecHandlerFunc) interp.ExecHandlerFunc {
	return func(next interp.ExecHandlerFunc) interp.ExecHandlerFunc {
		return func(ctx context.Context, args []string) error {
			if len(args) == 0 {
				return next(ctx, args)
			}
			for _, blockFunc := range blockFuncs {
				if blockFunc(args) {
					slog.InfoContext(ctx, "Command blocked by security policy",
						"command", args[0],
						"args", args,
					)
					return fmt.Errorf("command is not allowed for security reasons: %q", args[0])
				}
			}
			return next(ctx, args)
		}
	}
}
