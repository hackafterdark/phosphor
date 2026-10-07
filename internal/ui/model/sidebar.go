package model

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/hackafterdark/phosphor/internal/ui/common"
	"github.com/hackafterdark/phosphor/internal/ui/logo"
	"github.com/hackafterdark/phosphor/internal/workspaceindex"
	"github.com/hackafterdark/phosphor/pkg/config"
)

// modelInfo renders the current model information including reasoning
// settings and context usage/cost for the sidebar.
func (m *UI) modelInfo(width int) string {
	model := m.selectedLargeModel()
	reasoningInfo := ""
	providerName := ""

	if model != nil {
		// Get provider name first
		providerConfig, ok := m.com.Config().Providers.Get(model.ModelCfg.Provider)
		if ok {
			providerName = providerConfig.Name

			// Only check reasoning if the model can reason or the user
			// explicitly set a reasoning effort (e.g. for local models
			// that aren't in the catalog).
			if model.CatwalkCfg.CanReason || model.ModelCfg.ReasoningEffort != "" {
				if len(model.CatwalkCfg.ReasoningLevels) > 0 || model.ModelCfg.ReasoningEffort != "" {
					reasoningEffort := cmp.Or(model.ModelCfg.ReasoningEffort, model.CatwalkCfg.DefaultReasoningEffort)
					reasoningInfo = fmt.Sprintf("Reasoning %s", common.FormatReasoningEffort(reasoningEffort))
				} else if model.ModelCfg.Think {
					reasoningInfo = "Thinking On"
				} else {
					reasoningInfo = "Thinking Off"
				}
			} else if model.ModelCfg.Think || model.ModelCfg.EnableThinking == "on" {
				reasoningInfo = "Thinking On"
			} else if model.ModelCfg.EnableThinking == "off" {
				reasoningInfo = "Thinking Off"
			}

			// Append active sampling overrides so the sidebar reflects
			// the current model settings.
			var sampling []string
			if model.ModelCfg.Temperature != nil {
				sampling = append(sampling, "Temp "+strconv.FormatFloat(*model.ModelCfg.Temperature, 'f', -1, 64))
			}
			if model.ModelCfg.TopP != nil {
				sampling = append(sampling, "TopP "+strconv.FormatFloat(*model.ModelCfg.TopP, 'f', -1, 64))
			}
			if model.ModelCfg.TopK != nil {
				sampling = append(sampling, "TopK "+strconv.FormatInt(*model.ModelCfg.TopK, 10))
			}
			if model.ModelCfg.MaxTokens > 0 {
				sampling = append(sampling, "Max "+strconv.FormatInt(model.ModelCfg.MaxTokens, 10))
			}
			if model.ModelCfg.MaxThinkingTokens != nil {
				sampling = append(sampling, "Think "+strconv.FormatInt(*model.ModelCfg.MaxThinkingTokens, 10))
			}
			if len(sampling) > 0 {
				extra := strings.Join(sampling, " · ")
				if reasoningInfo != "" {
					reasoningInfo += " · " + extra
				} else {
					reasoningInfo = extra
				}
			}
		}
	}

	var modelContext *common.ModelContextInfo
	if model != nil && m.session != nil {
		tokens := m.session.CurrentTokens
		if tokens == 0 {
			tokens = m.session.PromptTokens + m.session.CompletionTokens
		}
		contextWindow := model.CatwalkCfg.ContextWindow
		// Fall back to config lookup when the coordinator's model
		// has a zero context window (e.g. provider model list not
		// populated by catwalk scripts).
		if contextWindow == 0 {
			if cfgModel := m.com.Config().GetModel(model.ModelCfg.Provider, model.ModelCfg.Model); cfgModel != nil {
				contextWindow = cfgModel.ContextWindow
			}
		}
		modelContext = &common.ModelContextInfo{
			ContextUsed:    tokens,
			Cost:           m.session.Cost,
			ModelContext:   contextWindow,
			EstimatedUsage: m.session.EstimatedUsage,
		}
	}
	var modelName string
	if model != nil {
		modelName = model.CatwalkCfg.Name
		if modelName == "" {
			modelName = model.ModelCfg.Model
		}
	}
	return common.ModelInfo(m.com.Styles, modelName, providerName, reasoningInfo, modelContext, width, m.hyperCredits)
}

// sidebar renders the chat sidebar containing session title, working
// directory, model info, file list, LSP status, and MCP status.
func (m *UI) goalInfo(width int) string {
	if m.currentGoal == nil {
		return ""
	}
	t := m.com.Styles
	status := string(m.currentGoal.Status)
	header := t.Sidebar.SectionHeader.Render("GOAL (" + status + ")")
	objective := t.Sidebar.SessionTitle.
		Foreground(t.Sidebar.WorkingDir.GetForeground()).
		Width(width).
		Render(m.currentGoal.Objective)
	return lipgloss.JoinVertical(lipgloss.Left, header, objective)
}

// codebaseIndexInfo renders the workspace search index status for the sidebar.
func (m *UI) codebaseIndexInfo(width int) string {
	cfg := m.com.Config()
	wi := cfg.WorkspaceSearch.FullText
	if cfg.WorkspaceSearch == nil || wi == nil || !wi.Enabled {
		return ""
	}
	t := m.com.Styles

	var parts []string
	parts = append(parts, common.Section(t, "Workspace Search", width))
	parts = append(parts, "")

	var statusParts []string
	if m.symbolIndex != nil {
		// m.indexProgress is refreshed off the render path by
		// indexProgressTickCmd/indexProgressFetchedMsg; the draw path only
		// ever reads it.
		if m.indexProgress != nil {
			p := m.indexProgress
			switch p.Status {
			case workspaceindex.IndexStatusIndexing:
				line := "Building index… " + abbrevNumber(p.FilesIndexed) + "/" + abbrevNumber(p.TotalFiles)
				statusParts = append(statusParts, t.ModelInfo.Provider.Render(line))
				if p.CurrentFile != "" {
					statusParts = append(statusParts, t.ModelInfo.Provider.Render("  "+truncRunes(p.CurrentFile, max(4, width-4))))
				}
			case workspaceindex.IndexStatusError:
				line := "Index error — open Workspace Index"
				if !statusFits(t.ModelInfo.Provider, line, width) {
					line = "Index error — see Workspace Index"
				}
				if !statusFits(t.ModelInfo.Provider, line, width) {
					line = "Index error"
				}
				statusParts = append(statusParts, t.ModelInfo.Provider.Render(line))
			default:
				counts, hint := indexStatusLines(p, wi.AutoIndexEnabled(), width, t.ModelInfo.Provider, t.ModelInfo.Reasoning)
				for _, line := range counts {
					statusParts = append(statusParts, t.ModelInfo.Provider.Render(line))
				}
				for _, line := range hint {
					statusParts = append(statusParts, t.ModelInfo.Reasoning.Render(line))
				}
			}
		}
	}
	for _, line := range statusParts {
		parts = append(parts, line)
	}

	return lipgloss.JoinVertical(lipgloss.Left, lipgloss.JoinVertical(lipgloss.Left, parts...))
}

// indexProgressRefreshInterval is how often the workspace search widget
// re-reads the index. See [UI.indexProgressTickCmd].
const indexProgressRefreshInterval = 1 * time.Second

// indexProgressTickMsg fires on indexProgressRefreshInterval to trigger the
// next background index-progress read. It carries no data; it only wakes
// Update so it can snapshot m.symbolIndex (safe: done on the UI goroutine)
// and hand the read off to a one-shot tea.Cmd.
type indexProgressTickMsg struct{}

// indexProgressFetchedMsg carries the workspace index progress read by that
// one-shot tea.Cmd back to Update, which is the only place m.indexProgress
// is written.
type indexProgressFetchedMsg struct {
	progress *workspaceindex.IndexProgress
}

// indexProgressTickCmd schedules the next indexProgressTickMsg. It is
// self-rescheduling: the indexProgressTickMsg handler in Update appends
// another call to keep the cadence going for the life of the program.
func indexProgressTickCmd() tea.Cmd {
	return tea.Tick(indexProgressRefreshInterval, func(time.Time) tea.Msg {
		return indexProgressTickMsg{}
	})
}

// fetchIndexProgressCmd snapshots m.symbolIndex and returns a tea.Cmd that
// reads its progress off the UI goroutine. The returned command touches only
// the captured store pointer, never m itself, so it is safe to run
// concurrently with the next Update call: see internal/ui/AGENTS.md ("Never
// change the model state inside of a command").
func (m *UI) fetchIndexProgressCmd() tea.Cmd {
	idx := m.symbolIndex
	if idx == nil {
		return nil
	}
	return func() tea.Msg {
		progress, err := idx.GetProgress(context.Background())
		if err != nil {
			return nil
		}
		return indexProgressFetchedMsg{progress: progress}
	}
}

// indexStatusLines renders the Workspace Search counts and a build-freshness
// hint as two independently style-able groups. On a wide sidebar the three
// counts share one line and the hint a second; on a narrow sidebar each count
// drops to its own short line so the trailing values are never truncated away.
// It measures fit against the *rendered* width (via the same styles the caller
// will apply), so styles that add padding — the hint's Reasoning style pads two
// columns — are accounted for and nothing clips.
func indexStatusLines(p *workspaceindex.IndexProgress, autoUpdate bool, width int, countsStyle, hintStyle lipgloss.Style) (counts, hint []string) {
	files := "Files " + abbrevNumber(p.FilesIndexed)
	syms := "Symbols " + abbrevNumber(p.SymbolsIndexed)
	docs := "Docs " + abbrevNumber(p.DocsIndexed)

	if joined := strings.Join([]string{files, syms, docs}, " · "); statusFits(countsStyle, joined, width) {
		counts = append(counts, joined)
	} else {
		counts = append(counts, files, syms, docs)
	}

	auto := "auto-update off"
	if autoUpdate {
		auto = "auto-update on"
	}
	fresh := indexFreshness(p)
	if joined := auto + " · " + fresh; statusFits(hintStyle, joined, width) {
		hint = append(hint, joined)
	} else {
		hint = append(hint, auto, fresh)
	}
	return counts, hint
}

// indexFreshness collapses the staleness signals into a single compact token.
func indexFreshness(p *workspaceindex.IndexProgress) string {
	switch {
	case p.Stale:
		return "partial (rebuild)"
	case p.UpdatedSinceBuild > 0 && !p.LastBuilt.IsZero():
		return "built " + relTimeStr(p.LastBuilt) + " (pending)"
	case !p.LastBuilt.IsZero():
		return "built " + relTimeStr(p.LastBuilt)
	default:
		return "never built"
	}
}

// statusFits reports whether s, once rendered with the given style, fits within
// width visible cells. Measuring the styled output (not the raw string) means
// style padding — such as the hint's two-column left pad — is counted, so the
// caller's width budget matches what the terminal actually draws.
func statusFits(style lipgloss.Style, s string, width int) bool {
	return lipgloss.Width(style.Render(s)) <= width
}

// truncRunes elides the middle of s so it fits within n runes.
func truncRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n || n <= 1 {
		return s
	}
	if n <= 3 {
		return string(r[:n-1]) + "…"
	}
	head := (n - 1) / 2
	tail := (n - 1) - head
	return string(r[:head]) + "…" + string(r[len(r)-tail:])
}

// relTimeStr renders a coarse "x ago" string for a timestamp.
func relTimeStr(t time.Time) string {
	if t.IsZero() {
		return "never"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return strconv.Itoa(int(d.Minutes())) + "m ago"
	case d < 24*time.Hour:
		return strconv.Itoa(int(d.Hours())) + "h ago"
	default:
		return strconv.Itoa(int(d.Hours()/24)) + "d ago"
	}
}

// gitBranchRefresh bounds how often the sidebar re-reads HEAD. The value is
// only consulted while the sidebar is already redrawing, so HEAD is probed
// at most once per interval even under continuous UI activity; no polling
// or watcher is involved, keeping input frames free of extra IO.
const gitBranchRefresh = 2 * time.Second

// gitBranch returns the current git branch of the workspace, or "" when the
// workspace is not a git checkout. It only reads HEAD, which is valid in a
// bare repo too: there the name comes from HEAD's contents instead of the
// directory name. A detached HEAD renders its short SHA. The result is
// cached and refreshed at most once per [gitBranchRefresh], so a checkout
// made by the agent or another terminal shows up without polling.
func (m *UI) gitBranch() string {
	if m.gitBranchLoaded && time.Since(m.gitBranchCheckedAt) < gitBranchRefresh {
		return m.gitBranchName
	}
	m.gitBranchLoaded = true
	m.gitBranchCheckedAt = time.Now()
	m.gitBranchName = readGitBranch(m.com.Workspace.WorkingDir())
	return m.gitBranchName
}

// readGitBranch resolves the branch for the repository rooted at dir. The
// .git entry may be a directory, a symlink to one, or a gitfile naming the
// real git directory (worktrees and submodules); each is resolved to a
// directory that holds HEAD.
func readGitBranch(dir string) string {
	gitDir := filepath.Join(dir, ".git")
	if st, err := os.Stat(gitDir); err == nil && st.IsDir() {
		if name, ok := headBranch(gitDir); ok {
			return name
		}
	} else if real, ok := gitFileDir(gitDir); ok {
		if name, ok := headBranch(real); ok {
			return name
		}
	}
	// The workspace path may itself be the git directory.
	if name, ok := headBranch(dir); ok {
		return name
	}
	return ""
}

// gitFileDir resolves the git directory referenced by a .git gitfile.
func gitFileDir(gitFile string) (string, bool) {
	data, err := os.ReadFile(gitFile)
	if err != nil {
		return "", false
	}
	path, ok := strings.CutPrefix(strings.TrimSpace(string(data)), "gitdir:")
	if !ok {
		return "", false
	}
	path = strings.TrimSpace(path)
	if path == "" {
		return "", false
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(filepath.Dir(gitFile), path)
	}
	return path, true
}

// headBranch reads the branch (or detached short SHA) recorded in HEAD.
func headBranch(gitDir string) (string, bool) {
	head := filepath.Join(gitDir, "HEAD")
	if st, err := os.Stat(head); err != nil || !st.Mode().IsRegular() {
		return "", false
	}
	data, err := os.ReadFile(head)
	if err != nil {
		return "", false
	}
	ref := strings.TrimSpace(string(data))
	if name, ok := strings.CutPrefix(ref, "ref: refs/heads/"); ok {
		if name = strings.TrimSpace(name); name != "" {
			return name, true
		}
		return "", false
	}
	if len(ref) >= 7 {
		return ref[:7], true
	}
	return "", false
}

func (m *UI) drawSidebar(scr uv.Screen, area uv.Rectangle) {
	if m.session == nil {
		return
	}

	width := area.Dx()
	height := area.Dy()

	// Get sidebar config, falling back to defaults.
	cfg := m.getSidebarConfig()

	// Filter non-hidden components; array position determines display order.
	var components []config.SidebarComponentConfig
	for _, comp := range cfg.Components {
		if !comp.Hidden {
			components = append(components, comp)
		}
	}

	// Fallback to default components if none configured.
	if len(components) == 0 {
		components = config.DefaultSidebarConfig().Components
	}

	// Render each component.
	var renderedSections []string
	for _, comp := range components {
		content := m.renderSidebarComponent(comp, width)
		if content != "" {
			renderedSections = append(renderedSections, content)
		}
	}
	// Every visible section refreshed its entry this pass; later tick-only
	// frames can reuse the cache until the next state-changing message.
	m.sidebarStale = false

	// Join sections with vertical gap.
	var content string
	if len(renderedSections) > 0 {
		content = joinWithVerticalGap(renderedSections, cfg.VerticalGap)
	}

	// Clamp scroll offset to valid range.
	contentLines := strings.Count(content, "\n") + 1
	maxScroll := max(0, contentLines-height)
	if m.sidebarScrollOffset < 0 {
		m.sidebarScrollOffset = 0
	}
	if maxScroll >= 0 && m.sidebarScrollOffset > maxScroll {
		m.sidebarScrollOffset = maxScroll
	}

	// Apply scroll offset: skip top lines and render the visible portion.
	if m.sidebarScrollOffset > 0 {
		visibleContent := strings.Split(content, "\n")
		if m.sidebarScrollOffset >= len(visibleContent) {
			m.sidebarScrollOffset = max(0, len(visibleContent)-1)
			visibleContent = visibleContent[1:]
		}
		content = strings.Join(visibleContent[m.sidebarScrollOffset:], "\n")
	}

	uv.NewStyledString(
		lipgloss.NewStyle().
			MaxWidth(width).
			MaxHeight(height).
			Render(content),
	).Draw(scr, area)
}

// getSidebarConfig returns the sidebar layout config from TUIOptions,
// falling back to the built-in defaults.
func (m *UI) getSidebarConfig() config.SidebarLayoutConfig {
	if m.com.Config().Options.TUI.Sidebar != nil {
		cfg := *m.com.Config().Options.TUI.Sidebar
		if cfg.VerticalGap == 0 {
			cfg.VerticalGap = config.DefaultSidebarConfig().VerticalGap
		}
		if cfg.Components == nil {
			cfg.Components = config.DefaultSidebarConfig().Components
		}
		return cfg
	}
	return config.DefaultSidebarConfig()
}

// renderSidebarComponent returns a section's rendered string, reusing the
// cached entry while the cache is fresh. Entries are keyed by component ID;
// width changes arrive as a WindowSizeMsg, which marks the cache stale.
// The working_dir entry is additionally rebuilt once the branch cache
// expires, so tick-only frames can pick up a checkout without waiting for
// a state-changing message.
func (m *UI) renderSidebarComponent(cfg config.SidebarComponentConfig, width int) string {
	if m.sidebarSections == nil {
		m.sidebarSections = make(map[string]string, len(m.getSidebarConfig().Components)+1)
	} else if !m.sidebarStale {
		if s, ok := m.sidebarSections[cfg.ID]; ok {
			if cfg.ID != "working_dir" || time.Since(m.gitBranchCheckedAt) < gitBranchRefresh {
				return s
			}
		}
	}
	s := m.renderSidebarSection(cfg, width)
	m.sidebarSections[cfg.ID] = s
	return s
}

// renderSidebarSection renders a single sidebar component by ID.
func (m *UI) renderSidebarSection(cfg config.SidebarComponentConfig, width int) string {
	t := m.com.Styles

	switch cfg.ID {
	case "logo":
		// The figlet render plus its gradient pass are pure functions of the
		// width and logo config, so reuse the string while those hold and only
		// re-render on a real change. See [UI.sidebarSmallLogo].
		key := strconv.Itoa(width) + "\x00" + t.LogoConfig.AppTitle + "\x00" + t.LogoConfig.SidebarLogoType + "\x00" + t.LogoConfig.SidebarFigletFont + "\x00" + strconv.FormatBool(m.com.IsHyper())
		if m.sidebarSmallLogoKey == key {
			return m.sidebarSmallLogo
		}
		sidebarLogo := logo.SmallRender(t, width, 3, logo.Opts{
			AppTitle:          t.LogoConfig.AppTitle,
			Hyper:             m.com.IsHyper(),
			SidebarLogoPlain:  t.LogoConfig.SidebarLogoType == "plain_text",
			SidebarLogoHidden: t.LogoConfig.SidebarLogoType == "hidden",
			SidebarFigletFont: t.LogoConfig.SidebarFigletFont,
		})
		m.sidebarSmallLogo, m.sidebarSmallLogoKey = sidebarLogo, key
		return sidebarLogo
	case "session_title":
		title := m.session.Title
		if m.session.IsPinned {
			title = "★ " + title
		}
		return t.Sidebar.SessionTitle.Width(width).MaxHeight(2).Render(title)
	case "working_dir":
		path := common.PrettyPath(t, m.com.Workspace.WorkingDir(), width)
		if branch := m.gitBranch(); branch != "" {
			path = lipgloss.JoinVertical(
				lipgloss.Left,
				path,
				t.ModelInfo.Reasoning.Width(width).Render(branch),
			)
		}
		return path
	case "active_llm":
		return m.modelInfo(width)
	case "goal":
		return m.goalInfo(width)
	case "workspace_search":
		return m.codebaseIndexInfo(width)
	case "files":
		return m.sidebarListComponent(cfg, width)
	case "lsps":
		return m.sidebarListComponent(cfg, width)
	case "mcps":
		return m.sidebarListComponent(cfg, width)
	case "skills":
		return m.sidebarListComponent(cfg, width)
	case "memory":
		return m.memoryInfo(width)
	default:
		return ""
	}
}

// sidebarListComponent handles list-type sidebar components (files, lsps, mcps, skills)
// with a default max_items of 10 if not specified.
func (m *UI) sidebarListComponent(cfg config.SidebarComponentConfig, width int) string {
	maxItems := cfg.MaxItems
	if maxItems == 0 {
		maxItems = 10
	}

	switch cfg.ID {
	case "files":
		return m.filesInfo(m.com.Workspace.WorkingDir(), width, maxItems, true)
	case "lsps":
		return m.lspInfo(width, maxItems, true)
	case "mcps":
		return m.mcpInfo(width, maxItems, true)
	case "skills":
		return m.skillsInfo(width, maxItems, true)
	default:
		return ""
	}
}

// abbrevNumber formats large numbers with K/M suffixes (e.g. 776849 → "776K").
func abbrevNumber(n int) string {
	switch {
	case n >= 1_000_000:
		formatted := fmt.Sprintf("%.1fM", float64(n)/1_000_000)
		if strings.HasSuffix(formatted, ".0M") {
			formatted = strings.Replace(formatted, ".0M", "M", 1)
		}
		return formatted
	case n >= 1_000:
		formatted := fmt.Sprintf("%.1fK", float64(n)/1_000)
		if strings.HasSuffix(formatted, ".0K") {
			formatted = strings.Replace(formatted, ".0K", "K", 1)
		}
		return formatted
	default:
		return fmt.Sprintf("%d", n)
	}
}
