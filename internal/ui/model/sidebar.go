package model

import (
	"cmp"
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/hackafterdark/phosphor/internal/ui/common"
	"github.com/hackafterdark/phosphor/internal/ui/logo"
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
			ModelContext:   model.CatwalkCfg.ContextWindow, // contextWindow,
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
		// Refresh progress only once per second to avoid per-draw SQL queries.
		if m.indexProgress == nil || time.Since(m.indexProgressTime) > time.Second {
			progress, err := m.symbolIndex.GetProgress(context.Background())
			if err == nil {
				m.indexProgress = progress
				m.indexProgressTime = time.Now()
			}
		}
		if m.indexProgress != nil {
			p := m.indexProgress
			if p.Complete {
				statusParts = append(statusParts, t.ModelInfo.Provider.Render("Files: "+abbrevNumber(p.FilesIndexed)))
				statusParts = append(statusParts, t.ModelInfo.Provider.Render("Symbols: "+abbrevNumber(p.SymbolsIndexed)))
				statusParts = append(statusParts, t.ModelInfo.Provider.Render("Docs: "+abbrevNumber(p.DocsIndexed)))
			} else {
				statusParts = append(statusParts, t.ModelInfo.Provider.Render("Building index..."))
			}
		}
	}
	for _, line := range statusParts {
		parts = append(parts, line)
	}

	return lipgloss.JoinVertical(lipgloss.Left, lipgloss.JoinVertical(lipgloss.Left, parts...))
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

// renderSidebarComponent renders a single sidebar component by ID.
func (m *UI) renderSidebarComponent(cfg config.SidebarComponentConfig, width int) string {
	t := m.com.Styles

	switch cfg.ID {
	case "logo":
		sidebarLogo := logo.SmallRender(t, width, 3, logo.Opts{
			AppTitle:          t.LogoConfig.AppTitle,
			Hyper:             m.com.IsHyper(),
			SidebarLogoPlain:  t.LogoConfig.SidebarLogoType == "plain_text",
			SidebarLogoHidden: t.LogoConfig.SidebarLogoType == "hidden",
			SidebarFigletFont: t.LogoConfig.SidebarFigletFont,
		})
		if sidebarLogo != "" {
			return sidebarLogo
		}
		return ""
	case "session_title":
		title := m.session.Title
		if m.session.IsPinned {
			title = "★ " + title
		}
		return t.Sidebar.SessionTitle.Width(width).MaxHeight(2).Render(title)
	case "working_dir":
		return common.PrettyPath(t, m.com.Workspace.WorkingDir(), width)
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
