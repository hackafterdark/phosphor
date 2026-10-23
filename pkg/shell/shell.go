// Package shell provides cross-platform shell execution capabilities.
//
// This package provides Shell instances for executing commands with their own
// working directory and environment. Each shell execution is independent.
//
// WINDOWS COMPATIBILITY:
// This implementation provides POSIX shell emulation (mvdan.cc/sh/v3) even on
// Windows. Commands should use forward slashes (/) as path separators to work
// correctly on all platforms.
package shell

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/charmbracelet/x/exp/slice"
	"github.com/hackafterdark/phosphor/internal/filepathext"
	"github.com/hackafterdark/phosphor/internal/pathguard"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/syntax"
)

// ShellType represents the type of shell to use
type ShellType int

const (
	ShellTypePOSIX ShellType = iota
	ShellTypeCmd
	ShellTypePowerShell
)

// PhosphorEnvMarkers returns a fresh slice of the environment variables that
// Phosphor unconditionally sets on every shell it spawns — both the interactive
// bash tool's [Shell] and the hook runner's [Run] calls. Tools that want to
// detect "am I being invoked by an AI agent?" can check any of these.
// Keeping them in one place guarantees the two shell surfaces cannot drift.
// A fresh slice is returned on every call so callers may append freely.
func PhosphorEnvMarkers() []string {
	return []string{
		"PHOSPHOR=1",
		"PHOSPHOR_AGENT=true",
		"AGENT=phosphor",
		"AI_AGENT=phosphor",
	}
}

// Logger interface for optional logging
type Logger interface {
	InfoPersist(msg string, keysAndValues ...any)
}

// noopLogger is a logger that does nothing
type noopLogger struct{}

func (noopLogger) InfoPersist(msg string, keysAndValues ...any) {}

// BlockFunc is a function that determines if a command should be blocked
type BlockFunc func(args []string) bool

// Shell provides cross-platform shell execution with optional state persistence
type Shell struct {
	env         []string
	cwd         string
	workspace   string // optional workspace root; if set, cd outside it is blocked
	mu          sync.Mutex
	logger      Logger
	blockFuncs  []BlockFunc
	confinement *pathguard.Confinement
	sandbox     *sandboxPolicy
}

// Options for creating a new shell
type Options struct {
	WorkingDir string
	Workspace  string // optional workspace root; if set, cd outside it is blocked
	Env        []string
	// AllowedEnv is an allowlist of environment variable names to keep when
	// filtering os.Environ(). When nil/empty a safe default is used. This
	// field is ignored if Env is non-nil (the caller is providing a fully
	// constructed environment).
	AllowedEnv []string
	Logger     Logger
	BlockFuncs []BlockFunc
	// ExtraTrustedRoots are additional absolute directories that fully expanded
	// argv may reference alongside Workspace.
	ExtraTrustedRoots []string
	// DisableTempRoot opts out of trusting the OS temporary directory.
	DisableTempRoot bool
	// ProxyEnv carries additional environment entries (for example the loopback
	// egress broker's HTTP_PROXY/HTTPS_PROXY and proxy-auth token) that are
	// appended to the child environment *after* allowlist filtering, so an
	// operator's allowed_env list cannot accidentally drop them and a credential-
	// credential-named variable still cannot leak through the allowlist. It is
	// nil in a normal session; only the opt-in egress-isolation tier populates it.
	ProxyEnv []string
	// SandboxMode selects kernel-enforced sandboxing of child processes:
	// "workspace" confines the filesystem kernel-side (degrading to in-process
	// guards with a visible warning when unavailable), "strict" refuses to exec
	// when enforcement cannot attach. Empty or "off" keeps today's behavior.
	SandboxMode string
}

// NewShell creates a new shell instance with the given options
func NewShell(opts *Options) *Shell {
	if opts == nil {
		opts = &Options{}
	}

	cwd := opts.WorkingDir
	if cwd == "" {
		cwd, _ = os.Getwd()
	}

	env := opts.Env
	if env == nil {
		allowlist := buildAllowlist(opts.AllowedEnv)
		if len(allowlist) == 0 {
			allowlist = buildAllowlist(SafeDefaultEnv())
		}
		env = filterEnv(os.Environ(), allowlist)
	}

	// Allow tools to detect execution by Phosphor.
	env = append(env, PhosphorEnvMarkers()...)

	// Append the caller-supplied proxy entries last, after allowlist filtering, so
	// the egress broker's proxy vars and its proxy-auth token survive the filter
	// (they are not operator allowlisted) yet still cannot smuggle a credential-
	// named variable past it. This is nil unless the opt-in egress tier is on.
	env = append(env, opts.ProxyEnv...)

	logger := opts.Logger
	if logger == nil {
		logger = noopLogger{}
	}

	return &Shell{
		cwd:         cwd,
		workspace:   opts.Workspace,
		env:         env,
		logger:      logger,
		blockFuncs:  opts.BlockFuncs,
		confinement: newConfinement(opts.Workspace, opts.ExtraTrustedRoots, opts.DisableTempRoot),
		sandbox:     newSandboxPolicy(opts.SandboxMode, opts.Workspace, opts.ExtraTrustedRoots),
	}
}

// SandboxDegraded reports whether kernel sandboxing was requested but could
// not attach on this host. Callers surface [SandboxDegradedWarning] once the
// degraded execution's output is reported.
func (s *Shell) SandboxDegraded() bool {
	return s.sandbox != nil && s.sandbox.degraded
}

// Exec executes a command in the shell
func (s *Shell) Exec(ctx context.Context, command string) (string, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.exec(ctx, command)
}

// ExecStream executes a command in the shell with streaming output to provided writers
func (s *Shell) ExecStream(ctx context.Context, command string, stdout, stderr io.Writer) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.execStream(ctx, command, stdout, stderr)
}

// GetWorkingDir returns the current working directory
func (s *Shell) GetWorkingDir() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cwd
}

// SetWorkingDir sets the working directory
func (s *Shell) SetWorkingDir(dir string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Verify the directory exists
	if _, err := os.Stat(dir); err != nil {
		return fmt.Errorf("directory does not exist: %w", err)
	}

	s.cwd = dir
	return nil
}

// GetEnv returns a copy of the environment variables
func (s *Shell) GetEnv() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	env := make([]string, len(s.env))
	copy(env, s.env)
	return env
}

// SetEnv sets an environment variable
func (s *Shell) SetEnv(key, value string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Update or add the environment variable
	keyPrefix := key + "="
	for i, env := range s.env {
		if strings.HasPrefix(env, keyPrefix) {
			s.env[i] = keyPrefix + value
			return
		}
	}
	s.env = append(s.env, keyPrefix+value)
}

// SetBlockFuncs sets the command block functions for the shell
func (s *Shell) SetBlockFuncs(blockFuncs []BlockFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.blockFuncs = blockFuncs
}

// wrapperCommands exec their wrapped program from argv instead of being the
// program themselves; the deny list must see through them.
var wrapperCommands = map[string]struct{}{
	"env": {}, "exec": {}, "command": {}, "time": {}, "timeout": {},
	"nice": {}, "stdbuf": {}, "nohup": {}, "xargs": {}, "busybox": {},
	"sudo": {}, "doas": {}, "pkexec": {},
}

// execPredicates are find predicates whose next argument is a program to exec.
var execPredicates = map[string]struct{}{
	"-exec": {}, "-execdir": {}, "-ok": {}, "-okdir": {},
}

// wrapperValueFlags lists, per wrapper, the flags that consume the following
// argument as a value. The wrapper scan must skip those values too, or the
// value is mistaken for the wrapped program and the real program is never
// checked (`env -u FOO curl` would surface candidate "FOO", not "curl").
var wrapperValueFlags = map[string]map[string]struct{}{
	"env":     {"-u": {}, "-C": {}, "-S": {}, "--unset": {}, "--chdir": {}, "--split-string": {}},
	"exec":    {"-a": {}},
	"sudo":    {"-u": {}, "-g": {}, "-h": {}, "-p": {}, "-C": {}, "-r": {}, "-T": {}, "-D": {}},
	"doas":    {"-u": {}},
	"xargs":   {"-I": {}, "-e": {}, "-E": {}, "-s": {}, "-S": {}, "-L": {}, "-P": {}, "-n": {}},
	"timeout": {"-k": {}, "-s": {}, "--kill-after": {}, "--signal": {}},
	"nice":    {"-n": {}},
	"stdbuf":  {"-o": {}, "-e": {}, "-i": {}},
}

// CommandsBlocker creates a BlockFunc that blocks commands matched against
// the deny list after normalizing argv[0] (basename, case, .exe suffix) and
// seeing through wrapper commands (env, xargs, timeout, busybox, ...) and
// find's -exec/-ok predicates.
func CommandsBlocker(cmds []string) BlockFunc {
	bannedSet := make(map[string]struct{})
	for _, cmd := range cmds {
		bannedSet[normalizeBlockCommand(cmd)] = struct{}{}
	}

	return func(args []string) bool {
		if len(args) == 0 {
			return false
		}
		for _, candidate := range executableCandidates(args) {
			if _, ok := bannedSet[candidate]; ok {
				return true
			}
		}
		return false
	}
}

// executableCandidates returns every program name a parsed argv may exec:
// the head command itself, the program behind wrapper commands, the
// unversioned form of a versioned interpreter (python3.11 → python), and any
// program behind find's -exec/-ok predicates.
func executableCandidates(args []string) []string {
	var out []string
	rest := args
	for depth := 0; depth < 8 && len(rest) > 0; depth++ {
		head := normalizeBlockCommand(rest[0])
		out = append(out, head)
		if base := versionedBaseName(head); base != "" {
			out = append(out, base)
		}
		if _, isWrapper := wrapperCommands[head]; !isWrapper {
			break
		}
		rest = wrappedProgram(rest[1:], head)
	}
	for i, arg := range args {
		if _, ok := execPredicates[strings.ToLower(arg)]; ok && i+1 < len(args) {
			c := normalizeBlockCommand(args[i+1])
			out = append(out, c)
			if base := versionedBaseName(c); base != "" {
				out = append(out, base)
			}
		}
	}
	return out
}

// wrappedProgram skips wrapper flags, variable assignments, and timing or
// priority values to find the wrapped program in argv. Flags listed in
// wrapperValueFlags for the given wrapper also consume their value.
func wrappedProgram(args []string, wrapper string) []string {
	valueFlags := wrapperValueFlags[wrapper]
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "":
			continue
		case strings.HasPrefix(arg, "-"):
			if _, takesValue := valueFlags[arg]; takesValue {
				i++ // the flag's value must not be mistaken for the program
			}
			continue
		case strings.Contains(arg, "="):
			continue
		case isTimingOrPriorityValue(arg):
			continue
		}
		return args[i:]
	}
	return nil
}

// isTimingOrPriorityValue reports whether arg is a timeout duration
// ("10", "10s", "1.5m") or a nice adjustment ("+5", "-n" values).
func isTimingOrPriorityValue(arg string) bool {
	s := strings.TrimPrefix(strings.TrimPrefix(arg, "+"), "-")
	if s == "" {
		return false
	}
	i := 0
	for i < len(s) && (s[i] >= '0' && s[i] <= '9' || s[i] == '.') {
		i++
	}
	if i == 0 {
		return false
	}
	return i == len(s) || (i+1 == len(s) && (s[i] == 's' || s[i] == 'm' || s[i] == 'h' || s[i] == 'd'))
}

// versionedBaseName returns the unversioned name of a versioned binary
// ("python3.11" → "python", "node20" → "node"), or "" when the argument is
// not of that shape.
func versionedBaseName(cmd string) string {
	i := strings.IndexAny(cmd, "0123456789")
	if i <= 1 {
		return ""
	}
	for j := i; j < len(cmd); j++ {
		if cmd[j] != '.' && (cmd[j] < '0' || cmd[j] > '9') {
			return ""
		}
	}
	return cmd[:i]
}

func normalizeBlockCommand(cmd string) string {
	cmd = strings.ToLower(strings.TrimSpace(cmd))
	cmd = strings.Replace(cmd, "\\", "/", -1)
	if idx := strings.LastIndexByte(cmd, '/'); idx >= 0 {
		cmd = cmd[idx+1:]
	}
	return strings.TrimSuffix(cmd, ".exe")
}

// ArgumentsBlocker creates a BlockFunc that blocks specific subcommand
func ArgumentsBlocker(cmd string, args []string, flags []string) BlockFunc {
	return func(parts []string) bool {
		if len(parts) == 0 || parts[0] != cmd {
			return false
		}

		argParts, flagParts := splitArgsFlags(parts[1:])
		if len(argParts) < len(args) || len(flagParts) < len(flags) {
			return false
		}

		argsMatch := slices.Equal(argParts[:len(args)], args)
		flagsMatch := slice.IsSubset(flags, flagParts)

		return argsMatch && flagsMatch
	}
}

// SelfExecBlocker blocks commands that would cause Phosphor to spawn itself,
// which would hijack the TUI and leave the user unable to interact.
func SelfExecBlocker() BlockFunc {
	return func(args []string) bool {
		if len(args) == 0 {
			return false
		}
		cmd := args[0]

		// Direct execution of the phosphor binary (./phosphor, ./phosphor.exe, phosphor)
		base := filepath.Base(cmd)
		if strings.EqualFold(base, "phosphor") {
			return true
		}

		// go run . — runs the current directory as a Go program (phosphor)
		if cmd == "go" && len(args) >= 3 && args[1] == "run" && args[2] == "." {
			return true
		}

		// go build . — builds the current directory (phosphor binary)
		if cmd == "go" && len(args) >= 3 && args[1] == "build" && args[2] == "." {
			return true
		}

		// go build -o phosphor — builds with output name matching phosphor
		if cmd == "go" && len(args) >= 4 && args[1] == "build" && args[2] == "-o" &&
			strings.EqualFold(args[3], "phosphor") {
			return true
		}

		return false
	}
}

// InlineExecutionWarnFunc returns a BlockFunc that never blocks but emits a
// Warn-level log entry and an OpenTelemetry span event whenever an interpreter
// or shell is invoked with an inline code execution flag (-c, -e, -r). It is
// used when the user has explicitly opted into allowing inline execution via
// tools.bash.allow_inline_execution. The warning appears in both slog logs and
// OpenTelemetry traces — the provided ctx carries the tool-call span so the
// log record and span event are attached for alerting and searchability.
func InlineExecutionWarnFunc(ctx context.Context) BlockFunc {
	return func(args []string) bool {
		if len(args) == 0 {
			return false
		}
		slog.WarnContext(ctx,
			"Interpreter shell code execution allowed by config",
			"command", args[0],
			"args", args,
		)
		if span := trace.SpanFromContext(ctx); span != nil {
			span.AddEvent("inline_execution_allowed",
				trace.WithAttributes(
					attribute.String("interpreter.command", args[0]),
					attribute.StringSlice("interpreter.args", args),
				),
			)
		}
		return false
	}
}

func splitArgsFlags(parts []string) (args []string, flags []string) {
	args = make([]string, 0, len(parts))
	flags = make([]string, 0, len(parts))
	for _, part := range parts {
		if strings.HasPrefix(part, "-") {
			// Extract flag name before '=' if present
			flag := part
			if before, _, ok := strings.Cut(part, "="); ok {
				flag = before
			}
			flags = append(flags, flag)
		} else {
			args = append(args, part)
		}
	}
	return args, flags
}

// newInterp creates a new interpreter with the current shell state. A nil
// stdin is equivalent to an empty input stream.
func (s *Shell) newInterp(stdin io.Reader, stdout, stderr io.Writer) (*interp.Runner, error) {
	return newRunner(s.cwd, s.env, stdin, stdout, stderr, s.blockFuncs, s.confinement, s.sandbox)
}

// updateShellFromRunner updates the shell from the interpreter after execution.
// If a workspace root is configured and the new cwd falls outside it, the
// working directory is reset to the original value to prevent escape.
func (s *Shell) updateShellFromRunner(runner *interp.Runner) {
	newCwd := runner.Dir
	if s.workspace != "" && !filepathext.IsInside(newCwd, s.workspace) {
		// Reset cwd to prevent escape from the workspace sandbox.
		newCwd = s.cwd
	}
	s.cwd = newCwd
	s.env = s.env[:0]
	for name, vr := range runner.Vars {
		if vr.Exported {
			s.env = append(s.env, name+"="+vr.Str)
		}
	}
}

// execCommon is the shared implementation for executing commands
func (s *Shell) execCommon(ctx context.Context, command string, stdout, stderr io.Writer) (err error) {
	var runner *interp.Runner
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("command execution panic: %v", r)
		}
		if runner != nil {
			s.updateShellFromRunner(runner)
		}
		s.logger.InfoPersist("command finished", "command", command, "err", err)
	}()

	line, err := syntax.NewParser().Parse(strings.NewReader(command), "")
	if err != nil {
		return fmt.Errorf("could not parse command: %w", err)
	}

	if err := checkProgram(s.confinement, s.cwd, s.env, line); err != nil {
		return err
	}

	runner, err = s.newInterp(nil, stdout, stderr)
	if err != nil {
		return fmt.Errorf("could not run command: %w", err)
	}

	err = runner.Run(ctx, line)
	return err
}

// exec executes commands using a cross-platform shell interpreter.
func (s *Shell) exec(ctx context.Context, command string) (string, string, error) {
	var stdout, stderr bytes.Buffer
	err := s.execCommon(ctx, command, &stdout, &stderr)
	return stdout.String(), stderr.String(), err
}

// execStream executes commands using POSIX shell emulation with streaming output
func (s *Shell) execStream(ctx context.Context, command string, stdout, stderr io.Writer) error {
	return s.execCommon(ctx, command, stdout, stderr)
}

// IsInterrupt checks if an error is due to interruption
func IsInterrupt(err error) bool {
	return errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded)
}

// ExitCode extracts the exit code from an error
func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	if exitErr, ok := errors.AsType[interp.ExitStatus](err); ok {
		return int(exitErr)
	}
	return 1
}
