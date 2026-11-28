package tools

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"charm.land/fantasy"
	"github.com/hackafterdark/phosphor/pkg/config"
	"github.com/hackafterdark/phosphor/pkg/egress"
	"github.com/hackafterdark/phosphor/pkg/otel"
	"github.com/hackafterdark/phosphor/pkg/permission"
	"github.com/hackafterdark/phosphor/pkg/security/externalcontent"
	"go.opentelemetry.io/otel/attribute"
)

// GhToolName is the stable tool name used for permission requests and
// allowlists.
const GhToolName = "gh"

// The gh tool mediates GitHub operations host-side with the host's real
// credential, but the model is blind to secrets: the credential never
// appears in tool input or (redacted) output, and every call is pinned to
// the workspace repository via a host-injected --repo. Everything outside
// the strict subcommand/flag allowlists below is unreachable.

type GhParams struct {
	Subcommand string   `json:"subcommand" description:"The gh subcommand to run, e.g. 'pr view', 'pr create', 'issue comment' (subcommand and sub-subcommand as one string)"`
	Args       []string `json:"args,omitempty" description:"Flags and positional arguments for the subcommand, each as a separate array entry. --repo is injected by the tool and cannot be supplied. Never repeat the subcommand in args: for pr view with comments pass args [--comments] only."`
}

//go:embed gh.md
var ghDescription []byte

type ghSpec struct {
	boolFlags  []string
	valueFlags []string
	// positional is true when the command may take a single numeric
	// PR/issue number.
	positional bool
	tier       gitTier
}

var ghSpecs = map[string]ghSpec{
	"pr view": {
		boolFlags:  []string{"--comments"},
		valueFlags: []string{"--json"},
		positional: true,
		tier:       gitTierReadOnly,
	},
	"pr list": {
		valueFlags: []string{"--state", "--limit", "--json"},
		tier:       gitTierReadOnly,
	},
	"pr create": {
		boolFlags:  []string{"--draft", "--fill"},
		valueFlags: []string{"--title", "--body", "--base", "--label"},
		tier:       gitTierLocal,
	},
	"pr comment": {
		valueFlags: []string{"--body"},
		positional: true,
		tier:       gitTierLocal,
	},
	"pr checkout": {
		positional: true,
		tier:       gitTierGated,
	},
	"issue view": {
		boolFlags:  []string{"--comments"},
		valueFlags: []string{"--json"},
		positional: true,
		tier:       gitTierReadOnly,
	},
	"issue list": {
		valueFlags: []string{"--state", "--limit", "--json"},
		tier:       gitTierReadOnly,
	},
	"issue comment": {
		valueFlags: []string{"--body"},
		positional: true,
		tier:       gitTierLocal,
	},
	"release view": {
		boolFlags:  []string{"--latest"},
		valueFlags: []string{"--json"},
		tier:       gitTierReadOnly,
	},
}

var ghNumberRe = regexp.MustCompile(`^[0-9]{1,10}$`)

// ghNeutralizedEnv returns the environment for every gh invocation: the
// fully neutralized git environment (gh invokes git internally, e.g. for
// pr checkout) plus non-interactive hardening so gh never spawns a pager,
// editor, or browser and never blocks on a prompt. Host values for the
// interactive-behavior variables are stripped first so ours always win.
// When a token override is configured (tools.gh.token_env names an
// environment variable), its value is injected as GH_TOKEN and both
// GH_TOKEN and GITHUB_TOKEN are stripped from the host environment, so
// the override unambiguously selects the credential. The token value is
// never logged.
func ghNeutralizedEnv(ghCfg config.ToolGh) []string {
	gitEnv := gitNeutralizedEnv()
	hostile := map[string]bool{
		"GH_PAGER": true, "GH_PROMPT_DISABLED": true, "GH_EDITOR": true,
		"GH_BROWSER": true, "GH_SPINNER_DISABLED": true, "NO_COLOR": true,
	}
	override := ""
	if ghCfg.TokenEnv != "" {
		override = os.Getenv(ghCfg.TokenEnv)
		if override == "" {
			slog.Warn("gh token env override configured but empty; falling back to host authentication",
				"env", ghCfg.TokenEnv)
		}
	}
	if override != "" {
		hostile["GH_TOKEN"] = true
		hostile["GITHUB_TOKEN"] = true
	}
	env := make([]string, 0, len(gitEnv)+8)
	for _, kv := range gitEnv {
		key, _, _ := strings.Cut(kv, "=")
		if hostile[key] {
			continue
		}
		env = append(env, kv)
	}
	env = append(env,
		"GH_PAGER=cat",
		"GH_PROMPT_DISABLED=1",
		"GH_EDITOR=false",
		"GH_BROWSER=false",
		"NO_COLOR=1",
		"GH_SPINNER_DISABLED=1",
	)
	if override != "" {
		env = append(env, "GH_TOKEN="+override)
	}
	return env
}

// ghWriteSecretGate scans the content arguments (--title/--body) of gh
// write operations with the gitleaks ruleset and blocks the operation
// when a credential is detected. The error names the rule and finding
// count but never echoes the secret itself.
func ghWriteSecretGate(args []string, policy *Policy) error {
	d, err := policy.orDefault().detector()
	if err != nil {
		slog.Warn("Secret detector unavailable; skipping gh write gate", "error", err)
		return nil
	}
	for i := 0; i+1 < len(args); i++ {
		if args[i] != "--title" && args[i] != "--body" {
			continue
		}
		content := args[i+1]
		if content == "" {
			continue
		}
		findings := d.DetectString(egress.StripTokens(stripTokens(content)))
		if len(findings) == 0 {
			continue
		}
		f := findings[0]
		msg := fmt.Sprintf("Security violation: potential secret detected in %s content (rule %s", args[i], f.RuleID)
		if len(findings) > 1 {
			msg += fmt.Sprintf(", %d findings total", len(findings))
		}
		msg += "). The GitHub operation was blocked to keep the credential out of the transcript and GitHub."
		return errors.New(msg)
	}
	return nil
}

// ghExec runs gh host-side with the neutralized environment, pinned to
// dir. It returns combined output and the process exit code.
func ghExec(ctx context.Context, dir string, timeout time.Duration, ghCfg config.ToolGh, args ...string) (string, int, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gh", args...)
	cmd.Dir = dir
	cmd.Env = ghNeutralizedEnv(ghCfg)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	out := stdout.String()
	if stderr.Len() > 0 {
		if out != "" {
			out += "\n"
		}
		out += stderr.String()
	}
	code := 0
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			code = exitErr.ExitCode()
			err = nil
		}
	}
	return out, code, err
}

// githubRepoFromRemoteURL derives the owner/repo a GitHub credential may
// act on from the pinned origin remote URL. Non-github.com hosts (GHES)
// are rejected until GH_HOST handling exists; the tool fails closed.
func githubRepoFromRemoteURL(url string) (string, error) {
	url = strings.TrimSpace(url)
	// git@github.com:owner/repo.git
	if i := strings.Index(url, ":"); i >= 0 && strings.HasPrefix(url, "git@github.com:") {
		path := strings.TrimPrefix(url, "git@github.com:")
		return normalizeRepoPath(path)
	}
	// https://github.com/owner/repo.git or ssh://git@github.com/owner/repo.git
	for _, prefix := range []string{"https://github.com/", "ssh://git@github.com/"} {
		if strings.HasPrefix(url, prefix) {
			return normalizeRepoPath(strings.TrimPrefix(url, prefix))
		}
	}
	return "", fmt.Errorf("origin remote %q is not a github.com repository; the gh tool fails closed", url)
}

func normalizeRepoPath(path string) (string, error) {
	path = strings.TrimSuffix(path, ".git")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", fmt.Errorf("cannot derive owner/repo from remote path %q", path)
	}
	if !ownerRepoSegmentRe.MatchString(parts[0]) || !ownerRepoSegmentRe.MatchString(parts[1]) {
		return "", fmt.Errorf("invalid owner/repo derived from remote: %q", path)
	}
	return parts[0] + "/" + parts[1], nil
}

var ownerRepoSegmentRe = regexp.MustCompile(`^[A-Za-z0-9._-]{1,100}$`)

// validateGhInvocation validates subcommand and args against the strict
// allowlist and returns the argv tail (without the injected --repo).
func validateGhInvocation(sub string, args []string) (argv []string, tier gitTier, err error) {
	spec, ok := ghSpecs[sub]
	if !ok {
		return nil, 0, fmt.Errorf("subcommand %q is not allowed", sub)
	}
	boolFlags := make(map[string]bool, len(spec.boolFlags))
	for _, f := range spec.boolFlags {
		boolFlags[f] = true
	}
	valueFlags := make(map[string]bool, len(spec.valueFlags))
	for _, f := range spec.valueFlags {
		valueFlags[f] = true
	}

	argv = nil
	seenPositional := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "-") && a != "-" {
			switch {
			case boolFlags[a]:
				argv = append(argv, a)
			case valueFlags[a]:
				if i+1 >= len(args) {
					return nil, 0, fmt.Errorf("flag %q requires a value", a)
				}
				argv = append(argv, a, args[i+1])
				i++
			default:
				return nil, 0, fmt.Errorf("flag %q is not allowed for %s", a, sub)
			}
			continue
		}
		// Positional argument: at most one numeric PR/issue number.
		if !spec.positional || seenPositional {
			return nil, 0, fmt.Errorf("%s does not accept positional arguments", sub)
		}
		if !ghNumberRe.MatchString(a) {
			return nil, 0, fmt.Errorf("positional argument %q must be a numeric PR or issue number", a)
		}
		argv = append(argv, a)
		seenPositional = true
	}
	return argv, spec.tier, nil
}

// ghOutputForModel renders gh output for the model: secret redaction,
// control-token defanging, and an untrusted-content boundary. gh output
// carries arbitrary user-authored text (PR bodies, issue comments), so it
// is always wrapped.
func ghOutputForModel(out, source string, policy *Policy) string {
	out = redactSensitiveOutputForCommand(out, source, policy)
	out = redactSecretsForTool(out, "", GhToolName, policy)
	out = DefangSpecialTokens(out)
	return externalcontent.Wrap(out, source)
}

// ghWorkspaceRepo resolves the pinned github.com owner/repo for the
// workspace and the origin URL it was derived from, failing closed on
// uninitialized workspaces, missing origin remotes, unapproved pins, and
// non-github.com remotes.
func ghWorkspaceRepo(ctx context.Context, dir, pinned string, approve func(url string) (bool, error)) (repo, origin string, err error) {
	if !IsGitRepo(dir) {
		return "", "", errors.New("workspace is not a git repository; the gh tool is unavailable until one is initialized")
	}
	newPin, err := pinOriginRemote(ctx, dir, pinned, approve)
	if err != nil {
		return "", "", err
	}
	repo, err = githubRepoFromRemoteURL(newPin)
	if err != nil {
		return "", "", err
	}
	return repo, newPin, nil
}

func NewGhTool(permissions permission.Service, workingDir string, ghCfg config.ToolGh, policy *Policy) fantasy.AgentTool {
	absWorkingDir, err := filepath.Abs(workingDir)
	if err != nil {
		absWorkingDir = workingDir
	}
	// pinnedOrigin holds the approved origin remote URL for this tool
	// instance; every gh call resolves its --repo from it. An existing
	// origin is seeded at construction so routine operations never see a
	// redundant pin prompt; any later origin transition still requires
	// explicit user approval.
	var pinMu sync.Mutex
	var pinnedOrigin string
	if url, ok := seedPinnedOrigin(context.Background(), absWorkingDir); ok {
		pinnedOrigin = url
	}
	return fantasy.NewAgentTool(
		GhToolName,
		string(ghDescription),
		func(ctx context.Context, params GhParams, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
			ctx, span := otel.StartSpan(ctx, "execute_tool "+GhToolName)
			defer span.End()
			span.SetAttributes(
				attribute.String("gen_ai.tool.name", GhToolName),
				attribute.String("gen_ai.tool.call.id", call.ID),
				attribute.String("gen_ai.tool.call.arguments", call.Input),
			)

			argvTail, tier, err := validateGhInvocation(params.Subcommand, params.Args)
			if err != nil {
				return fantasy.NewTextErrorResponse(gitSteerError(err).Error()), nil
			}

			// Symmetric secret gate: block writes whose title or body
			// carries a credential before anything reaches GitHub.
			if err := ghWriteSecretGate(argvTail, policy); err != nil {
				return fantasy.NewTextErrorResponse(gitSteerError(err).Error()), nil
			}

			// Fail closed: the workspace must be an initialized
			// repository with an approved origin pin.
			pinMu.Lock()
			currentPin := pinnedOrigin
			pinMu.Unlock()
			repo, newPin, err := ghWorkspaceRepo(ctx, absWorkingDir, currentPin, func(url string) (bool, error) {
				sessionID := GetSessionFromContext(ctx)
				if sessionID == "" {
					return false, fmt.Errorf("session ID is required for pinning the repository remote")
				}
				return permissions.BypassShortcuts(ctx, permission.CreatePermissionRequest{
					SessionID:   sessionID,
					Path:        absWorkingDir,
					ToolCallID:  call.ID,
					ToolName:    GhToolName,
					Action:      "pin",
					Description: fmt.Sprintf("Pin repository remote origin to %s", url),
					Params:      GhParams(params),
				})
			})
			if err != nil {
				if errors.Is(err, errOriginPinDenied) {
					return NewPermissionDeniedResponse(), nil
				}
				return fantasy.NewTextErrorResponse(gitSteerError(err).Error()), nil
			}
			if newPin != currentPin {
				pinMu.Lock()
				pinnedOrigin = newPin
				pinMu.Unlock()
				span.SetAttributes(attribute.String("phosphor.gh.origin_pin", newPin))
			}

			// Host-injected repo pin: gh can only address the
			// workspace repository.
			argv := append([]string{}, strings.Fields(params.Subcommand)...)
			argv = append(argv, "--repo", repo)
			argv = append(argv, argvTail...)
			span.SetAttributes(
				attribute.String("phosphor.gh.subcommand", params.Subcommand),
				attribute.String("phosphor.gh.repo", repo),
				attribute.Int("phosphor.gh.tier", int(tier)),
			)

			slog.Info("GitHub tool invocation",
				"subcommand", params.Subcommand,
				"repo", repo,
				"tier", tier,
			)

			if tier != gitTierReadOnly {
				sessionID := GetSessionFromContext(ctx)
				if sessionID == "" {
					return fantasy.ToolResponse{}, fmt.Errorf("session ID is required for executing gh commands")
				}
				req := permission.CreatePermissionRequest{
					SessionID:   sessionID,
					Path:        absWorkingDir,
					ToolCallID:  call.ID,
					ToolName:    GhToolName,
					Action:      "execute",
					Description: fmt.Sprintf("Run gh %s (repo %s)", strings.Join(argv, " "), repo),
					Params:      GhParams(params),
				}
				var granted bool
				if tier == gitTierGated {
					granted, err = permissions.BypassShortcuts(ctx, req)
				} else {
					granted, err = permissions.Request(ctx, req)
				}
				if err != nil {
					return fantasy.ToolResponse{}, err
				}
				if !granted {
					return NewPermissionDeniedResponse(), nil
				}
			}

			timeout := 60 * time.Second
			if params.Subcommand == "pr checkout" {
				timeout = 5 * time.Minute
			}
			out, code, err := ghExec(ctx, absWorkingDir, timeout, ghCfg, argv...)
			if err != nil {
				return fantasy.ToolResponse{}, fmt.Errorf("failed to run gh: %w", err)
			}
			source := fmt.Sprintf("gh %s (repo %s)", params.Subcommand, repo)
			out = ghOutputForModel(out, source, policy)
			if len(out) > MaxOutputLength {
				out = out[:MaxOutputLength] + "\n... output truncated ..."
			}
			if code != 0 {
				return fantasy.NewTextErrorResponse(fmt.Sprintf("gh %s exited with code %d:\n%s", params.Subcommand, code, out)), nil
			}
			if strings.TrimSpace(out) == "" {
				return fantasy.NewTextResponse("no output"), nil
			}
			return fantasy.NewTextResponse(out), nil
		},
	)
}
