package prompt

import (
	"cmp"
	"context"
	_ "embed"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"text/template"
	"time"

	"github.com/hackafterdark/phosphor/internal/filepathext"
	"github.com/hackafterdark/phosphor/internal/home"
	"github.com/hackafterdark/phosphor/pkg/config"
	"github.com/hackafterdark/phosphor/pkg/shell"
	"github.com/hackafterdark/phosphor/pkg/skills"
)

//go:embed templates/rules.md.tpl
var defaultRules []byte

//go:embed templates/style.md.tpl
var defaultStyle []byte

//go:embed templates/workflow.md.tpl
var defaultWorkflow []byte

//go:embed templates/interaction_gating.md.tpl
var defaultInteractionGating []byte

//go:embed templates/decision_making.md.tpl
var defaultDecisionMaking []byte

//go:embed templates/coding.md.tpl
var defaultCodingProtocol []byte

// Prompt represents a template-based prompt generator.
type Prompt struct {
	name                      string
	template                  string
	now                       func() time.Time
	platform                  string
	workingDir                string
	structuralSearchAvailable bool
}

type PromptDat struct {
	Provider                  string
	Model                     string
	PromptToolCalls           bool
	Config                    config.Config
	AgentConfig               *config.AgentConfig
	WorkingDir                string
	IsGitRepo                 bool
	Platform                  string
	Date                      string
	GitStatus                 string
	ContextFiles              []ContextFile
	GlobalContextFiles        []ContextFile
	AvailSkillXML             string
	StructuralSearchAvailable bool
	CriticalRules             string
	CommunicationStyle        string
	Workflow                  string
	InteractionGating         string
	DecisionMaking            string
	CodingProtocol            string
}

type ContextFile struct {
	Path    string
	Content string
}

// --- Section-specific views (least-privilege for profile partials) ---

// RulesView is the data exposed to rules.md.tpl partials.
type RulesView struct {
	PromptToolCalls           bool
	IsGitRepo                 bool
	Platform                  string
	StructuralSearchAvailable bool
}

// StyleView is the data exposed to style.md.tpl partials.
// The embedded default contains no template directives, but this view
// allows users to write conditional style content if needed.
type StyleView struct {
	Provider string
	Model    string
	Platform string
	Date     string
}

// WorkflowView is the data exposed to workflow.md.tpl partials.
type WorkflowView struct {
	WorkingDir string
	IsGitRepo  bool
	GitStatus  string
	Platform   string
}

// InteractionGatingView is the data exposed to interaction_gating.md.tpl partials.
type InteractionGatingView struct {
	MaxTurns int
}

// DecisionMakingView is the data exposed to decision_making.md.tpl partials.
// The embedded default contains no template directives, but this view
// allows users to write conditional decision content if needed.
type DecisionMakingView struct {
	Provider string
	Model    string
}

// CodingProtocolView is the data exposed to coding.md.tpl partials.
type CodingProtocolView struct {
	Provider                  string
	Model                     string
	WorkingDir                string
	IsGitRepo                 bool
	GitStatus                 string
	Platform                  string
	StructuralSearchAvailable bool
}

type Option func(*Prompt)

func WithTimeFunc(fn func() time.Time) Option {
	return func(p *Prompt) {
		p.now = fn
	}
}

func WithPlatform(platform string) Option {
	return func(p *Prompt) {
		p.platform = platform
	}
}

func WithWorkingDir(workingDir string) Option {
	return func(p *Prompt) {
		p.workingDir = workingDir
	}
}

func WithStructuralSearchAvailable(available bool) Option {
	return func(p *Prompt) {
		p.structuralSearchAvailable = available
	}
}

func NewPrompt(name, promptTemplate string, opts ...Option) (*Prompt, error) {
	p := &Prompt{
		name:     name,
		template: promptTemplate,
		now:      time.Now,
	}
	for _, opt := range opts {
		opt(p)
	}
	return p, nil
}

func resolveProfileTemplate(profileName, templateFile, workingDir string, embeddedDefault []byte) (string, error) {
	if profileName == "" {
		profileName = "default"
	}

	// 1. Workspace override: <workspace>/.phosphor/profiles/<profile-name>/<file>.md.tpl
	workspacePath := filepath.Join(workingDir, ".phosphor", "profiles", profileName, templateFile)
	if _, err := os.Stat(workspacePath); err == nil {
		content, err := os.ReadFile(workspacePath)
		if err == nil {
			return string(content), nil
		}
	}

	// 2. Global override: ~/.phosphor/profiles/<profile-name>/<file>.md.tpl
	globalPath := filepath.Join(home.Dir(), ".phosphor", "profiles", profileName, templateFile)
	if _, err := os.Stat(globalPath); err == nil {
		content, err := os.ReadFile(globalPath)
		if err == nil {
			return string(content), nil
		}
	}

	// 3. Embedded fallback
	return string(embeddedDefault), nil
}

func (p *Prompt) renderProfileTemplate(profileName, templateFile, workingDir string, embeddedDefault []byte, data any) (string, error) {
	tmplStr, err := resolveProfileTemplate(profileName, templateFile, workingDir, embeddedDefault)
	if err != nil {
		return "", err
	}
	t, err := template.New(templateFile).Parse(tmplStr)
	if err != nil {
		return "", fmt.Errorf("parsing profile template %s: %w", templateFile, err)
	}
	var sb strings.Builder
	if err := t.Execute(&sb, data); err != nil {
		return "", fmt.Errorf("executing profile template %s: %w", templateFile, err)
	}
	trimmed := strings.TrimSpace(sb.String())
	// Magic word override: Omit section
	if strings.Contains(trimmed, "# DISABLE_SECTION") {
		return "", nil
	}
	return trimmed, nil
}

func (p *Prompt) Build(ctx context.Context, provider, model string, store *config.ConfigStore) (string, error) {
	d, err := p.promptData(ctx, provider, model, store)
	if err != nil {
		return "", err
	}

	profileName := store.ActiveProfile()
	workingDir := cmp.Or(p.workingDir, store.WorkingDir())

	rulesRendered, err := p.renderProfileTemplate(profileName, "rules.md.tpl", workingDir, defaultRules, RulesView{
		PromptToolCalls:           d.PromptToolCalls,
		IsGitRepo:                 d.IsGitRepo,
		Platform:                  d.Platform,
		StructuralSearchAvailable: d.StructuralSearchAvailable,
	})
	if err != nil {
		return "", err
	}
	styleRendered, err := p.renderProfileTemplate(profileName, "style.md.tpl", workingDir, defaultStyle, StyleView{
		Provider: d.Provider,
		Model:    d.Model,
		Platform: d.Platform,
		Date:     d.Date,
	})
	if err != nil {
		return "", err
	}
	workflowRendered, err := p.renderProfileTemplate(profileName, "workflow.md.tpl", workingDir, defaultWorkflow, WorkflowView{
		WorkingDir: d.WorkingDir,
		IsGitRepo:  d.IsGitRepo,
		GitStatus:  d.GitStatus,
		Platform:   d.Platform,
	})
	if err != nil {
		return "", err
	}

	var maxTurns int
	if d.AgentConfig != nil {
		maxTurns = d.AgentConfig.MaxTurns
	}
	interactionGatingRendered, err := p.renderProfileTemplate(profileName, "interaction_gating.md.tpl", workingDir, defaultInteractionGating, InteractionGatingView{MaxTurns: maxTurns})
	if err != nil {
		return "", err
	}
	decisionMakingRendered, err := p.renderProfileTemplate(profileName, "decision_making.md.tpl", workingDir, defaultDecisionMaking, DecisionMakingView{
		Provider: d.Provider,
		Model:    d.Model,
	})
	if err != nil {
		return "", err
	}
	codingProtocolRendered, err := p.renderProfileTemplate(profileName, "coding.md.tpl", workingDir, defaultCodingProtocol, CodingProtocolView{
		Provider:                  d.Provider,
		Model:                     d.Model,
		WorkingDir:                d.WorkingDir,
		IsGitRepo:                 d.IsGitRepo,
		GitStatus:                 d.GitStatus,
		Platform:                  d.Platform,
		StructuralSearchAvailable: d.StructuralSearchAvailable,
	})
	if err != nil {
		return "", err
	}

	d.CriticalRules = rulesRendered
	d.CommunicationStyle = styleRendered
	d.Workflow = workflowRendered
	d.InteractionGating = interactionGatingRendered
	d.DecisionMaking = decisionMakingRendered
	d.CodingProtocol = codingProtocolRendered

	mainTmpl, err := resolveProfileTemplate(profileName, p.name+".md.tpl", workingDir, []byte(p.template))
	if err != nil {
		return "", err
	}

	t, err := template.New(p.name).Parse(mainTmpl)
	if err != nil {
		return "", fmt.Errorf("parsing template: %w", err)
	}
	var sb strings.Builder
	if err := t.Execute(&sb, d); err != nil {
		return "", fmt.Errorf("executing template: %w", err)
	}

	return sb.String(), nil
}

func processFile(filePath string) *ContextFile {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return nil
	}
	return &ContextFile{
		Path:    filePath,
		Content: string(content),
	}
}

func processContextPath(p string, store *config.ConfigStore) []ContextFile {
	var contexts []ContextFile
	fullPath := filepathext.SmartJoin(store.WorkingDir(), p)
	info, err := os.Stat(fullPath)
	if err != nil {
		return contexts
	}
	if info.IsDir() {
		filepath.WalkDir(fullPath, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() {
				if result := processFile(path); result != nil {
					contexts = append(contexts, *result)
				}
			}
			return nil
		})
	} else {
		result := processFile(fullPath)
		if result != nil {
			contexts = append(contexts, *result)
		}
	}
	return contexts
}

// expandPath expands ~ and environment variables in file paths
func expandPath(path string, store *config.ConfigStore) string {
	path = home.Long(path)
	// Handle environment variable expansion using the same pattern as config
	if strings.HasPrefix(path, "$") {
		if expanded, err := store.Resolver().ResolveValue(path); err == nil {
			path = expanded
		}
	}

	return path
}

// loadContextFiles loads and deduplicates context files from a list of paths.
func loadContextFiles(paths []string, store *config.ConfigStore) map[string][]ContextFile {
	files := map[string][]ContextFile{}
	for _, pth := range paths {
		expanded := expandPath(pth, store)
		pathKey := strings.ToLower(expanded)
		if _, ok := files[pathKey]; ok {
			continue
		}
		files[pathKey] = processContextPath(expanded, store)
	}
	return files
}

func (p *Prompt) promptData(ctx context.Context, provider, model string, store *config.ConfigStore) (PromptDat, error) {
	workingDir := cmp.Or(p.workingDir, store.WorkingDir())
	platform := cmp.Or(p.platform, runtime.GOOS)

	cfg := store.Config()
	promptToolCalls := false
	if cfg.Providers != nil {
		if pc, ok := cfg.Providers.Get(provider); ok {
			promptToolCalls = pc.ToolCallFormat == config.ToolCallFormatXML
		}
	}
	contextFiles := loadContextFiles(cfg.Options.ContextPaths, store)
	globalContextFiles := loadContextFiles(cfg.Options.GlobalContextPaths, store)

	// Discover and load skills metadata.
	var availSkillXML string

	// Start with builtin skills.
	allSkills := skills.DiscoverBuiltin()
	builtinNames := make(map[string]bool, len(allSkills))
	for _, s := range allSkills {
		builtinNames[s.Name] = true
	}

	// Discover user skills from configured paths.
	if len(cfg.Options.SkillsPaths) > 0 {
		expandedPaths := make([]string, 0, len(cfg.Options.SkillsPaths))
		for _, pth := range cfg.Options.SkillsPaths {
			expandedPaths = append(expandedPaths, expandPath(pth, store))
		}
		for _, userSkill := range skills.Discover(expandedPaths) {
			if builtinNames[userSkill.Name] {
				slog.Warn("User skill overrides builtin skill", "name", userSkill.Name)
			}
			allSkills = append(allSkills, userSkill)
		}
	}

	// Deduplicate: user skills override builtins with the same name.
	allSkills = skills.Deduplicate(allSkills)

	// Filter out disabled skills.
	allSkills = skills.Filter(allSkills, cfg.Options.DisabledSkills)

	if len(allSkills) > 0 {
		availSkillXML = skills.ToPromptXML(allSkills)
	}

	isGit := isGitRepo(store.WorkingDir())
	agentCfg := cfg.Options.Agent
	if agentCfg == nil {
		agentCfg = &config.AgentConfig{}
	}
	if store.ActiveProfile() == "fiduciary" {
		cp := *agentCfg
		cp.EnableReflection = true
		agentCfg = &cp
	}
	data := PromptDat{
		Provider:                  provider,
		Model:                     model,
		PromptToolCalls:           promptToolCalls,
		Config:                    *cfg,
		AgentConfig:               agentCfg,
		WorkingDir:                filepath.ToSlash(workingDir),
		IsGitRepo:                 isGit,
		Platform:                  platform,
		Date:                      p.now().Format("1/2/2006"),
		AvailSkillXML:             availSkillXML,
		StructuralSearchAvailable: p.structuralSearchAvailable,
	}
	if isGit {
		var err error
		data.GitStatus, err = getGitStatus(ctx, store.WorkingDir())
		if err != nil {
			return PromptDat{}, err
		}
	}

	for _, files := range contextFiles {
		data.ContextFiles = append(data.ContextFiles, files...)
	}
	for _, files := range globalContextFiles {
		data.GlobalContextFiles = append(data.GlobalContextFiles, files...)
	}
	return data, nil
}

func isGitRepo(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil
}

func getGitStatus(ctx context.Context, dir string) (string, error) {
	sh := shell.NewShell(&shell.Options{
		WorkingDir: dir,
	})
	branch, err := getGitBranch(ctx, sh)
	if err != nil {
		return "", err
	}
	status, err := getGitStatusSummary(ctx, sh)
	if err != nil {
		return "", err
	}
	commits, err := getGitRecentCommits(ctx, sh)
	if err != nil {
		return "", err
	}
	return branch + status + commits, nil
}

func getGitBranch(ctx context.Context, sh *shell.Shell) (string, error) {
	out, _, err := sh.Exec(ctx, "git branch --show-current 2>/dev/null")
	if err != nil {
		return "", nil
	}
	out = strings.TrimSpace(out)
	if out == "" {
		return "", nil
	}
	return fmt.Sprintf("Current branch: %s\n", out), nil
}

func getGitStatusSummary(ctx context.Context, sh *shell.Shell) (string, error) {
	out, _, err := sh.Exec(ctx, "git status --short 2>/dev/null | head -20")
	if err != nil {
		return "", nil
	}
	out = strings.TrimSpace(out)
	if out == "" {
		return "Status: clean\n", nil
	}
	return fmt.Sprintf("Status:\n%s\n", out), nil
}

func getGitRecentCommits(ctx context.Context, sh *shell.Shell) (string, error) {
	out, _, err := sh.Exec(ctx, "git log --oneline -n 3 2>/dev/null")
	if err != nil || out == "" {
		return "", nil
	}
	out = strings.TrimSpace(out)
	return fmt.Sprintf("Recent commits:\n%s\n", out), nil
}

func (p *Prompt) Name() string {
	return p.name
}
