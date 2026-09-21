package tools

import (
	"context"
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"charm.land/fantasy"
	"github.com/go-git/go-git/v5/plumbing/format/gitignore"
	"github.com/hackafterdark/phosphor/internal/filepathext"
	"github.com/hackafterdark/phosphor/internal/fsext"
	"github.com/hackafterdark/phosphor/pkg/egress"
	"github.com/hackafterdark/phosphor/pkg/otel"
	"github.com/zricethezav/gitleaks/v8/report"
	"go.opentelemetry.io/otel/attribute"
)

const ScanSecretsToolName = "scan_secrets"

//go:embed scan_secrets.md
var scanSecretsDescriptionStr string

// scanSecretsBullet is the mask that replaces the middle of a secret so the
// value can never be reconstructed from the tool output.
const scanSecretsBullet = "••••••••"

const (
	// scanSecretsMaxFiles bounds how many files a single scan will read before
	// giving up, so an accidental scan of a huge tree cannot hang the agent.
	scanSecretsMaxFiles = 10000
	// scanSecretsMaxFindings bounds the number of reported findings.
	scanSecretsMaxFindings = 250
	// scanSecretsMaxLineChars truncates the redacted line preview.
	scanSecretsMaxLineChars = 300
	// scanSecretsMaxHistoryCommits bounds how many commits a history scan walks.
	scanSecretsMaxHistoryCommits = 500
)

type ScanSecretsParams struct {
	Path        string `json:"path,omitempty" description:"Relative path to a directory or specific file to scan. Defaults to the current working directory."`
	ScanHistory bool   `json:"scan_history,omitempty" description:"If true and inside a git repository, scan git commit history; otherwise scan working tree files. Defaults to false."`
}

type ScanSecretsResponseMetadata struct {
	NumberOfFindings int  `json:"number_of_findings"`
	FilesScanned     int  `json:"files_scanned"`
	ScanHistory      bool `json:"scan_history"`
}

// secretFinding is a single masked credential ready to be rendered into the
// report. It intentionally never carries the raw secret; the raw bytes live only
// in needles, which are used solely to redact text, never printed.
type secretFinding struct {
	ruleID      string
	description string
	path        string
	line        int
	masked      string
	preview     string
	needles     []string
}

func NewScanSecretsTool(workingDir string) fantasy.AgentTool {
	return fantasy.NewAgentTool(
		ScanSecretsToolName,
		scanSecretsDescriptionStr,
		func(ctx context.Context, params ScanSecretsParams, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
			ctx, span := otel.StartSpan(ctx, "execute_tool scan_secrets")
			defer span.End()
			span.SetAttributes(
				attribute.String("gen_ai.tool.name", ScanSecretsToolName),
				attribute.String("gen_ai.tool.call.id", call.ID),
				attribute.String("gen_ai.tool.call.arguments", call.Input),
			)

			absWorkingDir, err := filepath.Abs(workingDir)
			if err != nil {
				return fantasy.NewTextErrorResponse(fmt.Sprintf("error resolving working directory: %v", err)), nil
			}

			searchPath, err := filepathext.ResolveSearchPath(workingDir, params.Path)
			if err != nil {
				return fantasy.NewTextErrorResponse(fmt.Sprintf("error resolving scan path: %v", err)), nil
			}
			absSearchPath, err := filepath.Abs(searchPath)
			if err != nil {
				return fantasy.NewTextErrorResponse(fmt.Sprintf("error resolving scan path: %v", err)), nil
			}
			if !filepathext.IsInside(absSearchPath, absWorkingDir) {
				return fantasy.NewTextErrorResponse(fmt.Sprintf("Security violation: path %s is outside workspace", absSearchPath)), nil
			}
			if _, err := os.Stat(absSearchPath); err != nil {
				return fantasy.NewTextErrorResponse(fmt.Sprintf("path not found: %s", absSearchPath)), nil
			}

			detector, err := secretDetector()
			if err != nil {
				return fantasy.NewTextErrorResponse("secret detection engine is unavailable"), nil
			}

			ignores := loadGitleaksIgnores(absWorkingDir, absSearchPath)

			var (
				findings     []secretFinding
				filesScaned  int
				historyScaned bool
			)

			// History mode: scan the patch text of all commits reachable from HEAD.
			// When the path is not inside a git repository this falls back to the
			// working tree, matching the parameter contract.
			if params.ScanHistory {
				if patch, ok := gitHistoryPatch(ctx, absWorkingDir, absSearchPath); ok {
					historyScaned = true
					findings = scanPatchText(detector, patch)
					filesScaned = countDistinctPatchFiles(patch)
				}
			}

			// Working-tree mode (also the history fallback).
			if !historyScaned {
				fileFindings, scanned, err := scanWorkingTree(ctx, detector, absSearchPath, absWorkingDir, ignores)
				if err != nil {
					return fantasy.NewTextErrorResponse(fmt.Sprintf("failed to scan %s: %v", absSearchPath, err)), nil
				}
				findings = fileFindings
				filesScaned = scanned
			}

			reportText := renderScanReport(absSearchPath, absWorkingDir, findings, historyScaned)
			reportText = scrubResidualSecrets(reportText, findings)

			return fantasy.WithResponseMetadata(
				fantasy.NewTextResponse(reportText),
				ScanSecretsResponseMetadata{
					NumberOfFindings: len(findings),
					FilesScanned:     filesScaned,
					ScanHistory:      historyScaned,
				},
			), nil
		},
	)
}

// scanWorkingTree walks the target path gitignore-aware and returns a masked
// finding for every secret the detector surfaces, together with the number of
// files it actually inspected.
func scanWorkingTree(ctx context.Context, detector detectorAPI, searchPath, workingDir string, ignores gitleaksIgnores) ([]secretFinding, int, error) {
	walker := fsext.NewFastGlobWalker(searchPath)
	var findings []secretFinding
	seen := map[string]struct{}{}
	filesScaned := 0

	err := filepath.Walk(searchPath, func(path string, info os.FileInfo, err error) error {
		if err != nil || ctx.Err() != nil {
			return nil
		}
		if info.IsDir() {
			if path != searchPath && (walker.ShouldSkipDir(path) || ignores.ignored(path, true)) {
				return filepath.SkipDir
			}
			return nil
		}
		if filesScaned >= scanSecretsMaxFiles {
			return filepath.SkipAll
		}
		if walker.ShouldSkip(path) || ignores.ignored(path, false) || !isTextFile(path) {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		filesScaned++
		display := relativeDisplayPath(workingDir, path)
		for _, f := range detector.DetectString(neutraliseTokens(string(content))) {
			if addFindingAt(&findings, seen, display, f.StartLine, &f) {
				return filepath.SkipAll
			}
		}
		return nil
	})
	if err != nil {
		return nil, filesScaned, err
	}
	return findings, filesScaned, nil
}

// scanPatchText runs the detector over a unified-diff blob (git history) and
// attributes every finding to the file/line reconstructed from the diff header
// that precedes it.
func scanPatchText(detector detectorAPI, patch string) []secretFinding {
	var findings []secretFinding
	seen := map[string]struct{}{}
	lines := strings.Split(patch, "\n")
	for _, f := range detector.DetectString(neutraliseTokens(patch)) {
		if len(findings) >= scanSecretsMaxFindings {
			break
		}
		display := historyFileDisplay(lines, f.StartLine)
		addFindingAt(&findings, seen, display, f.StartLine, &f)
	}
	return findings
}

// detectorAPI is the subset of the gitleaks detector the scanner depends on;
// it exists so tests can inject a stub without building the real ruleset.
type detectorAPI interface {
	DetectString(content string) []report.Finding
}

// addFindingAt builds a masked, deduplicated finding from a raw detection and
// appends it to the report. It returns true when the per-report finding cap has
// been reached so the caller can stop early.
func addFindingAt(findings *[]secretFinding, seen map[string]struct{}, display string, line int, f *report.Finding) bool {
	if len(*findings) >= scanSecretsMaxFindings {
		return true
	}
	secret := secretDisplayValue(f)
	needles := nonEmptyStrings(f.Match, f.Secret)
	if secret == "" && len(needles) == 0 {
		return false
	}
	key := fmt.Sprintf("%s\x1f%s\x1f%d\x1f%s", display, f.RuleID, line, secret)
	if _, ok := seen[key]; ok {
		return false
	}
	seen[key] = struct{}{}

	sentinel := redactionSentinel(f.RuleID)
	description := strings.TrimSpace(f.Description)
	if description == "" {
		description = f.RuleID
	}
	*findings = append(*findings, secretFinding{
		ruleID:      f.RuleID,
		description: description,
		path:        display,
		line:        line,
		masked:      DefangSpecialTokens(maskSecret(secret)),
		preview:     redactLinePreview(f.Line, needles, sentinel),
		needles:     needles,
	})
	return false
}

// renderScanReport formats the findings into the user-facing report. A zero-
// finding scan returns the canonical "no secrets" sentence naming the scanned
// path.
func renderScanReport(searchPath, workingDir string, findings []secretFinding, history bool) string {
	shown := relativeDisplayPath(workingDir, searchPath)
	if shown == "" {
		shown = filepath.ToSlash(searchPath)
	}
	if len(findings) == 0 {
		if history {
			return fmt.Sprintf("No unignored secrets detected in git history of %s.", shown)
		}
		return fmt.Sprintf("No unignored secrets detected in %s.", shown)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Found %d potential secret(s):\n", len(findings))
	for i, f := range findings {
		fmt.Fprintf(&b, "%d. [%s] %s\n", i+1, f.ruleID, f.description)
		location := f.path
		if f.line > 0 {
			location = fmt.Sprintf("%s:%d", f.path, f.line)
		}
		fmt.Fprintf(&b, "   File: %s\n", location)
		fmt.Fprintf(&b, "   Masked: %s\n", f.masked)
		fmt.Fprintf(&b, "   Line: %s\n", f.preview)
	}
	return b.String()
}

// scrubResidualSecrets is the belt-and-suspenders pass that guarantees the
// invariant "the raw secret never appears in tool output". Every reported raw
// byte string (the full match and the captured secret) is replaced by its
// redaction sentinel across the whole rendered report, so even an unexpected
// echo through a rule description cannot leak the credential.
func scrubResidualSecrets(text string, findings []secretFinding) string {
	for _, f := range findings {
		for _, needle := range f.needles {
			if needle == "" || needle == f.masked {
				continue
			}
			text = strings.ReplaceAll(text, needle, redactionSentinel(f.ruleID))
		}
	}
	return text
}

// redactLinePreview turns a raw source line into a safe preview by replacing
// every secret byte string with the rule sentinel, defanging inference control
// tokens, and truncating.
func redactLinePreview(line string, needles []string, sentinel string) string {
	for _, needle := range needles {
		if needle != "" {
			line = strings.ReplaceAll(line, needle, sentinel)
		}
	}
	line = strings.TrimSpace(DefangSpecialTokens(line))
	if len(line) > scanSecretsMaxLineChars {
		line = line[:scanSecretsMaxLineChars] + "…"
	}
	return line
}

// maskSecret keeps only a low-entropy vendor prefix (e.g. "ghp_") and the final
// two characters, replacing everything between them with the mask so the value
// can never be reconstructed while still being recognisable.
func maskSecret(secret string) string {
	if secret == "" {
		return ""
	}
	runes := []rune(secret)
	prefix := secretPrefix(secret)
	prefixLen := len([]rune(prefix))
	const suffixLen = 2
	if prefixLen+suffixLen >= len(runes) {
		return prefix + scanSecretsBullet
	}
	return prefix + scanSecretsBullet + string(runes[len(runes)-suffixLen:])
}

// secretPrefix returns the leading vendor marker of a secret up to and
// including the first "_" or "-" separator (bounded), or a short literal prefix
// when the value has no separator. It never returns the whole value.
func secretPrefix(secret string) string {
	const maxSep = 12
	limit := len(secret)
	if limit > maxSep {
		limit = maxSep
	}
	for i := 0; i < limit; i++ {
		if c := secret[i]; c == '_' || c == '-' {
			return secret[:i+1]
		}
	}
	if len(secret) > 4 {
		return secret[:4]
	}
	return ""
}

// secretDisplayValue picks the credential bytes to mask, preferring the
// captured secret group and falling back to the full regex match.
func secretDisplayValue(f *report.Finding) string {
	if f.Secret != "" {
		return f.Secret
	}
	return f.Match
}

// neutraliseTokens strips the reversible tokens we issued plus sealed egress
// handles so our own inert markers are never mistaken for credentials, exactly
// as the write gate does before detecting.
func neutraliseTokens(content string) string {
	return egress.StripTokens(stripTokens(content))
}

func nonEmptyStrings(values ...string) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}

// relativeDisplayPath returns path relative to workingDir with forward slashes
// for a stable, workspace-relative report; it falls back to the raw path when
// they do not share a root.
func relativeDisplayPath(workingDir, path string) string {
	if path == "" || workingDir == "" {
		return filepath.ToSlash(path)
	}
	rel, err := filepath.Rel(workingDir, path)
	if err != nil || rel == "" || strings.HasPrefix(rel, "..") {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(rel)
}

// historyFileDisplay reconstructs the file a finding belongs to by scanning the
// diff backwards from the finding line for the nearest "+++ b/<path>" (or
// "diff --git a/<path>") header.
func historyFileDisplay(lines []string, startLine int) string {
	if startLine > len(lines) {
		startLine = len(lines)
	}
	for i := startLine - 1; i >= 0; i-- {
		line := lines[i]
		if rest, ok := strings.CutPrefix(line, "+++ b/"); ok {
			if name := strings.TrimSpace(rest); name != "" && name != "/dev/null" {
				return "git history: " + name
			}
			continue
		}
		if rest, ok := strings.CutPrefix(line, "diff --git a/"); ok {
			fields := strings.Fields(rest)
			if len(fields) > 0 {
				return "git history: " + fields[0]
			}
		}
	}
	return "git history"
}

func countDistinctPatchFiles(patch string) int {
	seen := map[string]struct{}{}
	for _, line := range strings.Split(patch, "\n") {
		if rest, ok := strings.CutPrefix(line, "+++ b/"); ok {
			if name := strings.TrimSpace(rest); name != "" && name != "/dev/null" {
				seen[name] = struct{}{}
			}
		}
	}
	return len(seen)
}

// gitleaksIgnores holds one matcher per discovered .gitleaksignore file, each
// scoped to the directory it was found in, so patterns are matched against the
// correct relative path.
type gitleaksIgnores struct {
	sources []ignoreSource
}

type ignoreSource struct {
	matcher gitignore.Matcher
	baseDir string
}

// loadGitleaksIgnores collects every .gitleaksignore file found in the given
// base directories (de-duplicated) and compiles each into a gitignore matcher.
func loadGitleaksIgnores(baseDirs ...string) gitleaksIgnores {
	var out gitleaksIgnores
	seen := map[string]struct{}{}
	for _, base := range baseDirs {
		file := filepath.Join(base, ".gitleaksignore")
		abs, err := filepath.Abs(file)
		if err != nil {
			continue
		}
		if _, ok := seen[abs]; ok {
			continue
		}
		seen[abs] = struct{}{}
		data, err := os.ReadFile(abs)
		if err != nil {
			continue
		}
		var patterns []gitignore.Pattern
		for line := range strings.SplitSeq(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			patterns = append(patterns, gitignore.ParsePattern(line, nil))
		}
		if len(patterns) == 0 {
			continue
		}
		out.sources = append(out.sources, ignoreSource{matcher: gitignore.NewMatcher(patterns), baseDir: abs})
	}
	return out
}

func (g gitleaksIgnores) ignored(path string, isDir bool) bool {
	for _, src := range g.sources {
		rel, err := filepath.Rel(filepath.Dir(src.baseDir), path)
		if err != nil {
			continue
		}
		if comps := ignorePathComponents(rel); len(comps) > 0 && src.matcher.Match(comps, isDir) {
			return true
		}
	}
	return false
}

func ignorePathComponents(rel string) []string {
	rel = filepath.ToSlash(rel)
	rel = strings.TrimSuffix(rel, "/")
	if rel == "" || rel == "." {
		return nil
	}
	out := make([]string, 0, 4)
	for _, part := range strings.Split(rel, "/") {
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

// gitHistoryPatch returns the unified-diff text of every commit reachable from
// HEAD (optionally scoped to a pathspec) and reports whether the path is inside
// a git repository at all, so the caller can fall back to a working-tree scan.
func gitHistoryPatch(ctx context.Context, workingDir, searchPath string) (string, bool) {
	if !isGitRepository(ctx, workingDir) {
		return "", false
	}
	toplevel, err := runGit(ctx, workingDir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", false
	}
	repoRoot := strings.TrimSpace(toplevel)

	args := []string{"log", "--patch", "--no-color", fmt.Sprintf("--max-count=%d", scanSecretsMaxHistoryCommits)}
	if spec := gitPathSpec(repoRoot, searchPath); spec != "" {
		args = append(args, "--", spec)
	}
	patch, err := runGit(ctx, repoRoot, args...)
	if err != nil {
		return "", false
	}
	return patch, true
}

func isGitRepository(ctx context.Context, dir string) bool {
	out, err := runGit(ctx, dir, "rev-parse", "--is-inside-work-tree")
	return err == nil && strings.TrimSpace(out) == "true"
}

func gitPathSpec(repoRoot, searchPath string) string {
	if searchPath == "" {
		return "."
	}
	rel, err := filepath.Rel(repoRoot, searchPath)
	if err != nil || rel == "" || strings.HasPrefix(rel, "..") {
		return "."
	}
	return filepath.ToSlash(rel)
}

func runGit(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}
