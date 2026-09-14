package tools

import (
	"bytes"
	"cmp"
	"context"
	_ "embed"
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"charm.land/fantasy"
	"github.com/hackafterdark/phosphor/internal/filepathext"
	"github.com/hackafterdark/phosphor/pkg/config"
	"github.com/hackafterdark/phosphor/pkg/otel"
	"github.com/hackafterdark/phosphor/pkg/permission"
	"github.com/hackafterdark/phosphor/pkg/shell"
	"go.opentelemetry.io/otel/attribute"
)

type BashParams struct {
	Description         string `json:"description" description:"A brief description of what the command does, try to keep it under 30 characters or so"`
	Command             string `json:"command" description:"The command to execute"`
	WorkingDir          string `json:"working_dir,omitempty" description:"The working directory to execute the command in (defaults to current directory)"`
	RunInBackground     bool   `json:"run_in_background,omitempty" description:"Set to true (boolean) to run this command in the background. Use job_output to read the output later."`
	AutoBackgroundAfter int    `json:"auto_background_after,omitempty" description:"Seconds to wait before automatically moving the command to a background job (default: 60)"`
}

type BashPermissionsParams struct {
	Description         string `json:"description"`
	Command             string `json:"command"`
	WorkingDir          string `json:"working_dir"`
	RunInBackground     bool   `json:"run_in_background"`
	AutoBackgroundAfter int    `json:"auto_background_after"`
}

type BashResponseMetadata struct {
	StartTime        int64  `json:"start_time"`
	EndTime          int64  `json:"end_time"`
	Output           string `json:"output"`
	Description      string `json:"description"`
	WorkingDirectory string `json:"working_directory"`
	Background       bool   `json:"background,omitempty"`
	ShellID          string `json:"shell_id,omitempty"`
}

const (
	BashToolName = "bash"

	DefaultAutoBackgroundAfter = 60 // Commands taking longer automatically become background jobs
	MaxOutputLength            = 30000
	BashNoOutput               = "no output"
)

//go:embed bash.md.tpl
var bashDescriptionTmpl []byte

var bashDescriptionTpl = template.Must(
	template.New("bashDescription").
		Parse(string(bashDescriptionTmpl)),
)

type bashDescriptionData struct {
	BannedCommands  string
	MaxOutputLength int
	Attribution     config.Attribution
	ModelID         string
	RgAvailable     bool
	GhAvailable     bool
	WorkspaceRoot   string
}

var bannedCommands = []string{
	// Network/Download tools
	"alias",
	"aria2c",
	"axel",
	"chrome",
	"curl",
	"curlie",
	"firefox",
	"http-prompt",
	"httpie",
	"links",
	"lynx",
	"nc",
	"safari",
	"scp",
	"ssh",
	"telnet",
	"w3m",
	"wget",
	"xh",

	// System administration
	"doas",
	"su",
	"sudo",

	// Package managers
	"apk",
	"apt",
	"apt-cache",
	"apt-get",
	"dnf",
	"dpkg",
	"emerge",
	"home-manager",
	"makepkg",
	"opkg",
	"pacman",
	"paru",
	"pkg",
	"pkg_add",
	"pkg_delete",
	"portage",
	"rpm",
	"yay",
	"yum",
	"zypper",

	// System modification
	"at",
	"batch",
	"chkconfig",
	"crontab",
	"fdisk",
	"mkfs",
	"mount",
	"parted",
	"service",
	"systemctl",
	"umount",

	// Network configuration
	"firewall-cmd",
	"ifconfig",
	"ip",
	"iptables",
	"netstat",
	"pfctl",
	"route",
	"ufw",
}

func bashDescription(workspaceRoot string, attribution *config.Attribution, modelID string) string {
	bannedCommandsStr := strings.Join(bannedCommands, ", ")
	var attr config.Attribution
	if attribution != nil {
		attr = *attribution
	}
	var out bytes.Buffer
	if err := bashDescriptionTpl.Execute(&out, bashDescriptionData{
		BannedCommands:  bannedCommandsStr,
		MaxOutputLength: MaxOutputLength,
		Attribution:     attr,
		ModelID:         modelID,
		RgAvailable:     getRg() != "",
		GhAvailable:     ghAvailable,
		WorkspaceRoot:   workspaceRoot,
	}); err != nil {
		// this should never happen.
		panic("failed to execute bash description template: " + err.Error())
	}
	return out.String()
}

func blockFuncs(ctx context.Context, cfg config.ToolBash) []shell.BlockFunc {
	cmds := append(bannedCommands, cfg.BannedCommands...)
	funcs := []shell.BlockFunc{
		shell.CommandsBlocker(cmds),

		// System package managers
		shell.ArgumentsBlocker("apk", []string{"add"}, nil),
		shell.ArgumentsBlocker("apt", []string{"install"}, nil),
		shell.ArgumentsBlocker("apt-get", []string{"install"}, nil),
		shell.ArgumentsBlocker("dnf", []string{"install"}, nil),
		shell.ArgumentsBlocker("pacman", nil, []string{"-S"}),
		shell.ArgumentsBlocker("pkg", []string{"install"}, nil),
		shell.ArgumentsBlocker("yum", []string{"install"}, nil),
		shell.ArgumentsBlocker("zypper", []string{"install"}, nil),

		// Language-specific package managers
		shell.ArgumentsBlocker("brew", []string{"install"}, nil),
		shell.ArgumentsBlocker("cargo", []string{"install"}, nil),
		shell.ArgumentsBlocker("gem", []string{"install"}, nil),
		shell.ArgumentsBlocker("go", []string{"install"}, nil),
		shell.ArgumentsBlocker("npm", []string{"install"}, []string{"--global"}),
		shell.ArgumentsBlocker("npm", []string{"install"}, []string{"-g"}),
		shell.ArgumentsBlocker("pip", []string{"install"}, []string{"--user"}),
		shell.ArgumentsBlocker("pip3", []string{"install"}, []string{"--user"}),
		shell.ArgumentsBlocker("pnpm", []string{"add"}, []string{"--global"}),
		shell.ArgumentsBlocker("pnpm", []string{"add"}, []string{"-g"}),
		shell.ArgumentsBlocker("yarn", []string{"global", "add"}, nil),

		// Prevent Phosphor from spawning itself (hijacks TUI)
		shell.SelfExecBlocker(),

		// `go test -exec` can run arbitrary commands
		shell.ArgumentsBlocker("go", []string{"test"}, []string{"-exec"}),
	}

	// Interpreters and shells with inline code execution flags bypass every
	// shell-level defense (env filtering, command blocking, workspace bounds)
	// by executing arbitrary code in another runtime. These are only added
	// when AllowInlineExecution is false (the default). Normal script
	// invocation (python script.py, node build.js) is unaffected either way.
	if !cfg.AllowInlineExecution {
		funcs = append(funcs,
			shell.ArgumentsBlocker("python", nil, []string{"-c"}),
			shell.ArgumentsBlocker("python3", nil, []string{"-c"}),
			shell.ArgumentsBlocker("python2", nil, []string{"-c"}),
			shell.ArgumentsBlocker("node", nil, []string{"-e"}),
			shell.ArgumentsBlocker("perl", nil, []string{"-e"}),
			shell.ArgumentsBlocker("ruby", nil, []string{"-e"}),
			shell.ArgumentsBlocker("php", nil, []string{"-r"}),
			shell.ArgumentsBlocker("lua", nil, []string{"-e"}),

			// Shell sub-command execution. `bash -c '...'` spins up a new
			// shell that inherits our filtered env but could be used to
			// chain commands past the block list if the inner shell
			// resolves binaries differently. Blocking the -c flag forces
			// the agent to use our shell interpreter instead.
			shell.ArgumentsBlocker("bash", nil, []string{"-c"}),
			shell.ArgumentsBlocker("sh", nil, []string{"-c"}),
			shell.ArgumentsBlocker("zsh", nil, []string{"-c"}),
			shell.ArgumentsBlocker("ksh", nil, []string{"-c"}),
			shell.ArgumentsBlocker("dash", nil, []string{"-c"}),
		)
	} else {
		// When inline execution is permitted, attach a warn-level logging
		// BlockFunc so every interpreter/shell invocation emits a visible
		// audit record. The otel span for the bash tool call already
		// captures the full command; this adds a dedicated slog.Warn log
		// entry for alerting and log-based search.
		funcs = append(funcs, shell.InlineExecutionWarnFunc(ctx))
	}

	return funcs
}

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
func scanCommandTokens(command string) []commandToken {
	var tokens []commandToken
	i := 0
	for i < len(command) {
		if isShellDelimiter(command[i]) {
			i++
			continue
		}
		start := i
		var text []byte
		var quote byte
		var openQuote byte
		for i < len(command) {
			c := command[i]
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
// environment variable whose value lives outside the workspace. Such a token
// cannot be resolved statically because the shell expands it only at
// execution time, so it must be rejected rather than trusted.
func referencesOutsideEnvVar(token string) bool {
	if !strings.ContainsAny(token, "/\\") {
		return false
	}
	for _, m := range envVarRegexp.FindAllStringSubmatch(token, -1) {
		name := m[2] // $NAME or ${NAME}
		if name == "" {
			name = m[3] // %NAME%
		}
		if _, ok := outsideEnvVars[strings.ToLower(name)]; ok {
			return true
		}
	}
	return false
}

// resolveTokenBase maps a path token to an absolute path for the bounds check:
// a leading "~" is expanded to the user home when known, otherwise the token
// is treated as absolute (SmartIsAbs) or workspace-relative.
func resolveTokenBase(token, absWorkingDir, home string) string {
	s := filepath.ToSlash(token)
	if home != "" && (s == "~" || strings.HasPrefix(s, "~/")) {
		return filepath.Clean(filepath.Join(home, strings.TrimLeft(s[1:], "/")))
	}
	if filepathext.SmartIsAbs(token) {
		return filepath.Clean(token)
	}
	return filepath.Clean(filepath.Join(absWorkingDir, s))
}

// validateCommandPaths checks whether any path in the command escapes the
// workspace. Unlike the previous implementation it is no longer gated on the
// presence of a known I/O command name: every token that could refer to a
// location outside the workspace is resolved and bounds-checked. This closes
// the bypass where write/read vectors that are not in the I/O keyword list
// (cp, mv, tee, dd, shell redirection targets, …) skipped validation entirely.
// Tokens that cannot escape — remote URLs, import paths, package names and
// in-tree relative operands — are ignored.
func validateCommandPaths(command string, absWorkingDir string) error {
	// Skip cd commands entirely — they change directory, not access files.
	// The shell's workspace boundary enforcement (updateShellFromRunner)
	// already prevents cd from escaping the workspace.
	if isCDCommand(command) {
		return nil
	}

	home := ""
	if h, err := os.UserHomeDir(); err == nil {
		home = h
	}

	for _, tok := range scanCommandTokens(command) {
		path := tok.text
		if path == "" || isRemoteURL(path) {
			continue
		}

		// A path embedding a home/temp/system environment variable cannot be
		// resolved statically (the shell expands it at execution time) and its
		// targets live outside the workspace, so reject it outright.
		if referencesOutsideEnvVar(path) {
			return fmt.Errorf("Security violation: path %s is outside workspace", path)
		}

		if !isEscapablePathToken(path) {
			continue
		}

		absPath := resolveTokenBase(path, absWorkingDir, home)
		if !filepathext.IsInside(absPath, absWorkingDir) {
			return fmt.Errorf("Security violation: path %s is outside workspace", absPath)
		}
	}

	return nil
}

// isCDCommand reports whether the command is a cd/before command. These are
// skipped by validateCommandPaths because they change directory rather than
// access files, and the shell's workspace boundary enforcement already
// prevents them from escaping the workspace.
func isCDCommand(command string) bool {
	trimmed := strings.TrimSpace(command)
	lower := strings.ToLower(trimmed)
	return strings.HasPrefix(lower, "cd ") || strings.HasPrefix(lower, "cd\t") || lower == "cd"
}

func NewBashTool(permissions permission.Service, workingDir string, bashCfg config.ToolBash, attribution *config.Attribution, modelID string) fantasy.AgentTool {
	return fantasy.NewAgentTool(
		BashToolName,
		string(bashDescription(workingDir, attribution, modelID)),
		func(ctx context.Context, params BashParams, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
			ctx, span := otel.StartSpan(ctx, "execute_tool bash")
			defer span.End()
			span.SetAttributes(
				attribute.String("gen_ai.tool.name", BashToolName),
				attribute.String("gen_ai.tool.call.id", call.ID),
				attribute.String("gen_ai.tool.call.arguments", call.Input),
			)
			if bashCfg.AllowInlineExecution {
				span.SetAttributes(attribute.Bool("phosphor.security.inline_execution_allowed", true))
			}
			if params.Command == "" {
				return fantasy.NewTextErrorResponse("missing command"), nil
			}

			// Determine working directory
			execWorkingDir := cmp.Or(params.WorkingDir, workingDir)

			// Enforce workspace bounds on working directory
			absWorkingDir, err := filepath.Abs(workingDir)
			if err != nil {
				return fantasy.ToolResponse{}, fmt.Errorf("error resolving working directory: %w", err)
			}
			absExecDir, err := filepath.Abs(execWorkingDir)
			if err != nil {
				return fantasy.ToolResponse{}, fmt.Errorf("error resolving target working directory: %w", err)
			}
			if !filepathext.IsInside(absExecDir, absWorkingDir) {
				return fantasy.NewTextErrorResponse(fmt.Sprintf("Security violation: working directory %s is outside workspace", absExecDir)), nil
			}

			// Correct paths using heuristics in command before validating or executing
			params.Command = CorrectCommandPaths(params.Command, absWorkingDir)

			// Command Parser Guard: Validate file paths in I/O commands
			if err := validateCommandPaths(params.Command, absWorkingDir); err != nil {
				return fantasy.NewTextErrorResponse(err.Error()), nil
			}

			isSafeReadOnly := false
			cmdLower := strings.ToLower(params.Command)

			if !containsCommandChaining(params.Command) {
				for _, safe := range safeCommands {
					if strings.HasPrefix(cmdLower, safe) {
						if len(cmdLower) == len(safe) || cmdLower[len(safe)] == ' ' || cmdLower[len(safe)] == '-' {
							isSafeReadOnly = true
							break
						}
					}
				}
			}

			sessionID := GetSessionFromContext(ctx)
			if sessionID == "" {
				return fantasy.ToolResponse{}, fmt.Errorf("session ID is required for executing shell command")
			}
			if !isSafeReadOnly {
				p, err := permissions.Request(
					ctx,
					permission.CreatePermissionRequest{
						SessionID:   sessionID,
						Path:        execWorkingDir,
						ToolCallID:  call.ID,
						ToolName:    BashToolName,
						Action:      "execute",
						Description: fmt.Sprintf("Execute command: %s", params.Command),
						Params:      BashPermissionsParams(params),
					},
				)
				if err != nil {
					return fantasy.ToolResponse{}, err
				}
				if !p {
					return NewPermissionDeniedResponse(), nil
				}
			}

			// If explicitly requested as background, start immediately with detached context
			if params.RunInBackground {
				startTime := time.Now()
				bgManager := shell.GetBackgroundShellManager()
				bgManager.Cleanup()
				// Use background context so it continues after tool returns
				bgShell, err := bgManager.Start(ctx, execWorkingDir, absWorkingDir, blockFuncs(ctx, bashCfg), params.Command, params.Description)
				if err != nil {
					return fantasy.ToolResponse{}, fmt.Errorf("error starting background shell: %w", err)
				}

				// Wait a short time to detect fast failures (blocked commands, syntax errors, etc.)
				time.Sleep(1 * time.Second)
				stdout, stderr, done, execErr := bgShell.GetOutput()

				if done {
					// Command failed or completed very quickly
					bgManager.Remove(bgShell.ID)

					interrupted := shell.IsInterrupt(execErr)
					exitCode := shell.ExitCode(execErr)
					if exitCode == 0 && !interrupted && execErr != nil {
						return fantasy.ToolResponse{}, fmt.Errorf("[Job %s] error executing command: %w", bgShell.ID, execErr)
					}

					stdout = formatOutput(stdout, stderr, execErr)

					metadata := BashResponseMetadata{
						StartTime:        startTime.UnixMilli(),
						EndTime:          time.Now().UnixMilli(),
						Output:           stdout,
						Description:      params.Description,
						Background:       params.RunInBackground,
						WorkingDirectory: bgShell.WorkingDir,
					}
					if stdout == "" {
						return fantasy.WithResponseMetadata(fantasy.NewTextResponse(BashNoOutput), metadata), nil
					}
					stdout += fmt.Sprintf("\n\n<cwd>%s</cwd>", normalizeWorkingDir(bgShell.WorkingDir))
					return fantasy.WithResponseMetadata(fantasy.NewTextResponse(stdout), metadata), nil
				}

				// Still running after fast-failure check - return as background job
				metadata := BashResponseMetadata{
					StartTime:        startTime.UnixMilli(),
					EndTime:          time.Now().UnixMilli(),
					Description:      params.Description,
					WorkingDirectory: bgShell.WorkingDir,
					Background:       true,
					ShellID:          bgShell.ID,
				}
				response := fmt.Sprintf("Background shell started with ID: %s\n\nUse job_output tool to view output or job_kill to terminate.", bgShell.ID)
				return fantasy.WithResponseMetadata(fantasy.NewTextResponse(response), metadata), nil
			}

			// Start synchronous execution with auto-background support
			startTime := time.Now()

			// Start with detached context so it can survive if moved to background
			bgManager := shell.GetBackgroundShellManager()
			bgManager.Cleanup()
			bgShell, err := bgManager.Start(ctx, execWorkingDir, absWorkingDir, blockFuncs(ctx, bashCfg), params.Command, params.Description)
			if err != nil {
				return fantasy.ToolResponse{}, fmt.Errorf("error starting shell: %w", err)
			}

			// Wait for either completion, auto-background threshold, or context cancellation
			ticker := time.NewTicker(100 * time.Millisecond)
			defer ticker.Stop()

			autoBackgroundAfter := cmp.Or(params.AutoBackgroundAfter, DefaultAutoBackgroundAfter)
			autoBackgroundThreshold := time.Duration(autoBackgroundAfter) * time.Second
			timeout := time.After(autoBackgroundThreshold)

			var stdout, stderr string
			var done bool
			var execErr error

		waitLoop:
			for {
				select {
				case <-ticker.C:
					stdout, stderr, done, execErr = bgShell.GetOutput()
					if done {
						break waitLoop
					}
				case <-timeout:
					stdout, stderr, done, execErr = bgShell.GetOutput()
					break waitLoop
				case <-ctx.Done():
					// Incoming context was cancelled before we moved to background
					// Kill the shell and return error
					bgManager.Kill(bgShell.ID)
					return fantasy.ToolResponse{}, ctx.Err()
				}
			}

			if done {
				// Command completed within threshold - return synchronously
				// Remove from background manager since we're returning directly
				// Don't call Kill() as it cancels the context and corrupts the exit code
				bgManager.Remove(bgShell.ID)

				interrupted := shell.IsInterrupt(execErr)
				exitCode := shell.ExitCode(execErr)
				if exitCode == 0 && !interrupted && execErr != nil {
					return fantasy.ToolResponse{}, fmt.Errorf("[Job %s] error executing command: %w", bgShell.ID, execErr)
				}

				stdout = formatOutput(stdout, stderr, execErr)

				metadata := BashResponseMetadata{
					StartTime:        startTime.UnixMilli(),
					EndTime:          time.Now().UnixMilli(),
					Output:           stdout,
					Description:      params.Description,
					Background:       params.RunInBackground,
					WorkingDirectory: bgShell.WorkingDir,
				}
				if stdout == "" {
					return fantasy.WithResponseMetadata(fantasy.NewTextResponse(BashNoOutput), metadata), nil
				}
				stdout += fmt.Sprintf("\n\n<cwd>%s</cwd>", normalizeWorkingDir(bgShell.WorkingDir))
				return fantasy.WithResponseMetadata(fantasy.NewTextResponse(stdout), metadata), nil
			}

			// Still running - keep as background job
			metadata := BashResponseMetadata{
				StartTime:        startTime.UnixMilli(),
				EndTime:          time.Now().UnixMilli(),
				Description:      params.Description,
				WorkingDirectory: bgShell.WorkingDir,
				Background:       true,
				ShellID:          bgShell.ID,
			}
			response := fmt.Sprintf("Command is taking longer than expected and has been moved to background.\n\nBackground shell ID: %s\n\nUse job_output tool to view output or job_kill to terminate.", bgShell.ID)
			return fantasy.WithResponseMetadata(fantasy.NewTextResponse(response), metadata), nil
		},
	)
}

// formatOutput formats the output of a completed command with error handling
func formatOutput(stdout, stderr string, execErr error) string {
	interrupted := shell.IsInterrupt(execErr)
	exitCode := shell.ExitCode(execErr)

	stdout = truncateOutput(stdout)
	stderr = truncateOutput(stderr)

	errorMessage := stderr
	if errorMessage == "" && execErr != nil {
		errorMessage = execErr.Error()
	}

	if interrupted {
		if errorMessage != "" {
			errorMessage += "\n"
		}
		errorMessage += "Command was aborted before completion"
	} else if exitCode != 0 {
		if errorMessage != "" {
			errorMessage += "\n"
		}
		errorMessage += fmt.Sprintf("Exit code %d", exitCode)
	}

	hasBothOutputs := stdout != "" && stderr != ""

	if hasBothOutputs {
		stdout += "\n"
	}

	if errorMessage != "" {
		stdout += "\n" + errorMessage
	}

	return stdout
}

func TruncateOutput(content string) string {
	if len(content) <= MaxOutputLength {
		return content
	}

	halfLength := MaxOutputLength / 2
	start := content[:halfLength]
	end := content[len(content)-halfLength:]

	truncatedLinesCount := countLines(content[halfLength : len(content)-halfLength])
	return fmt.Sprintf("%s\n\n... [%d lines truncated] ...\n\n%s", start, truncatedLinesCount, end)
}

func truncateOutput(content string) string {
	return TruncateOutput(content)
}

func countLines(s string) int {
	if s == "" {
		return 0
	}
	return len(strings.Split(s, "\n"))
}

func normalizeWorkingDir(path string) string {
	return filepath.ToSlash(path)
}
