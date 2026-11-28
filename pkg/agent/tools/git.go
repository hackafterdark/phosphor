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
	"github.com/hackafterdark/phosphor/internal/filepathext"
	"github.com/hackafterdark/phosphor/pkg/config"
	"github.com/hackafterdark/phosphor/pkg/egress"
	"github.com/hackafterdark/phosphor/pkg/otel"
	"github.com/hackafterdark/phosphor/pkg/permission"
	gl "github.com/zricethezav/gitleaks/v8/detect"
	"github.com/zricethezav/gitleaks/v8/report"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// GitToolName is the stable tool name used for permission requests and
// allowlists.
const GitToolName = "git"

// The git tool mediates git host-side, pinned to the repository that
// contains the workspace, with hostile repo-local git config neutralized on
// every invocation (GitSpawn class: core.fsmonitor/core.hooksPath/
// core.editor/core.pager and friends can execute arbitrary shell code
// during otherwise-innocent commands). The model supplies structured argv
// only; no shell is involved, so the attack surface is bounded by the
// subcommand and flag allowlists below.

type GitParams struct {
	Subcommand string   `json:"subcommand" description:"The git subcommand to run (e.g. status, add, commit, push)"`
	Args       []string `json:"args,omitempty" description:"Flags and positional arguments for the subcommand, each as a separate array entry. No shell syntax is accepted. Never repeat the subcommand in args: for status with short output pass args [--short] only."`
}

//go:embed git.md
var gitDescription []byte

// gitTier classifies how a validated invocation must be approved.
type gitTier int

const (
	// gitTierReadOnly runs without a permission prompt.
	gitTierReadOnly gitTier = iota
	// gitTierLocal follows the normal permission policy (auto-approved by
	// yolo mode, allowlists, and session caches).
	gitTierLocal
	// gitTierGated always prompts via BypassShortcuts: yolo mode,
	// allowlists, and hook pre-approvals cannot grant it, and headless
	// runs are denied rather than prompted.
	gitTierGated
)

// gitArgKind describes how positional arguments are validated for a
// subcommand.
type gitArgKind int

const (
	gitArgNone       gitArgKind = iota
	gitArgRev                   // revisions only
	gitArgRevPath               // revisions; file paths only after a literal "--"
	gitArgPath                  // file paths only (tool inserts "--")
	gitArgRemoteRef             // pinned literal "origin" then refspecs
	gitArgBranchList            // listing (read-only) or one create name (local)
	gitArgTagList               // listing (read-only) or one create name (local)
	gitArgStash                 // custom: stash list/pop/apply sub-actions
	gitArgCheckout              // custom: -b <branch>, <branch>, or -- <paths>
	gitArgInit                  // custom: greenfield workspace initialization
)

type gitSpec struct {
	boolFlags  []string
	valueFlags []string
	kind       gitArgKind
	tier       gitTier
}

var gitSpecs = map[string]gitSpec{
	"status": {
		boolFlags: []string{"-s", "-u", "-b", "--short", "--porcelain", "--branch", "--ignored"},
		kind:      gitArgNone,
		tier:      gitTierReadOnly,
	},
	"log": {
		boolFlags:  []string{"--oneline", "--graph", "--stat", "--name-status", "--name-only", "-p", "-u", "--decorate"},
		valueFlags: []string{"-n", "--max-count", "--since", "--until", "--author", "--grep"},
		kind:       gitArgRevPath,
		tier:       gitTierReadOnly,
	},
	"diff": {
		boolFlags: []string{"--stat", "--name-status", "--name-only", "-p", "-u", "--cached", "--staged", "--word-diff", "--no-color"},
		kind:      gitArgRevPath,
		tier:      gitTierReadOnly,
	},
	"show": {
		boolFlags: []string{"--stat", "--name-status", "--name-only", "--no-color"},
		kind:      gitArgRev,
		tier:      gitTierReadOnly,
	},
	"branch": {
		boolFlags:  []string{"-a", "-r", "-v", "-l", "--list", "--show-current"},
		valueFlags: []string{"--merged", "--no-merged"},
		kind:       gitArgBranchList,
		tier:       gitTierReadOnly,
	},
	"remote": {
		boolFlags: []string{"-v"},
		kind:      gitArgNone,
		tier:      gitTierReadOnly,
	},
	"tag": {
		boolFlags: []string{"-l", "--list"},
		kind:      gitArgTagList,
		tier:      gitTierReadOnly,
	},
	"rev-parse": {
		boolFlags: []string{"--short", "--is-inside-work-tree", "--verify", "--quiet"},
		kind:      gitArgRev,
		tier:      gitTierReadOnly,
	},
	"add": {
		boolFlags: []string{"-u", "-A", "-f", "-n"},
		kind:      gitArgPath,
		tier:      gitTierLocal,
	},
	"commit": {
		boolFlags:  []string{"-a"},
		valueFlags: []string{"-m", "--message"},
		kind:       gitArgPath,
		tier:       gitTierLocal,
	},
	"stash": {
		kind: gitArgStash,
		tier: gitTierLocal,
	},
	"checkout": {
		kind: gitArgCheckout,
		tier: gitTierLocal,
	},
	"init": {
		valueFlags: []string{"-b", "--initial-branch"},
		kind:       gitArgInit,
		tier:       gitTierLocal,
	},
	"fetch": {
		boolFlags: []string{"--prune", "--tags", "--dry-run"},
		kind:      gitArgRemoteRef,
		tier:      gitTierLocal,
	},
	"push": {
		boolFlags: []string{"--dry-run", "--tags", "-u", "--set-upstream"},
		kind:      gitArgRemoteRef,
		tier:      gitTierLocal,
	},
	"pull": {
		boolFlags: []string{"--rebase", "--ff-only", "--no-rebase"},
		kind:      gitArgRemoteRef,
		tier:      gitTierGated,
	},
	"merge": {
		boolFlags:  []string{"--no-ff", "--no-edit", "--ff", "--abort", "--continue", "--skip"},
		valueFlags: []string{"-m", "--message"},
		kind:       gitArgRev,
		tier:       gitTierGated,
	},
	"rebase": {
		boolFlags: []string{"--abort", "--continue", "--skip"},
		kind:      gitArgRev,
		tier:      gitTierGated,
	},
	"reset": {
		boolFlags: []string{"--hard", "--soft", "--mixed", "--keep"},
		kind:      gitArgRev,
		tier:      gitTierGated,
	},
	"clean": {
		boolFlags: []string{"-n", "-f", "-d", "-q"},
		kind:      gitArgPath,
		tier:      gitTierGated,
	},
}

var gitBranchNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,254}$`)

// gitEnvDrop strips host environment variables that could repoint git at
// another repository or inject executable commands, so repo pinning and
// neutralization cannot be overridden from the ambient environment.
var gitEnvDrop = map[string]bool{
	"GIT_DIR": true, "GIT_WORK_TREE": true, "GIT_SSH_COMMAND": true,
	"GIT_SSH": true, "GIT_CONFIG": true, "GIT_CONFIG_GLOBAL": true,
	"GIT_CONFIG_SYSTEM": true, "GIT_INDEX_FILE": true,
	"GIT_OBJECT_DIRECTORY": true, "GIT_ALTERNATE_OBJECT_DIRECTORIES": true,
	"GIT_NAMESPACE": true,
}

// gitNeutralizedEnv returns the environment for every git invocation: the
// host environment minus repo-repointing and exec-injection variables, plus
// host-side neutralizing overrides applied via GIT_CONFIG_* (which take
// precedence over config files) and pager/editor/prompt suppression.
func gitNeutralizedEnv() []string {
	sshPath, err := exec.LookPath("ssh")
	if err != nil || sshPath == "" {
		sshPath = "ssh"
	}
	overrides := [][2]string{
		{"core.hooksPath", os.DevNull},
		{"core.fsmonitor", "false"},
		{"safe.bareRepository", "explicit"},
		{"core.sshCommand", sshPath},
	}
	env := make([]string, 0, len(os.Environ())+2*len(overrides)+4)
	for _, kv := range os.Environ() {
		key, _, _ := strings.Cut(kv, "=")
		if gitEnvDrop[key] || strings.HasPrefix(key, "GIT_CONFIG_") {
			continue
		}
		env = append(env, kv)
	}
	env = append(env, fmt.Sprintf("GIT_CONFIG_COUNT=%d", len(overrides)))
	for i, o := range overrides {
		env = append(env,
			fmt.Sprintf("GIT_CONFIG_KEY_%d=%s", i, o[0]),
			fmt.Sprintf("GIT_CONFIG_VALUE_%d=%s", i, o[1]),
		)
	}
	env = append(env,
		"GIT_PAGER=cat",
		"GIT_EDITOR=false",
		"GIT_TERMINAL_PROMPT=0",
		"GIT_MERGE_AUTOEDIT=no",
	)
	return env
}

// gitExec runs git host-side with the neutralized environment, pinned to
// dir. It returns combined output and the process exit code.
func gitExec(ctx context.Context, dir string, timeout time.Duration, args ...string) (string, int, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = gitNeutralizedEnv()
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

// IsGitRepo reports whether dir is inside a git work tree. Used at
// coordinator construction to decide whether to register the git tool.
func IsGitRepo(dir string) bool {
	out, code, err := gitExec(context.Background(), dir, 5*time.Second, "rev-parse", "--is-inside-work-tree")
	return err == nil && code == 0 && strings.TrimSpace(out) == "true"
}

// validateGitInvocation turns model-supplied subcommand+args into a
// validated argv (including the leading subcommand) and the required
// approval tier. Anything outside the strict allowlists is denied.
func validateGitInvocation(sub string, args []string, workingDir string) (argv []string, tier gitTier, err error) {
	spec, ok := gitSpecs[sub]
	if !ok {
		return nil, 0, fmt.Errorf("subcommand %q is not allowed", sub)
	}

	switch spec.kind {
	case gitArgStash:
		return validateStashArgs(sub, args)
	case gitArgCheckout:
		return validateCheckoutArgs(sub, args, workingDir)
	}

	boolFlags := make(map[string]bool, len(spec.boolFlags))
	for _, f := range spec.boolFlags {
		boolFlags[f] = true
	}
	valueFlags := make(map[string]bool, len(spec.valueFlags))
	for _, f := range spec.valueFlags {
		valueFlags[f] = true
	}

	argv = []string{sub}
	var pathArgs []string
	seenDashDash := false
	pastDashDash := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" && spec.kind == gitArgRevPath {
			if seenDashDash {
				return nil, 0, errors.New("only one `--` separator is allowed")
			}
			seenDashDash = true
			pastDashDash = true
			argv = append(argv, a)
			continue
		}
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
		// Positional argument.
		switch spec.kind {
		case gitArgNone, gitArgInit:
			return nil, 0, fmt.Errorf("%s does not accept positional arguments", sub)
		case gitArgRev:
			if err := validateGitRev(a); err != nil {
				return nil, 0, err
			}
			argv = append(argv, a)
		case gitArgRevPath:
			if pastDashDash {
				if err := validateGitPath(workingDir, a); err != nil {
					return nil, 0, err
				}
			} else {
				if err := validateGitRev(a); err != nil {
					return nil, 0, err
				}
			}
			argv = append(argv, a)
		case gitArgPath:
			if err := validateGitPath(workingDir, a); err != nil {
				return nil, 0, err
			}
			pathArgs = append(pathArgs, a)
		case gitArgRemoteRef:
			if err := validateGitRemoteArg(a); err != nil {
				return nil, 0, err
			}
			argv = append(argv, a)
		case gitArgBranchList:
			if err := validateGitBranch(a); err != nil {
				return nil, 0, err
			}
			argv = append(argv, a)
		case gitArgTagList:
			if err := validateGitBranch(a); err != nil {
				return nil, 0, err
			}
			argv = append(argv, a)
		}
	}

	// Repo pinning: network subcommands operate on the workspace
	// repository's origin remote; the remote name defaults to origin when
	// omitted and is rejected when it is anything else.
	if spec.kind == gitArgRemoteRef {
		hasPositional := false
		for _, a := range argv[1:] {
			if !strings.HasPrefix(a, "-") {
				hasPositional = true
				if a != "origin" {
					return nil, 0, fmt.Errorf("remote %q is not allowed; only `origin` is pinned", a)
				}
				break
			}
		}
		if !hasPositional {
			argv = append(argv, "origin")
		}
	}

	// Insert `--` before file paths so dash-prefixed filenames cannot be
	// parsed as flags.
	if len(pathArgs) > 0 {
		argv = append(argv, "--")
		argv = append(argv, pathArgs...)
	}

	tier = spec.tier
	// Branch/tag creation upgrades from the listing tier.
	if spec.kind == gitArgBranchList || spec.kind == gitArgTagList {
		for _, a := range argv[1:] {
			if !strings.HasPrefix(a, "-") && a != "--" {
				tier = gitTierLocal
				break
			}
		}
	}
	return argv, tier, nil
}

func validateGitRev(s string) error {
	if s == "" {
		return errors.New("empty argument")
	}
	if strings.HasPrefix(s, "-") {
		return fmt.Errorf("argument %q looks like a flag", s)
	}
	if strings.Contains(s, "://") || strings.HasPrefix(s, "git@") {
		return fmt.Errorf("remote URLs are not allowed (repo is pinned to origin): %q", s)
	}
	return nil
}

// validateGitRemoteArg validates remote names and refspecs: repository
// pinning rejects force prefixes, remote-branch deletions, and anything
// that resolves to a URL or local filesystem path in remote position.
func validateGitRemoteArg(s string) error {
	if err := validateGitRev(s); err != nil {
		return err
	}
	if strings.HasPrefix(s, "+") || strings.HasPrefix(s, ":") ||
		strings.HasPrefix(s, ".") || strings.HasPrefix(s, "/") || strings.HasPrefix(s, "~") {
		return fmt.Errorf("force prefixes, deletions, and local paths are not allowed in remote position: %q", s)
	}
	return nil
}

func validateGitPath(workingDir, p string) error {
	if p == "" {
		return errors.New("empty path")
	}
	if strings.HasPrefix(p, "-") {
		return fmt.Errorf("path %q looks like a flag", p)
	}
	abs := p
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(workingDir, p)
	}
	abs = filepath.Clean(abs)
	if !filepathext.IsInside(abs, workingDir) {
		return fmt.Errorf("Security violation: path %s is outside workspace", p)
	}
	return nil
}

func validateGitBranch(s string) error {
	if !gitBranchNameRe.MatchString(s) || strings.Contains(s, "..") || strings.Contains(s, "@{") {
		return fmt.Errorf("invalid branch or tag name: %q", s)
	}
	return nil
}

func validateStashArgs(sub string, args []string) (argv []string, tier gitTier, err error) {
	// git stash defaults to push (local tier). list is read-only;
	// pop/apply are local. drop/clear are not allowed.
	argv = []string{sub}
	if len(args) == 0 {
		return argv, gitTierLocal, nil
	}
	if strings.HasPrefix(args[0], "-") {
		return nil, 0, fmt.Errorf("flags are not allowed for %s", sub)
	}
	switch args[0] {
	case "list":
		return append(argv, args[0]), gitTierReadOnly, nil
	case "pop", "apply":
		return append(argv, args[0]), gitTierLocal, nil
	default:
		return nil, 0, fmt.Errorf("%s %q is not allowed (only list, pop, apply)", sub, args[0])
	}
}

func validateCheckoutArgs(sub string, args []string, workingDir string) (argv []string, tier gitTier, err error) {
	// checkout -b <branch> creates and switches (local tier).
	// checkout <branch> switches (gated: can clobber the working tree).
	// checkout -- <paths> restores paths (gated: destructive).
	argv = []string{sub}
	var sawB bool
	var branch string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-b":
			sawB = true
			argv = append(argv, a)
		case a == "--":
			if sawB {
				return nil, 0, errors.New("checkout -b does not accept file paths")
			}
			var paths []string
			for _, p := range args[i+1:] {
				if err := validateGitPath(workingDir, p); err != nil {
					return nil, 0, err
				}
				paths = append(paths, p)
			}
			argv = append(argv, "--")
			argv = append(argv, paths...)
			return argv, gitTierGated, nil
		case strings.HasPrefix(a, "-"):
			return nil, 0, fmt.Errorf("flag %q is not allowed for %s", a, sub)
		default:
			if sawB {
				if branch != "" {
					return nil, 0, errors.New("checkout -b accepts exactly one branch name")
				}
				if err := validateGitBranch(a); err != nil {
					return nil, 0, err
				}
				branch = a
				argv = append(argv, a)
				continue
			}
			// Bare checkout <branch>: branch switch, gated.
			if err := validateGitBranch(a); err != nil {
				return nil, 0, err
			}
			argv = append(argv, a)
			return argv, gitTierGated, nil
		}
	}
	if sawB {
		if branch == "" {
			return nil, 0, errors.New("checkout -b requires a branch name")
		}
		return argv, gitTierLocal, nil
	}
	return argv, gitTierGated, nil
}

// defaultBranch resolves the repository's default branch from
// refs/remotes/origin/HEAD; "" when it cannot be determined.
func defaultBranch(ctx context.Context, dir string) string {
	out, code, err := gitExec(ctx, dir, 10*time.Second, "symbolic-ref", "refs/remotes/origin/HEAD")
	if err != nil || code != 0 {
		return ""
	}
	ref := strings.TrimSpace(out)
	if i := strings.LastIndex(ref, "/"); i >= 0 {
		return ref[i+1:]
	}
	return ""
}

// gitPushGateTier decides whether a validated push stays on its
// validated tier or must escalate to the always-prompt gated tier.
// Default-branch pushes and workflow-touching pushes are always gated;
// feature-branch pushes stay unattended (YOLO/headless) only under the
// "feature_branches" push policy.
func gitPushGateTier(validated gitTier, branch, def string, touchesWorkflows bool, pushPolicy string) bool {
	if branch == def || touchesWorkflows {
		return true
	}
	return pushPolicy != config.GitPushPolicyFeatureBranches
}

// pushTargetBranch derives the branch a push operation would update.
func pushTargetBranch(ctx context.Context, dir string, argv []string) (string, error) {
	// argv: <subcommand> [origin] [refspec...]; positional tail after the
	// remote decides the target.
	var pos []string
	for _, a := range argv[1:] {
		if strings.HasPrefix(a, "-") || a == "origin" {
			continue
		}
		pos = append(pos, a)
	}
	if len(pos) > 0 {
		target := pos[len(pos)-1]
		if _, remote, ok := strings.Cut(target, ":"); ok {
			return remote, nil
		}
		return target, nil
	}
	// No refspec: the current branch decides.
	out, code, err := gitExec(ctx, dir, 10*time.Second, "symbolic-ref", "--short", "HEAD")
	if err != nil || code != 0 {
		return "", errors.New("detached HEAD: push requires an explicit branch or refspec")
	}
	return strings.TrimSpace(out), nil
}

// pushTouchesWorkflows reports whether commits destined for the remote
// include changes under .github/workflows/ (CI-secrets lateral movement).
func pushTouchesWorkflows(ctx context.Context, dir, def string) bool {
	if def == "" {
		return false
	}
	base := "origin/" + def
	if _, code, err := gitExec(ctx, dir, 10*time.Second, "rev-parse", "--verify", "--quiet", base); err != nil || code != 0 {
		base = def
		if _, code, err := gitExec(ctx, dir, 10*time.Second, "rev-parse", "--verify", "--quiet", base); err != nil || code != 0 {
			return false
		}
	}
	out, code, err := gitExec(ctx, dir, 10*time.Second, "diff", "--name-only", base+"...HEAD", "--", ".github/workflows")
	if err != nil || code != 0 {
		slog.Warn("Unable to verify workflow changes for push; requiring explicit approval", "error", err)
		return true
	}
	return strings.TrimSpace(out) != ""
}

// gitSecretGate scans outgoing content (staged diff for commits, outgoing
// commit diffs for pushes) with the gitleaks ruleset and blocks the
// operation when a credential is detected. The error names the rule and
// file but never echoes the secret itself, so a blocked commit cannot
// leak the credential into the transcript.
func gitSecretGate(ctx context.Context, dir string, policy *Policy, args ...string) error {
	diff, code, err := gitExec(ctx, dir, 30*time.Second, args...)
	if err != nil || code != 0 {
		slog.Warn("Unable to scan outgoing diff for secrets; proceeding", "error", err)
		return nil
	}
	if diff == "" {
		return nil
	}
	d, err := policy.orDefault().detector()
	if err != nil {
		slog.Warn("Secret detector unavailable; skipping commit/push secret gate", "error", err)
		return nil
	}
	// Scan only added lines, attributed per file. Feeding the raw diff
	// to gitleaks false-positives on commit metadata (40-char hex SHAs,
	// diff/index headers), so commit and diff headers are stripped and
	// only '+' lines are scanned. The per-file scan also makes every
	// finding attributable to the file that introduced it.
	findings := detectDiffSecrets(d, diff)
	if len(findings) == 0 {
		return nil
	}
	f := findings[0]
	msg := fmt.Sprintf("Security violation: potential secret detected (rule %s", f.RuleID)
	if f.file != "" {
		msg += fmt.Sprintf(", file %s", f.file)
	}
	if len(findings) > 1 {
		msg += fmt.Sprintf(", %d findings total", len(findings))
	}
	msg += "). The operation was blocked to keep the credential out of git history."
	return errors.New(msg)
}

// diffSecret bundles a gitleaks finding with the file whose diff it
// came from.
type diffSecret struct {
	report.Finding
	file string
}

// detectDiffSecrets scans added lines of a unified diff, file by file.
// Commit headers (`commit <sha>`), index/diff headers, hunk headers, and
// deletion/context lines are skipped; '+' lines are scanned with the
// leading '+' removed so the detector sees the content as it would exist
// in the file. The secret values themselves are never surfaced.
func detectDiffSecrets(d *gl.Detector, diff string) []diffSecret {
	var out []diffSecret
	file := ""
	var added []string
	flush := func() {
		if file == "" || len(added) == 0 {
			added = nil
			return
		}
		for _, f := range d.DetectString(egress.StripTokens(stripTokens(strings.Join(added, "\n")))) {
			out = append(out, diffSecret{Finding: f, file: file})
		}
		added = nil
	}
	for _, l := range strings.Split(diff, "\n") {
		switch {
		case strings.HasPrefix(l, "diff --git "):
			flush()
			_, b, ok := strings.Cut(l, " b/")
			if ok {
				file = b
			} else {
				file = ""
			}
		case strings.HasPrefix(l, "+") && !strings.HasPrefix(l, "+++"):
			added = append(added, strings.TrimPrefix(l, "+"))
		case strings.HasPrefix(l, "commit "),
			strings.HasPrefix(l, "index "),
			strings.HasPrefix(l, "--- "),
			strings.HasPrefix(l, "+++ "),
			strings.HasPrefix(l, "@@"),
			strings.HasPrefix(l, " "),
			strings.HasPrefix(l, "-"):
			// Commit/diff metadata and non-added lines: never scanned.
		}
	}
	flush()
	return out
}

// gitTraceTrailer returns the Phosphor-Trace trailer for the current
// context. It is empty unless OTel is actively configured (a no-op tracer
// yields an invalid, unsampled span context) and the trace is sampled.
func gitTraceTrailer(ctx context.Context) string {
	sc := trace.SpanContextFromContext(ctx)
	if !sc.IsValid() || !sc.IsSampled() {
		return ""
	}
	return "Phosphor-Trace: " + sc.TraceID().String()
}

// appendCommitTrailer appends the trailer to the last -m/--message value
// of a commit argv. Co-authored-by attribution is handled elsewhere and
// must not be duplicated here.
func appendCommitTrailer(argv []string, trailer string) []string {
	if trailer == "" {
		return argv
	}
	for i := len(argv) - 1; i >= 0; i-- {
		if argv[i] == "-m" || argv[i] == "--message" {
			if i+1 < len(argv) {
				argv[i+1] = argv[i+1] + "\n\n" + trailer
			}
			return argv
		}
	}
	return argv
}

// gitUnsupportedSteer explains, in the tool error itself, that the
// operation is outside the mediated tool's allowlist. Without this
// steering the model's default reaction is to retry the same command
// through bash; the steer turns that into an explicit, explained
// escalation request instead.
const gitUnsupportedSteer = " This operation is unsupported by the mediated tool; if the user needs it, explain why and request escalated bash (sandbox=\"escalate\") rather than retrying silently."

// gitPolicySteer makes clear that a security-policy rejection is final:
// escalating the same command through bash would bypass the guardrail
// that just fired, so it must not be attempted.
const gitPolicySteer = " Blocked by security policy. Do NOT retry this command via bash or escalation; resolve the issue or inform the user."

// gitSteerError annotates a mediated-tool rejection with the steering
// text matching its kind: unsupported operations get the escalation
// steer, security violations get the no-retry steer.
func gitSteerError(err error) error {
	if strings.HasPrefix(err.Error(), "Security violation:") {
		return errors.New(err.Error() + gitPolicySteer)
	}
	return errors.New(err.Error() + gitUnsupportedSteer)
}

// hasHead reports whether the repository has any commits.
func hasHead(ctx context.Context, dir string) bool {
	_, code, err := gitExec(ctx, dir, 10*time.Second, "rev-parse", "--verify", "--quiet", "HEAD")
	return err == nil && code == 0
}

// refExists reports whether a git revision reference resolves.
func refExists(ctx context.Context, dir, ref string) bool {
	_, code, err := gitExec(ctx, dir, 10*time.Second, "rev-parse", "--verify", "--quiet", ref)
	return err == nil && code == 0
}

func NewGitTool(permissions permission.Service, workingDir string, gitCfg config.ToolGit, policy *Policy) fantasy.AgentTool {
	absWorkingDir, err := filepath.Abs(workingDir)
	if err != nil {
		absWorkingDir = workingDir
	}
	// pinnedOrigin holds the approved origin remote URL for this tool
	// instance. An existing origin is seeded at construction so routine
	// operations in already-pinned repositories never see a redundant
	// pin prompt; any later origin transition (new URL after `git init`,
	// or a changed URL, e.g. after the agent edited .git/config) still
	// requires explicit user approval before remote operations run.
	var pinMu sync.Mutex
	var pinnedOrigin string
	if url, ok := seedPinnedOrigin(context.Background(), absWorkingDir); ok {
		pinnedOrigin = url
	}
	pushPolicy := gitCfg.GitPushPolicy()
	return fantasy.NewAgentTool(
		GitToolName,
		string(gitDescription),
		func(ctx context.Context, params GitParams, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
			ctx, span := otel.StartSpan(ctx, "execute_tool "+GitToolName)
			defer span.End()
			span.SetAttributes(
				attribute.String("gen_ai.tool.name", GitToolName),
				attribute.String("gen_ai.tool.call.id", call.ID),
				attribute.String("gen_ai.tool.call.arguments", call.Input),
			)

			argv, tier, err := validateGitInvocation(params.Subcommand, params.Args, absWorkingDir)
			if err != nil {
				return fantasy.NewTextErrorResponse(gitSteerError(err).Error()), nil
			}

			// Greenfield workspaces may init; everything else requires
			// an existing repository, and remote operations require the
			// user-configured origin pin.
			if err := gitWorkspacePrecondition(ctx, absWorkingDir, params.Subcommand); err != nil {
				return fantasy.NewTextErrorResponse(gitSteerError(err).Error()), nil
			}

			// Origin pin gate: remote operations fail closed without an
			// origin remote, and any new or changed origin URL (the agent
			// can edit .git/config with its file tools) requires explicit
			// user approval before the command runs.
			if params.Subcommand == "push" || params.Subcommand == "pull" || params.Subcommand == "fetch" {
				pinMu.Lock()
				currentPin := pinnedOrigin
				pinMu.Unlock()
				newPin, err := pinOriginRemote(ctx, absWorkingDir, currentPin, func(url string) (bool, error) {
					sessionID := GetSessionFromContext(ctx)
					if sessionID == "" {
						return false, fmt.Errorf("session ID is required for pinning the repository remote")
					}
					return permissions.BypassShortcuts(ctx, permission.CreatePermissionRequest{
						SessionID:   sessionID,
						Path:        absWorkingDir,
						ToolCallID:  call.ID,
						ToolName:    GitToolName,
						Action:      "pin",
						Description: fmt.Sprintf("Pin repository remote origin to %s", url),
						Params:      GitParams(params),
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
					span.SetAttributes(attribute.String("phosphor.git.origin_pin", newPin))
				}
			}

			// Commit secret gate: scan the content that would enter
			// history before the commit runs. Staged changes are always
			// scanned; -a and path commits additionally scan unstaged
			// tracked changes via the HEAD diff.
			if params.Subcommand == "commit" {
				gateArgs := []string{"diff", "--cached"}
				if commitIncludesUnstaged(params.Args) && hasHead(ctx, absWorkingDir) {
					gateArgs = []string{"diff", "HEAD"}
				}
				if err := gitSecretGate(ctx, absWorkingDir, policy, gateArgs...); err != nil {
					return fantasy.NewTextErrorResponse(gitSteerError(err).Error()), nil
				}
			}

			// Push policy: force flags are unreachable (allowlist);
			// default-branch pushes and workflow-touching pushes are
			// gated even in yolo mode.
			if params.Subcommand == "push" {
				branch, err := pushTargetBranch(ctx, absWorkingDir, argv)
				if err != nil {
					return fantasy.NewTextErrorResponse(err.Error()), nil
				}
				def := defaultBranch(ctx, absWorkingDir)
				if def == "" {
					def = "main"
					if _, code, err := gitExec(ctx, absWorkingDir, 10*time.Second, "rev-parse", "--verify", "--quiet", "master"); err == nil && code == 0 {
						def = "master"
					}
				}
				if gitPushGateTier(tier, branch, def, pushTouchesWorkflows(ctx, absWorkingDir, def), pushPolicy) {
					tier = gitTierGated
				}
				span.SetAttributes(attribute.String("phosphor.git.push_target", branch))

				// Push secret gate: scan the outgoing commits (per-commit
				// diffs, so secrets added and later removed still trip).
				gateArgs := []string{"log", "-p"}
				if base := pushDiffBase(ctx, absWorkingDir, branch, def); base != "" {
					gateArgs = append(gateArgs, base+"..HEAD")
				}
				if err := gitSecretGate(ctx, absWorkingDir, policy, gateArgs...); err != nil {
					return fantasy.NewTextErrorResponse(gitSteerError(err).Error()), nil
				}
			}
			span.SetAttributes(
				attribute.String("phosphor.git.subcommand", params.Subcommand),
				attribute.Int("phosphor.git.tier", int(tier)),
			)

			// Audit record for every mediated operation.
			slog.Info("Git tool invocation",
				"subcommand", params.Subcommand,
				"tier", tier,
				"repo", absWorkingDir,
			)

			if tier != gitTierReadOnly {
				sessionID := GetSessionFromContext(ctx)
				if sessionID == "" {
					return fantasy.ToolResponse{}, fmt.Errorf("session ID is required for executing git commands")
				}
				req := permission.CreatePermissionRequest{
					SessionID:   sessionID,
					Path:        absWorkingDir,
					ToolCallID:  call.ID,
					ToolName:    GitToolName,
					Action:      "execute",
					Description: fmt.Sprintf("Run git %s", strings.Join(argv, " ")),
					Params:      GitParams(params),
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
			switch params.Subcommand {
			case "push", "pull", "fetch":
				timeout = 5 * time.Minute
			}

			// Conditional OTel trace trailer: appended only when tracing
			// is actively configured and the trace is sampled.
			if params.Subcommand == "commit" {
				argv = appendCommitTrailer(argv, gitTraceTrailer(ctx))
			}
			out, code, err := gitExec(ctx, absWorkingDir, timeout, argv...)
			if err != nil {
				return fantasy.ToolResponse{}, fmt.Errorf("failed to run git: %w", err)
			}
			out = redactSensitiveOutputForCommand(out, "git "+strings.Join(argv, " "), policy)
			out = redactSecretsForTool(out, "", GitToolName, policy)
			if len(out) > MaxOutputLength {
				out = out[:MaxOutputLength] + "\n... output truncated ..."
			}
			if code != 0 {
				return fantasy.NewTextErrorResponse(fmt.Sprintf("git %s exited with code %d:\n%s", params.Subcommand, code, out)), nil
			}
			if strings.TrimSpace(out) == "" {
				return fantasy.NewTextResponse("no output"), nil
			}
			return fantasy.NewTextResponse(out), nil
		},
	)
}

// gitWorkspacePrecondition enforces the greenfield workspace rules:
// git init is only allowed in uninitialized workspaces (and strictly at
// the workspace root, since the tool's cwd is pinned); every other
// subcommand requires an existing repository. Remote operations have a
// separate pin gate (pinOriginRemote) because the agent can edit
// .git/config directly with its file tools.
func gitWorkspacePrecondition(ctx context.Context, dir, sub string) error {
	inRepo := IsGitRepo(dir)
	if sub == "init" {
		if inRepo {
			return errors.New("workspace is already a git repository; git init is only allowed in uninitialized workspaces")
		}
		return nil
	}
	if !inRepo {
		return errors.New("workspace is not a git repository; run git init first")
	}
	return nil
}

// errOriginPinDenied marks an explicit user denial of the repository pin.
var errOriginPinDenied = errors.New("origin pin denied")

// originURL returns the workspace repository's origin remote URL.
func originURL(ctx context.Context, dir string) (string, bool) {
	out, code, err := gitExec(ctx, dir, 10*time.Second, "remote", "get-url", "origin")
	if err != nil || code != 0 {
		return "", false
	}
	url := strings.TrimSpace(out)
	return url, url != ""
}

// seedPinnedOrigin returns the workspace's existing origin URL so the
// pin can be seeded at tool construction. No seed exists for
// non-repositories or repositories without an origin remote; those stay
// gated until the user configures one.
func seedPinnedOrigin(ctx context.Context, dir string) (string, bool) {
	if !IsGitRepo(dir) {
		return "", false
	}
	return originURL(ctx, dir)
}

// pinOriginRemote enforces the explicit repository pin: remote operations
// fail closed without an origin remote, and any transition of origin
// (unpinned -> populated, or a changed URL, e.g. after the agent edited
// .git/config) requires explicit user approval via approve. The approved
// URL becomes the pin for subsequent calls.
func pinOriginRemote(ctx context.Context, dir, pinned string, approve func(url string) (bool, error)) (string, error) {
	url, ok := originURL(ctx, dir)
	if !ok {
		return "", errors.New("remote operations require an origin remote; the user must configure one (the agent cannot modify remotes)")
	}
	if url == pinned {
		return pinned, nil
	}
	granted, err := approve(url)
	if err != nil {
		return "", err
	}
	if !granted {
		return "", errOriginPinDenied
	}
	return url, nil
}

// commitIncludesUnstaged reports whether a commit invocation would stage
// additional tracked changes (-a, or explicit path arguments).
func commitIncludesUnstaged(args []string) bool {
	for _, a := range args {
		if a == "-a" || (!strings.HasPrefix(a, "-") && a != "--") {
			return true
		}
	}
	return false
}

// pushDiffBase resolves the base reference for scanning outgoing commits:
// the target branch's upstream when known, else the default branch's
// upstream, else the default branch itself. "" means every commit is
// outgoing.
func pushDiffBase(ctx context.Context, dir, branch, def string) string {
	if refExists(ctx, dir, "origin/"+branch) {
		return "origin/" + branch
	}
	if refExists(ctx, dir, "origin/"+def) {
		return "origin/" + def
	}
	if refExists(ctx, dir, def) {
		return def
	}
	return ""
}
