package tools

import (
	"bytes"
	"cmp"
	"context"
	_ "embed"
	"fmt"
	"html/template"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"charm.land/fantasy"
	"github.com/hackafterdark/phosphor/internal/filepathext"
	"github.com/hackafterdark/phosphor/internal/pathguard"
	"github.com/hackafterdark/phosphor/pkg/config"
	"github.com/hackafterdark/phosphor/pkg/egress"
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

var bannedNetworkCommands = []string{
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
}

var bannedNonNetworkCommands = filterBannedNetworkCommands(bannedCommands, bannedNetworkCommands)

func filterBannedNetworkCommands(cmds []string, networkCmds []string) []string {
	out := make([]string, 0, len(cmds))
	for _, cmd := range cmds {
		if !slices.Contains(networkCmds, cmd) {
			out = append(out, cmd)
		}
	}
	return out
}

func networkPolicyFromConfig(cfg config.ToolBash) shell.NetworkPolicy {
	if cfg.Network == nil {
		return shell.NetworkPolicy{}
	}
	return shell.NetworkPolicy{
		Enabled:         cfg.Network.Enabled,
		AllowedCommands: cfg.Network.AllowedCommands,
		HostAllowlist:   cfg.Network.HostAllowlist,
	}
}

func effectiveBannedCommands(cfg config.ToolBash) []string {
	var cmds []string

	for _, cmd := range bannedNonNetworkCommands {
		cmds = appendBannedCommand(cmds, cmd)
	}
	for _, cmd := range cfg.BannedCommands {
		cmds = appendBannedCommand(cmds, cmd)
	}

	if cfg.Network == nil || !cfg.Network.Enabled {
		for _, cmd := range bannedNetworkCommands {
			cmds = appendBannedCommand(cmds, cmd)
		}
		return cmds
	}

	if len(cfg.Network.AllowedCommands) == 0 {
		return cmds
	}

	allowed := make(map[string]struct{}, len(cfg.Network.AllowedCommands))
	for _, cmd := range cfg.Network.AllowedCommands {
		if c := normalizeBashCommand(cmd); c != "" {
			allowed[c] = struct{}{}
		}
	}
	for _, cmd := range bannedNetworkCommands {
		c := normalizeBashCommand(cmd)
		if c == "" {
			continue
		}
		if _, ok := allowed[c]; !ok {
			cmds = appendBannedCommand(cmds, cmd)
		}
	}
	return cmds
}

func appendBannedCommand(cmds []string, cmd string) []string {
	if cmd == "" || slices.Contains(cmds, cmd) {
		return cmds
	}
	return append(cmds, cmd)
}

func normalizeBashCommand(cmd string) string {
	cmd = strings.ToLower(strings.TrimSpace(cmd))
	cmd = strings.Replace(cmd, "\\", "/", -1)
	if idx := strings.LastIndexByte(cmd, '/'); idx >= 0 {
		cmd = cmd[idx+1:]
	}
	return strings.TrimSuffix(cmd, ".exe")
}

func bashDescription(workspaceRoot string, cfg config.ToolBash, attribution *config.Attribution, modelID string) string {
	bannedCommandsStr := strings.Join(effectiveBannedCommands(cfg), ", ")
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
	cmds := append(bannedNonNetworkCommands, cfg.BannedCommands...)
	funcs := []shell.BlockFunc{
		shell.CommandsBlocker(cmds),
		shell.NetworkBlocker(bannedNetworkCommands, networkPolicyFromConfig(cfg)),

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

func NewBashTool(permissions permission.Service, workingDir string, bashCfg config.ToolBash, attribution *config.Attribution, modelID string) fantasy.AgentTool {
	return fantasy.NewAgentTool(
		BashToolName,
		string(bashDescription(workingDir, bashCfg, attribution, modelID)),
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
			params.Command = pathguard.CorrectCommandPaths(params.Command, absWorkingDir)

			// Command Parser Guard: Validate file paths in I/O commands
			if err := pathguard.ValidateCommandPaths(params.Command, absWorkingDir); err != nil {
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
				bgShell, err := bgManager.Start(ctx, execWorkingDir, absWorkingDir, blockFuncs(ctx, bashCfg), params.Command, params.Description, shell.WithTrustedRoots(bashCfg.TrustedExtraRoots), shell.WithProxyEnv(egress.SubprocessProxyEnv()))
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

					stdout = formatOutput(stdout, stderr, execErr, params.Command)

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
			bgShell, err := bgManager.Start(ctx, execWorkingDir, absWorkingDir, blockFuncs(ctx, bashCfg), params.Command, params.Description, shell.WithTrustedRoots(bashCfg.TrustedExtraRoots), shell.WithProxyEnv(egress.SubprocessProxyEnv()))
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

				stdout = formatOutput(stdout, stderr, execErr, params.Command)

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
func formatOutput(stdout, stderr string, execErr error, command string) string {
	interrupted := shell.IsInterrupt(execErr)
	exitCode := shell.ExitCode(execErr)

	stdout = truncateOutput(stdout)
	stderr = truncateOutput(stderr)

	// Whole-value redaction (A) when the command argv referenced a sensitive path
	// (cat/source/less .env, credentials, keys): drop assignment values from the
	// raw command data before it is combined. The detector pass (B) still runs on
	// the whole result below, so an output the argv heuristic missed is caught there.
	stdout = redactSensitiveOutputForCommand(stdout, command)
	stderr = redactSensitiveOutputForCommand(stderr, command)

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

	// Drop self-labelled secret fields when the command emitted a bare JSON
	// document (a JSON-emitting CLI such as an SDK/credential helper), then scrub
	// any credential the value scanner can see. Output that is not a single JSON
	// value is left byte-for-byte intact by the key-drop pass, so ordinary logs are
	// untouched. Bash output is path-less, so it is scanned in full mode and
	// honours the read-path secrets toggle.
	stdout = RedactJSONForTool(stdout, "bash")
	return redactSecretsForTool(stdout, "", "bash")
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
